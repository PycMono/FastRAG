package vo

// PageResult 通用分页返回结构
type PageResult[T any] struct {
	Total    int64 `json:"total"`
	List     []T   `json:"list"`
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
	Pages    int   `json:"pages"`
}

// NewPageResult 创建分页结果
func NewPageResult[T any](total int64, list []T, page, pageSize int) *PageResult[T] {
	if pageSize < 1 {
		pageSize = 10 // 与 dto.PageQuery.Limit() 的默认值保持一致，避免除零 panic
	}
	pages := int(total) / pageSize
	if int(total)%pageSize > 0 {
		pages++
	}
	if pages < 1 {
		pages = 1
	}
	return &PageResult[T]{
		Total:    total,
		List:     list,
		Page:     page,
		PageSize: pageSize,
		Pages:    pages,
	}
}
