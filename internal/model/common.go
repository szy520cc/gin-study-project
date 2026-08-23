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

// Normalize 归一化分页参数，返回实际生效的值（用于响应回显）
func (p PageRequest) Normalize() (int, int) {
	page, pageSize := p.Page, p.PageSize
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
