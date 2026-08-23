package service

import "myproject/internal/model"

// 分页约束：上限收口在 service 层，因为这是业务规则而非传输细节。
// 原实现只校验下界，page_size=1000000 会直接打穿数据库。
// 值取自 model 包，避免两处各写一份常量。
const (
	defaultPage     = model.DefaultPage
	defaultPageSize = model.DefaultPageSize
	maxPageSize     = model.MaxPageSize
	maxPage         = model.MaxPage
)

// normalizePage 归一化分页参数
func normalizePage(page, pageSize int) (int, int) {
	if page < 1 {
		page = defaultPage
	}
	// page 也要设上限：offset 溢出会让极大的 page 静默返回第一页
	if page > maxPage {
		page = maxPage
	}
	if pageSize < 1 {
		pageSize = defaultPageSize
	}
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}
	return page, pageSize
}
