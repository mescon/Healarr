package api

import (
	"strings"

	"github.com/gin-gonic/gin"
)

// PaginationParams holds parsed pagination parameters
type PaginationParams struct {
	Page      int
	Limit     int
	Offset    int
	SortBy    string
	SortOrder string
}

// PaginationResponse is the JSON response structure for paginated endpoints
type PaginationResponse struct {
	Page       int `json:"page"`
	Limit      int `json:"limit"`
	Total      int `json:"total"`
	TotalPages int `json:"total_pages"`
}

// PaginationConfig configures pagination parsing behavior
type PaginationConfig struct {
	DefaultLimit     int
	MaxLimit         int
	DefaultSortBy    string
	DefaultSortOrder string
	AllowedSortBy    map[string]bool
}

// DefaultPaginationConfig returns a standard config for most endpoints
func DefaultPaginationConfig() PaginationConfig {
	return PaginationConfig{
		DefaultLimit:     50,
		MaxLimit:         500,
		DefaultSortBy:    "id",
		DefaultSortOrder: "desc",
		AllowedSortBy:    nil, // No restriction
	}
}

// ParsePagination extracts and validates pagination parameters from a Gin context
func ParsePagination(c *gin.Context, cfg PaginationConfig) PaginationParams {
	p := PaginationParams{}

	// Parse page
	p.Page = parseInt(c.DefaultQuery("page", "1"), 1)
	if p.Page < 1 {
		p.Page = 1
	}

	// Parse limit
	p.Limit = parseInt(c.DefaultQuery("limit", itoa(cfg.DefaultLimit)), cfg.DefaultLimit)
	if p.Limit < 1 {
		p.Limit = cfg.DefaultLimit
	}
	if p.Limit > cfg.MaxLimit {
		p.Limit = cfg.DefaultLimit
	}

	// Calculate offset
	p.Offset = (p.Page - 1) * p.Limit

	// Parse sort parameters
	p.SortBy = c.DefaultQuery("sort_by", cfg.DefaultSortBy)
	p.SortOrder = strings.ToLower(c.DefaultQuery("sort_order", cfg.DefaultSortOrder))

	// Validate sort column if allowed list is specified
	if cfg.AllowedSortBy != nil && !cfg.AllowedSortBy[p.SortBy] {
		p.SortBy = cfg.DefaultSortBy
	}

	// Validate sort order
	if p.SortOrder != "asc" && p.SortOrder != "desc" {
		p.SortOrder = cfg.DefaultSortOrder
	}

	return p
}

// NewPaginationResponse creates a pagination response from params and total count
func NewPaginationResponse(p PaginationParams, total int) PaginationResponse {
	totalPages := 0
	if p.Limit > 0 {
		totalPages = (total + p.Limit - 1) / p.Limit
	}

	return PaginationResponse{
		Page:       p.Page,
		Limit:      p.Limit,
		Total:      total,
		TotalPages: totalPages,
	}
}

// parseInt safely parses a string to int with a default value
func parseInt(s string, defaultVal int) int {
	var result int
	for _, c := range s {
		if c < '0' || c > '9' {
			return defaultVal
		}
		result = result*10 + int(c-'0')
	}
	if result == 0 && s != "0" && s != "" {
		return defaultVal
	}
	return result
}

// itoa converts int to string (simple implementation to avoid strconv import)
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
