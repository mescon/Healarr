package api

import (
	"archive/zip"
	"bufio"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/mescon/Healarr/internal/config"
	"github.com/mescon/Healarr/internal/logger"
)

func (s *RESTServer) handleDownloadLogs(c *gin.Context) {
	c.Header("Content-Disposition", "attachment; filename=healarr_logs.zip")
	c.Header("Content-Type", "application/zip")

	zipWriter := zip.NewWriter(c.Writer)
	defer zipWriter.Close()

	cfg := config.Get()
	logDir := cfg.LogDir
	err := filepath.Walk(logDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		// Create a new file in the zip archive
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		// Use .txt extension for Windows compatibility
		baseName := filepath.Base(path)
		if strings.HasSuffix(baseName, ".log") {
			baseName = strings.TrimSuffix(baseName, ".log") + ".txt"
		}
		header.Name = baseName
		header.Method = zip.Deflate

		writer, err := zipWriter.CreateHeader(header)
		if err != nil {
			return err
		}

		// Open the log file
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()

		// Copy the file content to the zip writer
		_, err = io.Copy(writer, file)
		return err
	})

	if err != nil {
		logger.Errorf("Failed to zip logs: %v", err)
		return
	}
}

func (s *RESTServer) handleRecentLogs(c *gin.Context) {
	// Read log entries with pagination support
	// Query params:
	//   limit: number of entries to return (default 100, max 500)
	//   offset: number of entries to skip from the end (for pagination)
	cfg := config.Get()
	logFile := filepath.Join(cfg.LogDir, "healarr.log")

	file, err := os.Open(logFile)
	if err != nil {
		// If log file doesn't exist yet, return empty response
		if os.IsNotExist(err) {
			c.JSON(http.StatusOK, gin.H{
				"entries":     []map[string]interface{}{},
				"total_lines": 0,
				"has_more":    false,
				"offset":      0,
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read log file"})
		return
	}
	defer file.Close()

	// Parse query parameters using the parseInt helper from pagination.go
	limit := parseInt(c.DefaultQuery("limit", "100"), 100)
	if limit < 1 || limit > 500 {
		limit = 100
	}
	offset := parseInt(c.DefaultQuery("offset", "0"), 0)
	if offset < 0 {
		offset = 0
	}

	// Read all lines into memory
	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	if err := scanner.Err(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to scan log file"})
		return
	}

	totalLines := len(lines)

	// Calculate the range to return (from the end, with offset)
	// offset=0 means get the latest entries
	// offset=100 means skip the latest 100 and get older ones
	end := totalLines - offset
	if end < 0 {
		end = 0
	}
	start := end - limit
	if start < 0 {
		start = 0
	}

	// Check if there are more (older) logs available
	hasMore := start > 0

	// Get the slice of lines
	selectedLines := lines[start:end]

	// Parse each line as a log entry
	var logEntries []map[string]interface{}
	for _, line := range selectedLines {
		// Skip empty lines
		if strings.TrimSpace(line) == "" {
			continue
		}

		// Format: timestamp [LEVEL] message
		// Example: 2025-11-24T19:00:00Z [INFO] Server started
		parts := strings.SplitN(line, " ", 3)
		if len(parts) >= 3 {
			timestamp := parts[0]
			level := strings.Trim(parts[1], "[]")
			message := parts[2]

			logEntries = append(logEntries, map[string]interface{}{
				"timestamp": timestamp,
				"level":     level,
				"message":   message,
			})
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"entries":     logEntries,
		"total_lines": totalLines,
		"has_more":    hasMore,
		"offset":      offset,
	})
}
