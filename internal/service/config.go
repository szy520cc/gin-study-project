package service

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"myproject/internal/data"
	"myproject/internal/model"
	"myproject/pkg/errcode"
	"myproject/pkg/logger"
	"myproject/pkg/transaction"
)

var fieldPlaceholderRE = regexp.MustCompile(`##(\d+)\*\*([^#]+)##`)

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
