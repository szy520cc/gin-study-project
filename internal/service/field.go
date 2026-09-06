package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"myproject/internal/data"
	"myproject/internal/model"
	"myproject/pkg/errcode"
)

var (
	intRe    = regexp.MustCompile(`^-?[0-9]+$`)
	floatRe  = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)
)

// CreateField 创建字段。
// default_value 必须与字段类型一致且为合法 JSON；类型与 value 语义不符时拒绝创建。
func CreateField(ctx context.Context, username string, req *model.CreateFieldRequest) (*model.FieldResponse, error) {
	canonical, err := normalizeDefaultValue(req.Type, req.DefaultValue)
	if err != nil {
		return nil, err
	}

	now := time.Now().Unix()
	status := uint8(req.Status)
	if status != 0 && status != model.FieldStatusActive && status != model.FieldStatusRetired {
		return nil, errcode.ErrInvalidParams.WithDetails("status 只能是 1(生效) 或 2(废弃)")
	}
	if status == 0 {
		status = model.FieldStatusActive
	}

	f := &model.Field{
		ProjectID:    strings.TrimSpace(req.ProjectID),
		Name:         strings.TrimSpace(req.Name),
		Type:         req.Type,
		DefaultValue: canonical,
		ParsePath:    strings.TrimSpace(req.ParsePath),
		Status:       status,
		Remark:       strings.TrimSpace(req.Remark),
		CreatedUser:  username,
		UpdatedUser:  username,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := data.CreateField(ctx, f); err != nil {
		if data.IsDuplicate(err) {
			return nil, errcode.ErrFieldParsePathExists.WithDetails("同一项目下解析路径 %s 已存在", f.ParsePath)
		}
		return nil, err
	}
	return f.ToResponse(), nil
}

// GetField 获取字段详情
func GetField(ctx context.Context, id uint64) (*model.FieldResponse, error) {
	f, err := getField(ctx, id)
	if err != nil {
		return nil, err
	}
	return f.ToResponse(), nil
}

// UpdateField 更新字段（整行编辑，default_value 同样做类型一致性与 JSON 校验）
func UpdateField(ctx context.Context, id uint64, username string, req *model.UpdateFieldRequest) error {
	if _, err := getField(ctx, id); err != nil {
		return err
	}

	canonical, err := normalizeDefaultValue(req.Type, req.DefaultValue)
	if err != nil {
		return err
	}

	status := uint8(req.Status)
	if status != model.FieldStatusActive && status != model.FieldStatusRetired {
		return errcode.ErrInvalidParams.WithDetails("status 只能是 1(生效) 或 2(废弃)")
	}

	upd := &model.Field{
		ProjectID:    strings.TrimSpace(req.ProjectID),
		Name:         strings.TrimSpace(req.Name),
		Type:         req.Type,
		DefaultValue: canonical,
		ParsePath:    strings.TrimSpace(req.ParsePath),
		Status:       status,
		Remark:       strings.TrimSpace(req.Remark),
		UpdatedUser:  username,
		UpdatedAt:    time.Now().Unix(),
	}

	if _, err := data.UpdateField(ctx, id, upd); err != nil {
		if data.IsDuplicate(err) {
			return errcode.ErrFieldParsePathExists.WithDetails("同一项目下解析路径 %s 已存在", upd.ParsePath)
		}
		return err
	}
	return nil
}

// DeleteField 删除字段
func DeleteField(ctx context.Context, id uint64) error {
	if _, err := getField(ctx, id); err != nil {
		return err
	}
	return data.DeleteField(ctx, id)
}

// ListFields 分页查询字段
func ListFields(ctx context.Context, req *model.FieldListRequest) ([]*model.FieldResponse, int64, error) {
	page, pageSize := model.NormalizePage(req.Page, req.PageSize)

	// 筛选时 status=0 视为“不过滤”，避免残留/清空参数触发 400
	var st *uint8
	if req.Status != nil && *req.Status != 0 {
		st = req.Status
	}
	list, total, err := data.ListFields(ctx, req.ProjectID, req.Name, req.Type, st, page, pageSize)
	if err != nil {
		return nil, 0, err
	}

	res := make([]*model.FieldResponse, 0, len(list))
	for _, f := range list {
		res = append(res, f.ToResponse())
	}
	return res, total, nil
}

func getField(ctx context.Context, id uint64) (*model.Field, error) {
	f, err := data.GetFieldByID(ctx, id)
	if err != nil {
		if data.IsNotFound(err) {
			return nil, errcode.ErrFieldNotFound
		}
		return nil, err
	}
	return f, nil
}

// normalizeDefaultValue 校验并规范化 default_value。
//
// 规则：
//  1. 必须是合法 JSON 对象且含 type/value；
//  2. default_value.type 必须与字段类型一致，不一致报错；
//  3. value 的 JSON 语义必须落在该类型允许的范围内（见 valueKindMatches）。
func normalizeDefaultValue(typ, dv string) (string, error) {
	if !model.IsValidFieldType(typ) {
		return "", errcode.ErrFieldTypeInvalid
	}

	var parsed struct {
		Type  string          `json:"type"`
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal([]byte(dv), &parsed); err != nil {
		return "", errcode.ErrFieldDefaultValueInvalid.WithDetails("default_value 不是合法的 JSON 对象，应为 {\"type\":\"%s\",\"value\":<默认值>}", typ)
	}

	if parsed.Type != typ {
		return "", errcode.ErrFieldDefaultTypeMismatch.WithDetails("default_value.type=%q 与字段类型 %q 不一致", parsed.Type, typ)
	}

	val := string(bytes.TrimSpace(parsed.Value))
	if val == "" {
		return "", errcode.ErrFieldDefaultValueInvalid.WithDetails("default_value 缺少 value 字段")
	}
	if !valueKindMatches(typ, val) {
		return "", errcode.ErrFieldDefaultValueInvalid.WithDetails("字段类型为「%s」，默认值格式不匹配（请按 %s 类型填写 value）",
			model.FieldTypeLabel(typ), model.FieldTypeLabel(typ))
	}

	canonical := fmt.Sprintf(`{"type":%s,"value":%s}`, strconv.Quote(parsed.Type), val)
	if len(canonical) > 200 {
		return "", errcode.ErrFieldDefaultValueInvalid.WithDetails("default_value 长度 %d 超过上限 200", len(canonical))
	}
	return canonical, nil
}

// valueKindMatches 按字段类型检查默认值 value 的 JSON 语义。
// value 已是 JSON 字面量（不含外层包裹），在此只需看它属于哪一类。
func valueKindMatches(typ, val string) bool {
	valid := json.Valid([]byte(val))
	switch typ {
	case model.FieldTypeInt:
		// 只能是整数（不允许小数 / 数组 / 对象 / 布尔 / 字符串）
		return intRe.MatchString(val)
	case model.FieldTypeFloat:
		return floatRe.MatchString(val)
	case model.FieldTypeString:
		return valid && strings.HasPrefix(val, `"`)
	case model.FieldTypeBool:
		return val == "true" || val == "false"
	case model.FieldTypeArray:
		return valid && strings.HasPrefix(val, `[`)
	case model.FieldTypeObject:
		return valid && strings.HasPrefix(val, `{`)
	}
	return false
}
