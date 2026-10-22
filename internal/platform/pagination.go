package platform

import "context"

type ExecutionQuery struct {
	Page, PageSize int
	AccountID      string
	Status         string
}

type ExecutionPage struct {
	Items    []Execution `json:"items"`
	Total    int         `json:"total"`
	Page     int         `json:"page"`
	PageSize int         `json:"page_size"`
}

type ExecutionPager interface {
	ListExecutionPage(context.Context, ExecutionQuery) (ExecutionPage, error)
}
