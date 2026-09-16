package model

// 分页默认值。上限与 service 层保持一致，binding 先挡一道，
// service 再兜一道（防止绕过 HTTP 层直接调用 service）。
const (
	DefaultPage     = 1
	DefaultPageSize = 10
	MaxPageSize     = 100
	// MaxPage 页码上限。
	//
	// 不设上限时 offset = (page-1)*pageSize 会溢出成负数，
	// gorm 只在 Offset > 0 时渲染 OFFSET，于是 page 极大时会静默返回第一页；
	// 即使不溢出，OFFSET 10^15 也是一条能打穿数据库的深分页查询。
	MaxPage = 10000
)

// PageRequest 通用分页请求
type PageRequest struct {
	Page     int `form:"page" binding:"omitempty,min=1,max=10000"`
	PageSize int `form:"page_size" binding:"omitempty,min=1,max=100"`
}

// IDRequest 只带主键的请求体（POST + JSON body 形态）。
//
// delete / publish 这类「按主键操作一个资源」的接口共用：字段与校验完全一致，
// 每个模块各定义一份 XxxRequest 只会多出 N 份同形同义的结构体。
// 若某个接口将来需要额外参数（如 publish 加 force），再从本类型拆出独立结构体。
type IDRequest struct {
	ID uint64 `json:"id" binding:"required"`
}

// IDQueryRequest 只带主键的查询参数（GET + query 形态），用于 ?id= 取详情/回填。
type IDQueryRequest struct {
	ID uint64 `form:"id" binding:"required"`
}

// Normalize 归一化分页参数，返回实际生效的值（用于响应回显）
func (p PageRequest) Normalize() (int, int) {
	return NormalizePage(p.Page, p.PageSize)
}

// NormalizePage 归一化分页参数。
//
// 这是全项目唯一一份归一化逻辑：controller 用它回显实际生效的分页，
// service 用它兜底（绕过 HTTP 层直接调 service 时 binding 那道校验不生效）。
// 两处各写一份的话，改了上限只改一边就会出现「回显 100 实际查 1000」这种错位。
func NormalizePage(page, pageSize int) (int, int) {
	if page < 1 {
		page = DefaultPage
	}
	if page > MaxPage {
		page = MaxPage
	}
	if pageSize < 1 {
		pageSize = DefaultPageSize
	}
	if pageSize > MaxPageSize {
		pageSize = MaxPageSize
	}
	return page, pageSize
}
