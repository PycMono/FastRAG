package dto

// PageQuery 通用分页查询参数
type PageQuery struct {
	Page     int `form:"page,default=1" json:"page" binding:"gte=1"`
	PageSize int `form:"page_size,default=10" json:"page_size" binding:"gte=1,lte=100"`
}

// Offset 计算数据库偏移量
func (p *PageQuery) Offset() int {
	if p.Page < 1 {
		p.Page = 1
	}
	return (p.Page - 1) * p.Limit()
}

// Limit 计算每页条数
func (p *PageQuery) Limit() int {
	if p.PageSize < 1 {
		p.PageSize = 10
	}
	if p.PageSize > 100 {
		p.PageSize = 100
	}
	return p.PageSize
}
