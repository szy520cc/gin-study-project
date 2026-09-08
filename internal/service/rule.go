package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"myproject/internal/data"
	"myproject/internal/engine"
	"myproject/internal/model"
	"myproject/pkg/errcode"
	"myproject/pkg/transaction"
)

// CreateRuleConfig 一步创建「type=rule 配置 + 规则内容」（规则页「新增规则」入口）。
//
// 一个动作完成：建 config（type 固定 rule、version 自动生成时间戳、status=0、
// is_latest=1）并保存规则内容。logo 必须是全新标识——同一标识的后续版本只能
// 通过编辑已有版本 fork 产生（不可变发布链），否则报错引导去编辑入口。
func CreateRuleConfig(ctx context.Context, username string, req *model.CreateRuleConfigRequest) (*model.RuleResponse, error) {
	if err := engine.ValidateRule(req.Rule); err != nil {
		return nil, errcode.ErrRuleInvalid.WithDetails("%s", err.Error())
	}
	ruleScript, bindVar := engine.CompileRule(req.Rule)
	fields, err := data.GetFieldsByIDs(ctx, bindVar)
	if err != nil {
		return nil, err
	}
	ccBytes, err := json.Marshal(&model.RuleConditionConfig{BindVar: bindVar, Rule: req.Rule})
	if err != nil {
		return nil, err
	}
	conditionConfig := string(ccBytes)

	now := time.Now().Unix()
	pack := packOf(ctx, req.ProjectID)

	// logo 全局查重：同一标识只能 create 一次，后续版本靠 fork
	logo := strings.TrimSpace(req.Logo)
	if n, cerr := data.CountConfigsByLogo(ctx, logo); cerr != nil {
		return nil, cerr
	} else if n > 0 {
		return nil, errcode.ErrConfigLogoVersionExists.WithDetails("配置标识 %s 已存在，请到编辑入口基于已有版本修改", logo)
	}

	c := &model.Config{
		ProjectID:    strings.TrimSpace(req.ProjectID),
		ConfigPackID: req.ConfigPackID,
		Name:         strings.TrimSpace(req.Name),
		Logo:         logo,
		Type:         model.ConfigTypeRule,
		Version:      newVersion(),
		Status:       model.ConfigStatusPending,
		IsLatest:     model.ConfigLatestYes,
		Remark:       strings.TrimSpace(req.Remark),
		CreatedUser:  username,
		UpdatedUser:  username,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	var resp *model.RuleResponse
	err = transaction.Do(ctx, func(ctx context.Context) error {
		if cerr := data.CreateConfig(ctx, c); cerr != nil {
			if data.IsDuplicate(cerr) {
				return errcode.ErrConfigLogoVersionExists.WithDetails("配置标识 %s 已存在", c.Logo)
			}
			return cerr
		}
		r := &model.Rule{
			ConfigID:        c.ID,
			Rule:            ruleScript,
			ConditionConfig: conditionConfig,
			BindVar:         engine.JoinInt64Slice(bindVar),
			ResultType:      req.ResultType,
			Engine:          model.EngineStarlark,
			Pack:            pack,
			Extension:       c.Logo,
			Version:         c.Version,
			CreatedAt:       now,
			UpdatedAt:       now,
		}
		if rerr := data.CreateRule(ctx, r); rerr != nil {
			return rerr
		}
		resp = buildRuleResponse(c, ruleScript, conditionConfig, bindVar, fields, req.ResultType)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// SaveRule 保存规则（含编译 + 不可变版本链的 fork 语义）。
//
// 流程：
//  1. 校验规则原文（占位符格式）；
//  2. 编译成 Starlark 成品 + 收集指标 ID；
//  3. 查指标元信息（供前端回显 bind_var_info）；
//  4. 按 config 状态分流：
//     - 草稿（status=0）：原地保存/更新规则；
//     - 生效（status=1）：fork 新 config（新版本号、status=0、is_latest=1），
//       老版本 is_latest→2 继续生效，复制旧规则到新版本后更新内容。
//
// 返回 fork 后（或原地）所在 config 的规则响应。
func SaveRule(ctx context.Context, username string, req *model.SaveRuleRequest) (*model.RuleResponse, error) {
	if err := engine.ValidateRule(req.Rule); err != nil {
		return nil, errcode.ErrRuleInvalid.WithDetails("%s", err.Error())
	}
	ruleScript, bindVar := engine.CompileRule(req.Rule)

	c, err := getConfig(ctx, req.ConfigID)
	if err != nil {
		return nil, err
	}
	// 规则只能挂在 type=rule 的配置下；否则保存了规则但快照组装不会加载它，
	// eval 永远不执行（静默失败）。
	if c.Type != model.ConfigTypeRule {
		return nil, errcode.ErrConfigStatusInvalid.WithDetails("仅规则类型（type=rule）配置可保存规则，当前类型 %s", c.Type)
	}

	fields, err := data.GetFieldsByIDs(ctx, bindVar)
	if err != nil {
		return nil, err
	}

	ccBytes, err := json.Marshal(&model.RuleConditionConfig{BindVar: bindVar, Rule: req.Rule})
	if err != nil {
		return nil, err
	}
	conditionConfig := string(ccBytes)

	now := time.Now().Unix()
	pack := packOf(ctx, c.ProjectID)

	var resp *model.RuleResponse
	err = transaction.Do(ctx, func(ctx context.Context) error {
		target := c
		if c.Status == model.ConfigStatusActive {
			// 编辑生效版本 → fork 新版本（不可变发布链）；
			// 规则内容由下方 upsertRule 全量写入新版本（已保存的自动复制、首次的直接创建），
			// 无需在此预复制旧规则。
			forked, ferr := forkConfig(ctx, c, username, now)
			if ferr != nil {
				return ferr
			}
			target = forked
		}
		if uerr := upsertRule(ctx, target, pack, ruleScript, conditionConfig, bindVar, req.ResultType, now); uerr != nil {
			return uerr
		}
		resp = buildRuleResponse(target, ruleScript, conditionConfig, bindVar, fields, req.ResultType)
		return nil
	})
	if err != nil {
		return nil, err
	}
	// 编辑必删 latest：草稿快照不带版本号，改了草稿必须失效，
	// 否则 offline 请求会读到旧草稿（最长脏 7 天）。
	_ = data.DelLatest(ctx, pack, c.Logo)
	return resp, nil
}

// forkConfig 复制老 config 生成新版本（status=0, is_latest=1, 版本号=时间戳），
// 并把老版本 is_latest 置为否。线上版本不被动过，形成不可变发布链。
func forkConfig(ctx context.Context, old *model.Config, username string, now int64) (*model.Config, error) {
	nc := *old
	nc.ID = 0
	nc.Version = newVersion()
	nc.Status = model.ConfigStatusPending
	nc.IsLatest = model.ConfigLatestYes
	nc.CutNum = 0
	nc.CutAt = 0
	nc.CutVersion = ""
	nc.CutBy = ""
	nc.CreatedUser = username
	nc.UpdatedUser = username
	nc.CreatedAt = now
	nc.UpdatedAt = now
	if err := data.CreateConfig(ctx, &nc); err != nil {
		return nil, err
	}
	if err := data.MarkConfigNotLatest(ctx, old.ID); err != nil {
		return nil, err
	}
	return &nc, nil
}

// newVersion 生成 fork 新版本号：时间戳（秒）+ 3 位随机后缀，
// 避免同一秒内并发 fork 撞上 logo+version 唯一索引。
func newVersion() string {
	return time.Now().Format("20060102150405") + fmt.Sprintf("%03d", rand.Intn(1000))
}

// upsertRule 保存/更新规则到指定 config（业务上 config:rule 是 1:1）。
func upsertRule(ctx context.Context, c *model.Config, pack, ruleScript, conditionConfig string, bindVar []int64, resultType string, now int64) error {
	r := &model.Rule{
		Rule:            ruleScript,
		ConditionConfig: conditionConfig,
		BindVar:         engine.JoinInt64Slice(bindVar),
		ResultType:      resultType,
		Engine:          model.EngineStarlark,
		Pack:            pack,
		Extension:       c.Logo,
		Version:         c.Version,
		UpdatedAt:       now,
	}
	existing, err := data.GetRuleByConfigID(ctx, c.ID)
	if err != nil && !data.IsNotFound(err) {
		return err
	}
	if existing == nil {
		r.ConfigID = c.ID
		r.CreatedAt = now
		return data.CreateRule(ctx, r)
	}
	_, err = data.UpdateRule(ctx, existing.ID, r)
	return err
}

// buildRuleResponse 组装规则响应。
func buildRuleResponse(c *model.Config, ruleScript, conditionConfig string, bindVar []int64, fields []*model.Field, resultType string) *model.RuleResponse {
	fieldResp := make([]*model.FieldResponse, 0, len(fields))
	for _, f := range fields {
		fieldResp = append(fieldResp, f.ToResponse())
	}
	return &model.RuleResponse{
		ConfigID:        c.ID,
		ProjectID:       c.ProjectID,
		Rule:            ruleScript,
		RuleSource:      extractRuleSource(conditionConfig),
		ConditionConfig: conditionConfig,
		BindVar:         engine.JoinInt64Slice(bindVar),
		BindVarInfo:     fieldResp,
		ResultType:      resultType,
		Engine:          model.EngineStarlark,
		Version:         c.Version,
	}
}

// extractRuleSource 从 condition_config（原文 JSON）解析出占位符规则原文。
func extractRuleSource(conditionConfig string) string {
	var cc model.RuleConditionConfig
	if err := json.Unmarshal([]byte(conditionConfig), &cc); err != nil {
		return ""
	}
	return cc.Rule
}

// GetRule 取规则详情（含占位符原文供编辑器回显）。
//
// config 存在但规则尚未保存（首次进入编辑器）时返回空规则响应而非 404，
// 让前端能打开空表单完成首次保存。
func GetRule(ctx context.Context, configID uint64) (*model.RuleResponse, error) {
	c, err := getConfig(ctx, configID)
	if err != nil {
		return nil, err
	}
	r, err := data.GetRuleByConfigID(ctx, configID)
	if err != nil {
		if data.IsNotFound(err) {
			return &model.RuleResponse{
				ConfigID:   configID,
				RuleSource: "",
				ResultType: model.ResultTypePassRejectReview,
				Engine:     model.EngineStarlark,
				Version:    c.Version,
			}, nil
		}
		return nil, err
	}
	fields, _ := data.GetFieldsByIDs(ctx, engine.SplitInt64Slice(r.BindVar))
	fieldResp := make([]*model.FieldResponse, 0, len(fields))
	for _, f := range fields {
		fieldResp = append(fieldResp, f.ToResponse())
	}
	return &model.RuleResponse{
		ConfigID:        configID,
		Rule:            r.Rule,
		RuleSource:      extractRuleSource(r.ConditionConfig),
		ConditionConfig: r.ConditionConfig,
		BindVar:         r.BindVar,
		BindVarInfo:     fieldResp,
		ResultType:      r.ResultType,
		Engine:          r.Engine,
		Version:         c.Version,
	}, nil
}

// normalizeData 把请求的 data 规范化为 map[string]any。
//
// 兼容前端两种传参形态：JSON 对象（amis json 组件/程序直传）或 JSON 对象字符串
// （textarea 输入）。空值视为空对象（规则可能不引用任何指标）。
func normalizeData(v interface{}) (map[string]any, error) {
	if v == nil {
		return map[string]any{}, nil
	}
	switch t := v.(type) {
	case map[string]any:
		return t, nil
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return map[string]any{}, nil
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(s), &m); err != nil {
			return nil, errcode.ErrInvalidParams.WithDetails("data 必须是 JSON 对象或 JSON 对象字符串")
		}
		return m, nil
	default:
		return nil, errcode.ErrInvalidParams.WithDetails("data 必须是 JSON 对象或 JSON 对象字符串")
	}
}

// TestRun 现场验证：编译 + 提取 + 执行，不落库、不碰缓存。
// 返回执行结果 + 每个指标的实际提取值，便于区分「规则写错」与「取值取错」。
func TestRun(ctx context.Context, req *model.TestRunRequest) (*model.TestRunResponse, error) {
	if err := engine.ValidateRule(req.Rule); err != nil {
		return nil, errcode.ErrRuleInvalid.WithDetails("%s", err.Error())
	}
	inputData, err := normalizeData(req.Data)
	if err != nil {
		return nil, err
	}
	ruleScript, bindVar := engine.CompileRule(req.Rule)
	fields, err := data.GetFieldsByIDs(ctx, bindVar)
	if err != nil {
		return nil, err
	}

	env := make(map[string]any, len(fields))
	bindResults := make([]*model.BindVarResult, 0, len(fields))
	for _, f := range fields {
		meta := engine.FieldMeta{
			ID:           f.ID,
			Name:         f.Name,
			Type:         f.Type,
			ParsePath:    f.ParsePath,
			DefaultValue: f.DefaultValue,
		}
		val, hit := engine.ExtractByPath(inputData, f.ParsePath)
		def := engine.ParseDefaultValue(f.DefaultValue)
		if !hit {
			val = def
		}
		env[meta.EnvKey()] = val
		bindResults = append(bindResults, &model.BindVarResult{
			ID:      f.ID,
			Name:    f.Name,
			Path:    f.ParsePath,
			Value:   val,
			Hit:     hit,
			Default: def,
		})
	}

	result, err := engine.Run(ruleScript, env)
	if err != nil {
		return nil, errcode.ErrRuleExecuteFailed.WithDetails("%s", err.Error())
	}
	value, err := engine.ConvertResult(result, req.ResultType)
	if err != nil {
		return nil, errcode.ErrRuleExecuteFailed.WithDetails("%s", err.Error())
	}
	return &model.TestRunResponse{
		Value:       value,
		ResultType:  req.ResultType,
		BindVarInfo: bindResults,
	}, nil
}
