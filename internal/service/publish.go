package service

import (
	"context"
	"time"

	"myproject/internal/data"
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
		// ① 下线其它生效版本（连带清灰度字段）
		if err := data.OfflineOtherVersions(ctx, c.Logo, c.ID); err != nil {
			return err
		}
		// ② 激活当前版本
		return data.ActiveConfig(ctx, c.ID)
	}); err != nil {
		return nil, err
	}

	// ③ commit 成功后写版本指针（事务外，避免事务内操作非事务资源）
	if err := data.SetCurVer(ctx, pack, c.Logo, c.Version); err != nil {
		// 指针写失败不影响 DB 已提交的发布结果（config 已激活）。
		// 这里不能返回错误：DB 已发布，用户重试会报「已生效」，陷入死胡同。
		// eval 侧指针 miss 时会回源 DB 查 status=1 版本并自愈，因此只记日志。
		logger.C(ctx).Warn("发布成功但版本指针写失败（eval 将回源自愈）",
			"pack", pack, "logo", c.Logo, "version", c.Version, "err", err.Error())
	}

	// 回读最新状态返回
	latest, err := getConfig(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	return latest.ToResponse(), nil
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
	// ① 切流比例范围校验（资料缺失，越界值会静默失效）
	if req.CutNum <= 0 || req.CutNum >= 1 {
		return nil, errcode.ErrCutNumInvalid
	}

	// ② 待上线版本
	c, err := getConfig(ctx, req.ConfigID)
	if err != nil {
		return nil, err
	}
	if c.Status != model.ConfigStatusPending {
		return nil, errcode.ErrConfigStatusInvalid.WithDetails("仅待审核版本可切流，当前状态 %d", c.Status)
	}
	// 规则类型必须先有规则内容，否则切流后灰度流量会报「规则未配置」
	if err := ensureRuleConfigured(ctx, c); err != nil {
		return nil, err
	}

	// ③ 当前生效版本（灰度状态挂在其行上）
	active, err := data.GetActiveConfigByLogo(ctx, c.Logo)
	if err != nil {
		if data.IsNotFound(err) {
			return nil, errcode.ErrConfigNoActiveVersion
		}
		return nil, err
	}

	// ④ 写老版本行的切流字段
	now := time.Now().Unix()
	if err := data.UpdateConfigCut(ctx, active.ID, req.CutNum, c.Version, username, now); err != nil {
		return nil, err
	}

	// 重读 active（拿最新 cut 字段）
	active2, err := data.GetConfigByID(ctx, active.ID)
	if err != nil {
		return nil, err
	}

	// ⑤ 组装快照：老版本全量 + 新版本灰度详情
	activeSnap, err := buildSnapshot(ctx, active2)
	if err != nil {
		return nil, err
	}
	newSnap, err := buildSnapshot(ctx, c)
	if err != nil {
		return nil, err
	}
	activeSnap.NewVersion = newSnap

	// ⑥ 投放快照（7 天 TTL）
	pack := packOf(ctx, active2.ProjectID)
	if err := data.SetSnapshot(ctx, pack, active2.Logo, active2.Version, activeSnap); err != nil {
		// 灰度标记已写入 DB（事实源），但快照未投放成功 → 灰度不会生效。
		// 返回错误引导重试（重试幂等，会重新组装并投放快照）。
		logger.C(ctx).Error("切流快照投放失败，灰度标记已写入 DB",
			"pack", pack, "logo", active2.Logo, "version", active2.Version, "err", err.Error())
		return nil, errcode.ErrInternal.WithDetails("切流已记录但缓存投放失败，请重试")
	}

	return active2.ToResponse(), nil
}

// ensureRuleConfigured type=rule 的 config 必须有规则内容才能发布/切流，
// 避免发布无内容的规则导致线上 eval 报「规则未配置」。
func ensureRuleConfigured(ctx context.Context, c *model.Config) error {
	if c.Type != model.ConfigTypeRule {
		return nil
	}
	if _, err := data.GetRuleByConfigID(ctx, c.ID); err != nil {
		if data.IsNotFound(err) {
			return errcode.ErrRuleNotFound.WithDetails("请先在「规则管理」中保存规则内容后再发布/切流")
		}
		return err
	}
	return nil
}
