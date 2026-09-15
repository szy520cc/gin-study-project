package service

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"

	"myproject/internal/data"
	"myproject/internal/model"
	"myproject/pkg/errcode"
	"myproject/pkg/logger"
	"myproject/pkg/transaction"
)

var fieldPlaceholderRE = regexp.MustCompile(`##(\d+)\*\*([^#]+)##`)

// CreateConfig 创建配置。
// logo+version 组合唯一；status 缺省为待审核(0)，is_latest 缺省为是(1)。
func CreateConfig(ctx context.Context, username string, req *model.CreateConfigRequest) (*model.ConfigResponse, error) {
	now := time.Now().Unix()
	isLatest := req.IsLatest
	if isLatest == 0 {
		isLatest = model.ConfigLatestYes
	}

	c := &model.Config{
		ProjectID:    strings.TrimSpace(req.ProjectID),
		ConfigPackID: req.ConfigPackID,
		Name:         strings.TrimSpace(req.Name),
		Logo:         strings.TrimSpace(req.Logo),
		Type:         strings.TrimSpace(req.Type),
		Version:      strings.TrimSpace(req.Version),
		Status:       req.Status, // 0 即待审核，合法状态
		IsLatest:     isLatest,
		Remark:       strings.TrimSpace(req.Remark),
		CreatedUser:  username,
		UpdatedUser:  username,
		CreatedAt:    now,
		UpdatedAt:    now,
		CutNum:       req.CutNum,
		CutAt:        req.CutAt,
		CutVersion:   strings.TrimSpace(req.CutVersion),
	}

	if err := data.CreateConfig(ctx, c); err != nil {
		if data.IsDuplicate(err) {
			return nil, errcode.ErrConfigLogoVersionExists.WithDetails("配置标识 %s 的版本 %s 已存在", c.Logo, c.Version)
		}
		return nil, err
	}
	// 保证同一 logo 下只有这个新创建的版本是最新版本。
	if err := data.EnsureOnlyLatest(ctx, c.Logo, c.ID); err != nil {
		return nil, err
	}
	return c.ToResponse(), nil
}

// GetConfig 获取配置详情
func GetConfig(ctx context.Context, id uint64) (*model.ConfigResponse, error) {
	c, err := getConfig(ctx, id)
	if err != nil {
		return nil, err
	}
	resp := c.ToResponse()
	if err := fillConfigNames(ctx, []*model.ConfigResponse{resp}); err != nil {
		return nil, err
	}
	if err := fillConfigFlags(ctx, []*model.ConfigResponse{resp}); err != nil {
		return nil, err
	}
	if err := fillConfigCutInfo(ctx, []*model.ConfigResponse{resp}); err != nil {
		return nil, err
	}
	return resp, nil
}

// UpdateConfig 更新配置（对齐资料逻辑：不可变发布链 + 状态机约束）。
//
// 与 UpdateRuleConfig 同构的基本信息保存：
//   - 身份字段（type 为配置身份一部分）不可改；
//   - 草稿（status=0）：原地更新 name/remark；
//   - 生效（status=1）：fork 新版本（status=0、is_latest=1），老版本 is_latest→2 继续生效，
//     并本次提交的 name/remark 写到新版本；
//   - 保存后删除 latest 草稿快照缓存（避免 offline 读到旧草稿）。
//
// 状态机硬约束（资料要求）：status 只由发布接口改、cut_* 只由切流接口改，
// 本接口一律拒绝这两个字段的写入，禁止绕过发布链/切流状态机。
func UpdateConfig(ctx context.Context, id uint64, username string, req *model.UpdateConfigRequest) error {
	existing, err := getConfig(ctx, id)
	if err != nil {
		return err
	}

	// 身份字段不可改：type 是配置身份的一部分，不允许变更。
	if req.Type != "" && req.Type != existing.Type {
		return errcode.ErrConfigStatusInvalid.WithDetails("配置类型不可修改")
	}
	// 状态机约束：status 只由 publish 改、cut_* 只由 cutprogress 改。
	if req.Status != 0 {
		return errcode.ErrConfigStatusInvalid.WithDetails("配置状态只能由发布接口修改，不可在编辑时直接变更")
	}
	if req.CutNum != 0 || req.CutAt != 0 || req.CutVersion != "" {
		return errcode.ErrConfigStatusInvalid.WithDetails("切流字段只能由切流接口修改，不可在编辑时直接变更")
	}

	name := strings.TrimSpace(req.Name)
	remark := strings.TrimSpace(req.Remark)
	now := time.Now().Unix()
	pack := packOf(ctx, existing.ProjectID)

	err = transaction.Do(ctx, func(ctx context.Context) error {
		target := existing
		if existing.Status == model.ConfigStatusActive {
			// 编辑生效版本 → fork 新版本（不可变发布链），新版本行在下方写入本次提交的 name/remark
			forked, ferr := forkConfig(ctx, existing, username, now)
			if ferr != nil {
				return ferr
			}
			target = forked
		}
		if uerr := data.UpdateConfigBasic(ctx, target.ID, name, remark, username, now); uerr != nil {
			return uerr
		}
		return nil
	})
	if err != nil {
		return err
	}
	// 编辑必删 latest（与 SaveRule / UpdateRuleConfig 一致）
	_ = data.DelLatest(ctx, pack, existing.Logo)
	return nil
}

// DeleteConfig 删除配置（联动删除其规则子表，避免孤儿数据）。
//
// 删除后同步修正缓存与「最新版本」标记（纯 DB/Redis 收尾，失败不回滚删除结果）：
//   - 清 latest 草稿快照 + 该版本的版本化快照 → 避免 offline/求值读到已删内容（最长 7 天）；
//   - 若删掉的正是版本指针所指版本 → 清掉指针，避免 eval 一直指向不存在的版本；
//   - 若删掉的是 is_latest 版本 → 把标记归还给剩余版本（否则 is_latest=1 查不到任何行）。
func DeleteConfig(ctx context.Context, id uint64) error {
	c, err := getConfig(ctx, id)
	if err != nil {
		return err
	}
	pack := packOf(ctx, c.ProjectID)

	if err := transaction.Do(ctx, func(ctx context.Context) error {
		if err := data.DeleteRuleByConfigID(ctx, id); err != nil {
			return err
		}
		return data.DeleteConfig(ctx, id)
	}); err != nil {
		return err
	}

	_ = data.DelLatest(ctx, pack, c.Logo)
	_ = data.DelSnapshot(ctx, pack, c.Logo, c.Version)
	if data.GetCurVer(ctx, pack, c.Logo) == c.Version {
		_ = data.DelCurVer(ctx, pack, c.Logo)
	}
	if c.IsLatest == model.ConfigLatestYes {
		if rerr := data.RestoreLatest(ctx, c.Logo); rerr != nil {
			logger.C(ctx).Warn("删除配置后归还 is_latest 失败（列表按 logo 聚合不受影响）",
				"logo", c.Logo, "err", rerr.Error())
		}
	}
	return nil
}

// ListConfigs 分页查询配置。
// 列表按「配置」维度展示：同一 logo 只取最新版本，避免满屏都是同一配置的历史版本。
func ListConfigs(ctx context.Context, req *model.ConfigListRequest) ([]*model.ConfigResponse, int64, error) {
	page, pageSize := model.NormalizePage(req.Page, req.PageSize)

	// 列表始终只展示最新版本；历史版本通过「配置详情/版本」查看。
	list, total, err := data.ListConfigs(ctx, req.ProjectID, req.ConfigPackID, req.Name, req.Type, req.Status, page, pageSize)
	if err != nil {
		return nil, 0, err
	}

	res := make([]*model.ConfigResponse, 0, len(list))
	for _, c := range list {
		// 列表按 logo 聚合，返回的已经是该 logo 下 id 最大的版本，
		// 因此其 is_latest 语义上一定是 1（避免 DB 中 is_latest 不一致时展示错误）。
		c.IsLatest = model.ConfigLatestYes
		res = append(res, c.ToResponse())
	}
	// 回填项目名称与配置包名称：两个 ID 直接展示都是一串编号
	if err := fillConfigNames(ctx, res); err != nil {
		return nil, 0, err
	}
	// 回填切流/发布前置标记（has_active_version / rule_ready）
	if err := fillConfigFlags(ctx, res); err != nil {
		return nil, 0, err
	}
	// 回填切流信息：从同 logo 的线上版本行取（切流标记写在线上版本上）
	if err := fillConfigCutInfo(ctx, res); err != nil {
		return nil, 0, err
	}
	return res, total, nil
}

func getConfig(ctx context.Context, id uint64) (*model.Config, error) {
	c, err := data.GetConfigByID(ctx, id)
	if err != nil {
		if data.IsNotFound(err) {
			return nil, errcode.ErrConfigNotFound
		}
		return nil, err
	}
	return c, nil
}

// ImportConfigFields 解析 Starlark 规则里的字段占位符，按字段默认值组装嵌套 JSON。
//
// 占位符格式：##字段ID**解析路径##，例如 ##12**risk_engine.account_age##。
// 解析路径用 '.' 分层，组装时保持原样，叶子节点取字段 default_value 中的 value。
func ImportConfigFields(ctx context.Context, req *model.ImportConfigFieldsRequest) (*model.ImportConfigFieldsResponse, error) {
	matches := fieldPlaceholderRE.FindAllStringSubmatch(req.Rule, -1)
	if len(matches) == 0 {
		return &model.ImportConfigFieldsResponse{ImportedJSON: "{}"}, nil
	}

	idSet := make(map[uint64]struct{}, len(matches))
	for _, m := range matches {
		if id, err := strconv.ParseUint(m[1], 10, 64); err == nil {
			idSet[id] = struct{}{}
		}
	}
	if len(idSet) == 0 {
		return &model.ImportConfigFieldsResponse{ImportedJSON: "{}"}, nil
	}

	ids := make([]int64, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, int64(id))
	}

	fields, err := data.GetFieldsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}

	fieldMap := make(map[uint64]*model.Field, len(fields))
	for _, f := range fields {
		if f.ProjectID != req.ProjectID {
			continue
		}
		fieldMap[f.ID] = f
	}

	obj := make(map[string]interface{})
	for _, m := range matches {
		id, _ := strconv.ParseUint(m[1], 10, 64)
		path := strings.TrimSpace(m[2])
		f := fieldMap[id]
		if f == nil || path == "" {
			continue
		}

		val := fieldDefaultValue(f.DefaultValue)
		parts := strings.Split(path, ".")
		cur := obj
		for i := 0; i < len(parts)-1; i++ {
			part := parts[i]
			if part == "" {
				continue
			}
			next, ok := cur[part].(map[string]interface{})
			if !ok || next == nil {
				next = make(map[string]interface{})
				cur[part] = next
			}
			cur = next
		}
		cur[parts[len(parts)-1]] = val
	}

	b, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return nil, errcode.ErrInternal.WithDetails("序列化导入 JSON 失败: %v", err)
	}
	return &model.ImportConfigFieldsResponse{ImportedJSON: string(b)}, nil
}

// fieldDefaultValue 从字段默认值配置 JSON 中提取 value 字段；解析失败时返回空串。
func fieldDefaultValue(dv string) interface{} {
	var wrapper struct {
		Value interface{} `json:"value"`
	}
	if err := json.Unmarshal([]byte(dv), &wrapper); err == nil {
		return wrapper.Value
	}
	return ""
}
