package service

import (
	"context"
	"math/rand"
	"strings"

	"myproject/internal/data"
	"myproject/internal/engine"
	"myproject/internal/model"
	"myproject/pkg/errcode"
)

// Eval 线下测试求值（供程序调用）。
//
// 决策树（对齐资料 /engine/api/eval）：
//  1. 显式 version → 锁定该版本，跳过灰度；
//  2. offline_flag → 读 latest 草稿（草稿优先），跳过灰度；
//  3. 默认 → 读指针 cur_ver → 版本化快照，miss 回源 DB 自愈；
//  4. 灰度判定：cut_num ∈ (0,1) 且非 offline 且非显式 version 且有 NewVersion，
//     按 rand < cut_num 抽样执行待上线版本。
func Eval(ctx context.Context, req *model.EvalRequest) (*model.EvalResponse, error) {
	snap := resolveSnapshot(ctx, req)
	if snap == nil {
		return nil, errcode.ErrConfigNotFound.WithDetails("未找到配置 %s/%s", req.Pack, req.Key)
	}
	inputData, err := normalizeData(req.Data)
	if err != nil {
		return nil, err
	}

	// 命名空间校验：规则字段的 parse_path 强制以项目 logo 开头（如 risk_engine.account_age），
	// 因此入参必须按项目 logo 命名空间（{"risk_engine": {...}}）。未按命名空间传递时，
	// 按路径提取不到值会静默回退到字段默认值，导致外部调用方拿到「看似正常但其实是默认值算出来」的结果。
	if err := checkNamespace(req.Pack, snap, inputData); err != nil {
		return nil, err
	}

	// 灰度判定
	target := snap
	if snap.CutNum > 0 && snap.CutNum < 1 && !req.OfflineFlag && req.Version == "" && snap.NewVersion != nil {
		if rand.Float64() < snap.CutNum {
			target = snap.NewVersion
		}
	}

	value, resultType, err := executeSnapshot(target, inputData)
	if err != nil {
		return nil, err
	}

	return &model.EvalResponse{
		Pack:       target.Pack,
		Key:        target.Extension,
		Version:    target.Version,
		Value:      value,
		ResultType: resultType,
	}, nil
}

// resolveSnapshot 按决策树取快照（含回源自愈）。
func resolveSnapshot(ctx context.Context, req *model.EvalRequest) *model.ConfigSnapshot {
	if req.Version != "" {
		// 显式版本：锁定该版本，跳过灰度
		if snap := data.GetSnapshot(ctx, req.Pack, req.Key, req.Version); snap != nil {
			return snap
		}
		c, err := data.GetConfigByLogoAndVersion(ctx, req.Key, req.Version)
		if err != nil {
			return nil
		}
		snap, _ := buildSnapshot(ctx, c)
		// 显式 version 分支只读不写缓存：该分支可能指向「待审核草稿」，
		// 而草稿会被原地编辑（版本号不变）——一旦写入版本化快照，就会在
		// 发布后被默认路径命中，导致线上执行旧规则（TTL 内难以察觉）。
		return snap
	}

	if req.OfflineFlag {
		// 草稿优先：读 latest，miss 回源（is_latest=1 优先，否则生效版本）
		if snap := data.GetLatestSnapshot(ctx, req.Pack, req.Key); snap != nil {
			return snap
		}
		c, err := data.GetLatestConfigByLogo(ctx, req.Key)
		if err != nil {
			c, err = data.GetActiveConfigByLogo(ctx, req.Key)
			if err != nil {
				return nil
			}
		}
		snap, _ := buildSnapshot(ctx, c)
		_ = data.SetLatestSnapshot(ctx, req.Pack, req.Key, snap)
		return snap
	}

	// 默认：指针 → 版本化快照
	version := data.GetCurVer(ctx, req.Pack, req.Key)
	if version == "" {
		// 指针 miss → 回源 DB 拿生效版本
		active, err := data.GetActiveConfigByLogo(ctx, req.Key)
		if err != nil {
			return nil
		}
		version = active.Version
	}
	if snap := data.GetSnapshot(ctx, req.Pack, req.Key, version); snap != nil {
		return snap
	}
	// 回源 DB 组装（含灰度自愈）+ 回填
	c, err := data.GetConfigByLogoAndVersion(ctx, req.Key, version)
	if err != nil {
		return nil
	}
	snap := buildSnapshotWithGray(ctx, c)
	_ = data.SetSnapshot(ctx, req.Pack, req.Key, version, snap)
	return snap
}

// executeSnapshot 从快照执行规则（组装 env + 提取字段 + Starlark 执行 + 转换）。
func executeSnapshot(snap *model.ConfigSnapshot, data map[string]any) (interface{}, string, error) {
	if snap.Type != model.ConfigTypeRule || snap.Rule == nil {
		if snap.Type == model.ConfigTypeRule {
			return nil, "", errcode.ErrRuleNotFound.WithDetails("规则 %s/%s 尚未配置规则内容", snap.Pack, snap.Extension)
		}
		return nil, "", errcode.ErrConfigStatusInvalid.WithDetails("非规则类型暂不支持执行")
	}
	fields := make([]engine.FieldMeta, 0, len(snap.BindVar))
	for _, f := range snap.BindVar {
		fields = append(fields, engine.FieldMeta{
			ID:           f.ID,
			Name:         f.Name,
			Type:         f.Type,
			ParsePath:    f.ParsePath,
			DefaultValue: f.DefaultValue,
		})
	}
	value, err := engine.Execute(snap.Rule.Script, snap.Rule.ResultType, fields, data)
	if err != nil {
		return nil, "", errcode.ErrRuleExecuteFailed.WithDetails("%s", err.Error())
	}
	return value, snap.Rule.ResultType, nil
}

// checkNamespace 校验入参按「项目 logo」命名空间传递，避免字段静默回退默认值导致误判。
// 规则字段的 parse_path 强制以项目 logo 开头（如 risk_engine.account_age），因此入参必须形如
// {"risk_engine": {...}}；否则按路径提取不到值会落到字段默认值，外部调用方难以察觉。
func checkNamespace(pack string, snap *model.ConfigSnapshot, data map[string]any) error {
	if _, ok := data[pack]; !ok {
		return errcode.ErrInvalidParams.WithDetails(
			"参数未按项目命名空间传递：缺少顶层键 %q，请使用 {\"%s\": {...}} 结构", pack, pack)
	}
	var missing []string
	for _, f := range snap.BindVar {
		if !pathExists(data, f.ParsePath) {
			missing = append(missing, f.ParsePath)
		}
	}
	if len(missing) > 0 {
		return errcode.ErrInvalidParams.WithDetails(
			"参数未按命名空间传递，以下字段路径在入参中缺失：%s", strings.Join(missing, ", "))
	}
	return nil
}

// pathExists 判断 dotted path 在 data 中是否「存在」（值可为任意类型，包括 0/""），
// 仅校验结构是否存在，不取具体值——以此区分「未传」与「传了空值」。
func pathExists(data map[string]any, path string) bool {
	parts := strings.Split(path, ".")
	cur := any(data)
	for _, p := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return false
		}
		v, ok := m[p]
		if !ok {
			return false
		}
		cur = v
	}
	return true
}
