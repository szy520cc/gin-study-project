package service

import (
	"context"
	"time"

	"myproject/internal/data"
	"myproject/internal/engine"
	"myproject/internal/model"
	"myproject/pkg/errcode"
	"myproject/pkg/logger"
	"myproject/pkg/transaction"
)

// Publish 全量发布（灰度链路的唯一出口）。
//
// 流程（对齐资料 publish）：
//  1. 校验：待发布版本必须是待审核(0)；已生效则拒绝（幂等保护）；
//  2. 事务内：下线同 logo 其它生效版本（status 1→2，并清灰度字段）→ 激活当前（0→1）；
//  3. 事务提交成功后写版本指针 cur_ver_{pack}_{ext}（Redis 无 TTL）。
//
// 不删任何版本化快照 key：指针切换后旧 key 自然成孤儿，靠 7 天 TTL 淘汰。
func Publish(ctx context.Context, configID uint64) (*model.ConfigResponse, error) {
	c, err := getConfig(ctx, configID)
	if err != nil {
		return nil, err
	}
	if c.Status == model.ConfigStatusActive {
		return nil, errcode.ErrConfigAlreadyActive
	}
	if c.Status != model.ConfigStatusPending {
		return nil, errcode.ErrConfigStatusInvalid.WithDetails("仅待审核状态可发布，当前状态 %d", c.Status)
	}
	// 规则类型必须先有规则内容，否则发布后 eval 线上请求会报「规则未配置」
	if err := ensureRuleConfigured(ctx, c); err != nil {
		return nil, err
	}
	pack := packOf(ctx, c.ProjectID)

	if err := transaction.Do(ctx, func(ctx context.Context) error {
		// ① 锁定同 logo 全部版本行，与切流串行化（避免「读状态 → 写状态」并发交错）
		if _, lerr := data.LockConfigsByLogo(ctx, c.Logo); lerr != nil {
			return lerr
		}
		// ② 锁内重读状态，防止加锁前的判断已过期
		cur, cerr := data.GetConfigByID(ctx, c.ID)
		if cerr != nil {
			return cerr
		}
		if cur.Status == model.ConfigStatusActive {
			return errcode.ErrConfigAlreadyActive
		}
		if cur.Status != model.ConfigStatusPending {
			return errcode.ErrConfigStatusInvalid.WithDetails("仅待审核状态可发布，当前状态 %d", cur.Status)
		}
		// ③ 下线其它生效版本（连带清灰度字段）
		if err := data.OfflineOtherVersions(ctx, c.Logo, c.ID); err != nil {
			return err
		}
		// ④ 激活当前版本
		return data.ActiveConfig(ctx, c.ID)
	}); err != nil {
		return nil, err
	}

	// ⑤ commit 成功后写版本指针（事务外，避免事务内操作非事务资源）
	if err := data.SetCurVer(ctx, pack, c.Logo, c.Version); err != nil {
		// 指针写失败不影响 DB 已提交的发布结果（config 已激活）。
		// 这里不能返回错误：DB 已发布，用户重试会报「已生效」，陷入死胡同。
		// eval 侧指针 miss 时会回源 DB 查 status=1 版本并自愈，因此只记日志。
		logger.C(ctx).Warn("发布成功但版本指针写失败（eval 将回源自愈）",
			"pack", pack, "logo", c.Logo, "version", c.Version, "err", err.Error())
	}

	// 发布成功后，该版本即为线上版本；统一校正 is_latest，确保一个 logo 只有一个最新版本。
	if err := data.EnsureOnlyLatest(ctx, c.Logo, c.ID); err != nil {
		logger.C(ctx).Warn("发布后校正 is_latest 失败", "logo", c.Logo, "id", c.ID, "err", err.Error())
	}

	// 回读最新状态返回
	latest, err := getConfig(ctx, c.ID)
	if err != nil {
		return nil, err
	}

	// 刷新「新上线版本」的版本化快照 + 清草稿快照：
	// 防止历史遗留的陈旧快照（例如该版本待审核时曾被显式 version 求值写入）在发布后被默认路径命中。
	if snap, serr := buildSnapshot(ctx, latest); serr != nil {
		logger.C(ctx).Warn("发布后组装版本快照失败（eval 将回源自愈）",
			"logo", latest.Logo, "version", latest.Version, "err", serr.Error())
	} else if serr := data.SetSnapshot(ctx, pack, latest.Logo, latest.Version, snap); serr != nil {
		logger.C(ctx).Warn("发布后刷新版本快照失败（eval 将回源自愈）",
			"logo", latest.Logo, "version", latest.Version, "err", serr.Error())
	}
	_ = data.DelLatest(ctx, pack, c.Logo)

	resp := latest.ToResponse()
	if err := fillConfigFlags(ctx, []*model.ConfigResponse{resp}); err != nil {
		return nil, err
	}
	return resp, nil
}

// CutProgress 灰度切流：把 cut_num 流量切给待上线版本。
//
// 流程（对齐资料 cutprogress，并修复其 cut_num 无范围校验的缺陷）：
//  1. 校验 cut_num ∈ (0,1)；
//  2. 取待上线版本（status=0）；
//  3. 查同 logo 的当前生效版本（status=1）——灰度状态挂在老版本行上；
//  4. 写老版本行的 cut_num/cut_version/cut_by/cut_at；
//  5. 组装「老版本全量 + 新版本 NewVersionDetail」快照，写版本化 key（7 天 TTL）。
func CutProgress(ctx context.Context, username string, req *model.CutProgressRequest) (*model.ConfigResponse, error) {
	// ① 切流比例范围校验：0 表示取消切流；正常切流必须在 (0,1)
	if req.CutNum < 0 || req.CutNum >= 1 {
		return nil, errcode.ErrCutNumInvalid
	}
	cancel := req.CutNum == 0

	// ② 待上线版本（先读一次拿 logo 用于加锁；状态会在锁内重校）
	c, err := getConfig(ctx, req.ConfigID)
	if err != nil {
		return nil, err
	}
	if c.Status != model.ConfigStatusPending {
		return nil, errcode.ErrConfigStatusInvalid.WithDetails("仅待审核版本可切流，当前状态 %d", c.Status)
	}
	if !cancel {
		// 取消切流不涉及规则上线；正常切流要求规则内容就绪，否则灰度流量会报「规则未配置」。
		if err := ensureRuleConfigured(ctx, c); err != nil {
			return nil, err
		}
	}

	// ③ 事务内：锁住同 logo 全部版本行（与 Publish 共用同一把锁 → 二者互斥）
	//    → 锁内重校状态 → 写/清切流字段 → 锁内重读拿到最终状态。
	//    快照投放在事务外，但只投给「这里确定的那个 active 版本」，避免 commit 与投放
	//    之间被并发发布换掉 active 版本（否则会把灰度挂到刚上线的版本上）。
	var activeAfter, pendingAfter *model.Config
	if err := transaction.Do(ctx, func(ctx context.Context) error {
		if _, lerr := data.LockConfigsByLogo(ctx, c.Logo); lerr != nil {
			return lerr
		}
		cur, cerr := data.GetConfigByID(ctx, c.ID)
		if cerr != nil {
			return cerr
		}
		if cur.Status != model.ConfigStatusPending {
			return errcode.ErrConfigStatusInvalid.WithDetails("仅待审核版本可切流，当前状态 %d", cur.Status)
		}
		pendingAfter = cur

		active, aerr := data.GetActiveConfigByLogo(ctx, c.Logo)
		if aerr != nil {
			if data.IsNotFound(aerr) {
				// 取消切流幂等：本就没有生效版本 = 已是未切流态，直接成功。
				if cancel {
					return nil
				}
				return errcode.ErrConfigNoActiveVersion
			}
			return aerr
		}

		if cancel {
			// 取消：清空全部切流字段，回到「从未切流」的干净状态（不留操作者/时间残留）。
			if uerr := data.ClearConfigCut(ctx, active.ID); uerr != nil {
				return uerr
			}
		} else {
			if uerr := data.UpdateConfigCut(ctx, active.ID, req.CutNum, cur.Version, username, time.Now().Unix()); uerr != nil {
				return uerr
			}
		}

		// 锁内重读：拿到写入后的切流字段，且确保仍是刚被锁定的那一行。
		latest, lerr := data.GetConfigByID(ctx, active.ID)
		if lerr != nil {
			return lerr
		}
		activeAfter = latest
		return nil
	}); err != nil {
		return nil, err
	}

	// ④ 事务外：重建并投放到「事务内确定的 active 版本」的 key
	//    （正常切流=老版本+新版本；取消=仅老版本自身）。
	//    activeAfter 为 nil 表示「无生效版本且是取消切流」→ 幂等成功，无需投放。
	resp := pendingAfter.ToResponse()
	if activeAfter != nil {
		snap, berr := buildSnapshot(ctx, activeAfter)
		if berr != nil {
			return nil, berr
		}
		if !cancel {
			newSnap, nerr := buildSnapshot(ctx, pendingAfter)
			if nerr != nil {
				return nil, nerr
			}
			snap.NewVersion = newSnap
		}
		pack := packOf(ctx, activeAfter.ProjectID)
		if serr := data.SetSnapshot(ctx, pack, activeAfter.Logo, activeAfter.Version, snap); serr != nil {
			// 灰度标记已写入 DB（事实源），但快照未投放成功 → 灰度不会生效。
			// 返回错误引导重试（重试幂等，会重新组装并投放快照）。
			logger.C(ctx).Error("切流快照投放失败，灰度标记已写入 DB",
				"pack", pack, "logo", activeAfter.Logo, "version", activeAfter.Version, "err", serr.Error())
			return nil, errcode.ErrInternal.WithDetails("切流已记录但缓存投放失败，请重试")
		}
		resp = activeAfter.ToResponse()
	}

	action := "cut"
	if cancel {
		action = "cancel"
	}
	// 审计：cut_* 只表达「当前灰度状态」，谁在何时切流/取消走日志，避免业务字段残留。
	logger.C(ctx).Info("config cut changed",
		"action", action, "logo", c.Logo, "operator", username, "cut_num", req.CutNum)

	if err := fillConfigFlags(ctx, []*model.ConfigResponse{resp}); err != nil {
		return nil, err
	}
	return resp, nil
}

// invalidateGraySnapshotForDraft 若被编辑的版本正是「当前生效版本灰度指向的待上线版本」，
// 就删掉生效版本的版本化快照，让下次 eval 回源重建。
//
// 为什么需要：灰度快照里内嵌了待上线版本（NewVersion）的规则副本，而原地编辑草稿
// 既不改版本号、也不碰生效版本的 key —— 不删的话灰度流量会继续执行旧草稿规则，
// 直到下次点切流或 7 天 TTL 到期（用户无感知）。
func invalidateGraySnapshotForDraft(ctx context.Context, pack, logo, editedVersion string) {
	if editedVersion == "" {
		return
	}
	active, err := data.GetActiveConfigByLogo(ctx, logo)
	if err != nil {
		return // 无生效版本（或查询失败）：没有灰度快照可失效
	}
	if active.CutVersion != editedVersion {
		return // 编辑的不是灰度目标版本，不涉及灰度快照
	}
	if derr := data.DelSnapshot(ctx, pack, logo, active.Version); derr != nil {
		logger.C(ctx).Warn("灰度中编辑待上线版本后失效生效版本快照失败（下次切流会重建）",
			"pack", pack, "logo", logo, "active_version", active.Version, "err", derr.Error())
	}
}

// ensureRuleConfigured type=rule 的 config 必须有「可用的」规则内容才能发布/切流。
//
// 两道检查：
//  1. 规则记录存在 —— 否则发布/切流后线上 eval 会报「规则未配置」；
//  2. 库里存的规则脚本能通过 Starlark 编译校验 —— 防止历史脏数据，
//     或规则行被绕过保存接口直接改坏后仍被发布上线。
//
// 这是「规则验证通过才能切流/推全」在服务端的兜底：前端按钮可见性只是体验，
// 真正的闸门在这里。
func ensureRuleConfigured(ctx context.Context, c *model.Config) error {
	if c.Type != model.ConfigTypeRule {
		return nil
	}
	r, err := data.GetRuleByConfigID(ctx, c.ID)
	if err != nil {
		if data.IsNotFound(err) {
			return errcode.ErrRuleNotFound.WithDetails("请先在「规则管理」中保存规则内容后再发布/切流")
		}
		return err
	}
	if verr := engine.ValidateStarlark(r.Rule); verr != nil {
		return errcode.ErrRuleInvalid.WithDetails("已保存的规则未通过校验，请重新编辑保存：%s", verr.Error())
	}
	return nil
}
