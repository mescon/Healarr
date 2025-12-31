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
	// Read the last N log entries from the log file
	cfg := config.Get()
	logFile := filepath.Join(cfg.LogDir, "healarr.log")

	file, err := os.Open(logFile)
	if err != nil {
		// If log file doesn't exist yet, return empty array
		if os.IsNotExist(err) {
			c.JSON(http.StatusOK, []map[string]interface{}{})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read log file"})
		return
	}
	defer file.Close()

	// Read all lines into memory (we'll keep last 100)
	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	if err := scanner.Err(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to scan log file"})
		return
	}

	// Get the last 100 lines (or all if less than 100)
	start := 0
	if len(lines) > 100 {
		start = len(lines) - 100
	}
	recentLines := lines[start:]

	// Parse each line as a log entry
	var logEntries []map[string]interface{}
	for _, line := range recentLines {
		// Skip empty lines
		if strings.TrimSpace(line) == "" {
			continue
		}

		// Try to parse as JSON (from logger package)
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

	c.JSON(http.StatusOK, logEntries)
}
