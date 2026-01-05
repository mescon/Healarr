package api

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestParsePagination_Defaults(t *testing.T) {
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/test", nil)

	cfg := DefaultPaginationConfig()
	p := ParsePagination(c, cfg)

	if p.Page != 1 {
		t.Errorf("Expected page=1, got %d", p.Page)
	}
	if p.Limit != 50 {
		t.Errorf("Expected limit=50, got %d", p.Limit)
	}
	if p.Offset != 0 {
		t.Errorf("Expected offset=0, got %d", p.Offset)
	}
	if p.SortOrder != "desc" {
		t.Errorf("Expected sort_order=desc, got %s", p.SortOrder)
	}
}

func TestParsePagination_CustomValues(t *testing.T) {
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/test?page=3&limit=25&sort_by=name&sort_order=asc", nil)

	cfg := PaginationConfig{
		DefaultLimit:     50,
		MaxLimit:         100,
		DefaultSortBy:    "id",
		DefaultSortOrder: "desc",
		AllowedSortBy:    map[string]bool{"id": true, "name": true, "created_at": true},
	}
	p := ParsePagination(c, cfg)

	if p.Page != 3 {
		t.Errorf("Expected page=3, got %d", p.Page)
	}
	if p.Limit != 25 {
		t.Errorf("Expected limit=25, got %d", p.Limit)
	}
	if p.Offset != 50 { // (3-1) * 25
		t.Errorf("Expected offset=50, got %d", p.Offset)
	}
	if p.SortBy != "name" {
		t.Errorf("Expected sort_by=name, got %s", p.SortBy)
	}
	if p.SortOrder != "asc" {
		t.Errorf("Expected sort_order=asc, got %s", p.SortOrder)
	}
}

func TestParsePagination_InvalidPage(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name     string
		query    string
		expected int
	}{
		{"negative page", "page=-1", 1},
		{"zero page", "page=0", 1},
		{"invalid page", "page=abc", 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("GET", "/test?"+tt.query, nil)

			cfg := DefaultPaginationConfig()
			p := ParsePagination(c, cfg)

			if p.Page != tt.expected {
				t.Errorf("Expected page=%d, got %d", tt.expected, p.Page)
			}
		})
	}
}

func TestParsePagination_LimitBounds(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name     string
		query    string
		maxLimit int
		expected int
	}{
		{"exceeds max", "limit=1000", 500, 50},
		{"zero limit", "limit=0", 500, 50},
		{"negative limit", "limit=-5", 500, 50},
		{"at max", "limit=500", 500, 500},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("GET", "/test?"+tt.query, nil)

			cfg := PaginationConfig{
				DefaultLimit:     50,
				MaxLimit:         tt.maxLimit,
				DefaultSortBy:    "id",
				DefaultSortOrder: "desc",
			}
			p := ParsePagination(c, cfg)

			if p.Limit != tt.expected {
				t.Errorf("Expected limit=%d, got %d", tt.expected, p.Limit)
			}
		})
	}
}

func TestParsePagination_InvalidSortBy(t *testing.T) {
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/test?sort_by=invalid_column", nil)

	cfg := PaginationConfig{
		DefaultLimit:     50,
		MaxLimit:         500,
		DefaultSortBy:    "id",
		DefaultSortOrder: "desc",
		AllowedSortBy:    map[string]bool{"id": true, "name": true},
	}
	p := ParsePagination(c, cfg)

	if p.SortBy != "id" {
		t.Errorf("Expected sort_by to fallback to 'id', got %s", p.SortBy)
	}
}

func TestParsePagination_InvalidSortOrder(t *testing.T) {
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/test?sort_order=invalid", nil)

	cfg := DefaultPaginationConfig()
	p := ParsePagination(c, cfg)

	if p.SortOrder != "desc" {
		t.Errorf("Expected sort_order to fallback to 'desc', got %s", p.SortOrder)
	}
}

func TestNewPaginationResponse(t *testing.T) {
	tests := []struct {
		name      string
		page      int
		limit     int
		total     int
		wantPages int
	}{
		{"exact pages", 1, 10, 30, 3},
		{"partial page", 1, 10, 25, 3},
		{"single page", 1, 50, 10, 1},
		{"empty", 1, 50, 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := PaginationParams{Page: tt.page, Limit: tt.limit}
			resp := NewPaginationResponse(p, tt.total)

			if resp.TotalPages != tt.wantPages {
				t.Errorf("Expected total_pages=%d, got %d", tt.wantPages, resp.TotalPages)
			}
			if resp.Total != tt.total {
				t.Errorf("Expected total=%d, got %d", tt.total, resp.Total)
			}
		})
	}
}

func TestItoa(t *testing.T) {
	tests := []struct {
		input    int
		expected string
	}{
		{0, "0"},
		{1, "1"},
		{10, "10"},
		{123, "123"},
		{1000, "1000"},
	}

	for _, tt := range tests {
		result := itoa(tt.input)
		if result != tt.expected {
			t.Errorf("itoa(%d) = %s, want %s", tt.input, result, tt.expected)
		}
	}
}

func TestParseInt(t *testing.T) {
	tests := []struct {
		input      string
		defaultVal int
		expected   int
	}{
		{"0", 99, 0},     // Special case: "0" returns 0
		{"123", 99, 123}, // Valid number
		{"abc", 99, 99},  // Invalid chars, return default
		{"12a3", 99, 99}, // Invalid char in middle, return default
		{"", 99, 0},      // Empty string (no iteration), result stays 0
		{"10", 99, 10},   // Valid number
	}

	for _, tt := range tests {
		result := parseInt(tt.input, tt.defaultVal)
		if result != tt.expected {
			t.Errorf("parseInt(%q, %d) = %d, want %d", tt.input, tt.defaultVal, result, tt.expected)
		}
	}
}
