package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"reflect"
	"strconv"
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
	// 保存闸门：必须带试跑入参且真实跑通，否则不落库。
	if err := ensureRuleRunnable(ctx, req.Rule, req.ResultType, req.TestData); err != nil {
		return nil, err
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

// UpdateRuleConfig 保存一条规则配置（基本信息 + 规则内容），是规则保存的唯一入口
// （原 /api/v1/configs/rule/save 已合并进本接口）。
//
// 流程：
//  1. 保存闸门：校验规则原文（占位符格式 + Starlark 编译）并带试跑入参真实执行一遍，
//     任一环节不通过直接拒绝，不落库（见 ensureRuleRunnable）；
//  2. 编译成 Starlark 成品 + 收集指标 ID，并查指标元信息（供前端回显 bind_var_info）；
//  3. 规则只能挂 type=rule 的配置（否则保存了也不会被快照加载，eval 静默不执行）；
//  4. 按 config 状态分流（不可变发布链，线上版本行不被改动）：
//     - 草稿（status=0）：原地更新 name/remark + 规则；
//     - 生效（status=1）：fork 新版本（新版本号、status=0、is_latest=1），老版本
//     is_latest→2 继续生效，本次提交的 name/remark 写到新版本；规则内容由
//     upsertRule 全量写入新版本（已存在则更新、首次则创建），无需预复制旧规则。
//
// 保存后失效缓存：latest 草稿快照 + 该版本快照；若该版本正是灰度目标，
// 还要失效生效版本快照（否则灰度继续跑旧草稿规则）。
func UpdateRuleConfig(ctx context.Context, username string, req *model.UpdateRuleConfigRequest) (*model.RuleResponse, error) {
	// 保存闸门：必须带试跑入参且真实跑通，否则不落库。
	if err := ensureRuleRunnable(ctx, req.Rule, req.ResultType, req.TestData); err != nil {
		return nil, err
	}
	ruleScript, bindVar := engine.CompileRule(req.Rule)

	c, err := getConfig(ctx, req.ConfigID)
	if err != nil {
		return nil, err
	}
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
	name := strings.TrimSpace(req.Name)
	remark := strings.TrimSpace(req.Remark)

	var resp *model.RuleResponse
	err = transaction.Do(ctx, func(ctx context.Context) error {
		target := c
		if c.Status == model.ConfigStatusActive {
			// 编辑生效版本 → fork 新版本（不可变发布链），新版本行在下方写入本次提交的 name/remark
			forked, ferr := forkConfig(ctx, c, username, now)
			if ferr != nil {
				return ferr
			}
			target = forked
		}
		if uerr := data.UpdateConfigBasic(ctx, target.ID, name, remark, username, now); uerr != nil {
			return uerr
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
	// 同时失效该版本的「版本化快照」：原地编辑草稿时版本号不变，
	// 若不删，曾被显式 version 求值写入的旧快照会在发布后被默认路径命中。
	_ = data.DelSnapshot(ctx, pack, c.Logo, resp.Version)
	// 若改的正是灰度目标版本，还要失效生效版本快照（否则灰度继续跑旧草稿规则）。
	invalidateGraySnapshotForDraft(ctx, pack, c.Logo, resp.Version)
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
	// 把老版本（以及可能存在的其它旧版本）全部置为非最新，保证一个 logo 只有一个最新版本。
	if err := data.EnsureOnlyLatest(ctx, old.Logo, nc.ID); err != nil {
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
		ConfigPackID:    c.ConfigPackID,
		Name:            c.Name,
		Logo:            c.Logo,
		Type:            c.Type,
		StatusText:      model.ConfigStatusText(c.Status),
		Remark:          c.Remark,
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

	var resp *model.RuleResponse
	r, err := data.GetRuleByConfigID(ctx, configID)
	if err != nil {
		if !data.IsNotFound(err) {
			return nil, err
		}
		resp = &model.RuleResponse{
			ConfigID:     configID,
			ProjectID:    c.ProjectID,
			ConfigPackID: c.ConfigPackID,
			Name:         c.Name,
			Logo:         c.Logo,
			Type:         c.Type,
			StatusText:   model.ConfigStatusText(c.Status),
			Remark:       c.Remark,
			RuleSource:   "",
			ResultType:   model.ResultTypePassRejectReview,
			Engine:       model.EngineStarlark,
			Version:      c.Version,
		}
	} else {
		fields, _ := data.GetFieldsByIDs(ctx, engine.SplitInt64Slice(r.BindVar))
		fieldResp := make([]*model.FieldResponse, 0, len(fields))
		for _, f := range fields {
			fieldResp = append(fieldResp, f.ToResponse())
		}
		resp = &model.RuleResponse{
			ConfigID:        configID,
			ProjectID:       c.ProjectID,
			ConfigPackID:    c.ConfigPackID,
			Name:            c.Name,
			Logo:            c.Logo,
			Type:            c.Type,
			StatusText:      model.ConfigStatusText(c.Status),
			Remark:          c.Remark,
			Rule:            r.Rule,
			RuleSource:      extractRuleSource(r.ConditionConfig),
			ConditionConfig: r.ConditionConfig,
			BindVar:         r.BindVar,
			BindVarInfo:     fieldResp,
			ResultType:      r.ResultType,
			Engine:          r.Engine,
			Version:         c.Version,
		}
	}

	if err := fillRuleConfigNames(ctx, resp); err != nil {
		return nil, err
	}
	return resp, nil
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
	return executeRule(ctx, req.Rule, req.ResultType, req.Data, req.Pack, strconv.FormatUint(uint64(req.ConfigPackID), 10), req.Key, req.Version)
}

// executeRule 是「试跑」与「保存闸门」共用的执行核心。
//
// 链路：校验原文 → 规范化入参 → 编译成 Starlark 成品 → 按 bind_var 取指标元信息 →
// 从入参里按解析路径取值（取不到用默认值）→ 执行 → 转换结果。
// 全程只读：不落库、不碰缓存，可安全地在保存前重复调用。
//
// pack 优先用请求里直传的配置包标识（logo）；为空时退回用 configPackID 反查，
// 再不行退回空串，不阻断试跑。
func executeRule(ctx context.Context, ruleSource, resultType string, rawData interface{}, pack, configPackID, key, version string) (*model.TestRunResponse, error) {
	if err := engine.ValidateRule(ruleSource); err != nil {
		return nil, errcode.ErrRuleInvalid.WithDetails("%s", err.Error())
	}
	inputData, err := normalizeData(rawData)
	if err != nil {
		return nil, err
	}
	ruleScript, bindVar := engine.CompileRule(ruleSource)
	fields, err := data.GetFieldsByIDs(ctx, bindVar)
	if err != nil {
		return nil, err
	}

	env := make(map[string]any, len(fields))
	bindResults := make([]*model.BindVarResult, 0, len(fields))
	bindInfo := make(map[string]*model.BindVarResult, len(fields))
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
		item := &model.BindVarResult{
			ID:            f.ID,
			Source:        fieldSource(f.ParsePath),
			Type:          f.Type,
			DefaultConfig: f.DefaultValue,
			Name:          f.Name,
			Path:          f.ParsePath,
			Value:         val,
			Hit:           hit,
			Default:       def,
		}
		bindResults = append(bindResults, item)
		bindInfo[meta.EnvKey()] = item
	}

	result, err := engine.Run(ruleScript, env)
	if err != nil {
		return nil, errcode.ErrRuleExecuteFailed.WithDetails("%s", err.Error())
	}
	value, err := engine.ConvertResult(result, resultType)
	if err != nil {
		return nil, errcode.ErrRuleExecuteFailed.WithDetails("%s", err.Error())
	}
	// pack 优先用请求直传的配置包标识；为空才反查兜底。
	resolvedPack := pack
	if resolvedPack == "" && configPackID != "" {
		resolvedPack = packLogoOf(ctx, configPackID)
	}
	return &model.TestRunResponse{
		Pack:        resolvedPack,
		Key:         key,
		Version:     version,
		Value:       value,
		Type:        resultValueType(value),
		ResultType:  resultType,
		BindVarInfo: bindInfo,
	}, nil
}

// packLogoOf 把配置包主键解析成配置包标识（logo）回填到响应的 pack 字段。
// 解析不到（草稿未选配置包 / ID 非法）时退回空串，不阻断试跑。
func packLogoOf(ctx context.Context, configPackID string) string {
	if configPackID == "" {
		return ""
	}
	id, err := strconv.ParseUint(configPackID, 10, 64)
	if err != nil || id == 0 {
		return configPackID
	}
	p, err := data.GetConfigPackByID(ctx, id)
	if err != nil || p.Logo == "" {
		return configPackID
	}
	return p.Logo
}

// isEmptyTestData 判断「试跑入参」是否等于没填。
// 前端 JSON 编辑器提交的是文本，空编辑器会给出 ""；amis 也可能给出 {} 或 "null"，
// 这些都不算「编写了对应的 json」，一律按未填写处理。
func isEmptyTestData(v interface{}) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		s := strings.TrimSpace(t)
		return s == "" || s == "{}" || s == "null"
	case map[string]any:
		return len(t) == 0
	}
	return false
}

// fieldSource 返回解析路径的顶层来源，例如 material.title -> material。
func fieldSource(path string) string {
	if i := strings.IndexByte(path, '.'); i >= 0 {
		return path[:i]
	}
	if i := strings.IndexByte(path, '$'); i >= 0 {
		return path[:i]
	}
	return path
}

// resultValueType 返回试运行结果的 JSON 类型，便于页面快速判断结果是否符合预期。
func resultValueType(v interface{}) string {
	if v == nil {
		return "null"
	}
	if v == "" {
		return "string"
	}
	switch v.(type) {
	case bool:
		return "bool"
	case string:
		return "string"
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return "int"
	case float32, float64:
		return "float"
	}
	kind := reflect.TypeOf(v).Kind()
	if kind == reflect.Slice || kind == reflect.Array {
		return "array"
	}
	if kind == reflect.Map || kind == reflect.Struct {
		return "object"
	}
	return kind.String()
}

// ensureRuleRunnable 保存闸门：规则必须先「带着入参真实跑通」才允许落库。
//
// 这是「验证不是独立功能」的落点——新增/编辑配置时不需要单独点验证按钮，
// 保存接口自己就会跑一遍，跑不通直接拒绝，未验证通过的配置不可能写进数据库。
//
// 两道关卡缺一不可：
//  1. 必须携带试跑入参 JSON —— 只给规则没法验证取值，直接拒绝；
//  2. 用该入参真实执行 —— 占位符格式错、Starlark 编译错、运行时错、
//     结果类型不匹配，都会在这里暴露并带着行号/中文说明返回给运营。
func ensureRuleRunnable(ctx context.Context, ruleSource, resultType string, rawData interface{}) error {
	if isEmptyTestData(rawData) {
		return errcode.ErrRuleInvalid.WithDetails("保存前必须填写「试跑入参（JSON）」：规则要先能真实跑通才允许保存")
	}
	_, err := executeRule(ctx, ruleSource, resultType, rawData, "", "", "", "")
	return err
}
