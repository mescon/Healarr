package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mescon/Healarr/internal/config"
	"github.com/mescon/Healarr/internal/crypto"
	"github.com/mescon/Healarr/internal/logger"
)

// handleHealth returns server health status for container orchestration.
// This endpoint must return quickly (within 5 seconds) for Docker healthchecks.
func (s *RESTServer) handleHealth(c *gin.Context) {
	// Create a context with timeout to ensure we don't hang
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	health := gin.H{
		"status":  "healthy",
		"version": config.Version,
	}

	// Calculate uptime
	uptime := time.Since(s.startTime)
	days := int(uptime.Hours()) / 24
	hours := int(uptime.Hours()) % 24
	minutes := int(uptime.Minutes()) % 60
	if days > 0 {
		health["uptime"] = fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
	} else if hours > 0 {
		health["uptime"] = fmt.Sprintf("%dh %dm", hours, minutes)
	} else {
		health["uptime"] = fmt.Sprintf("%dm", minutes)
	}

	// Check database with timeout
	dbHealth := gin.H{"status": "connected"}
	if err := s.db.PingContext(ctx); err != nil {
		health["status"] = "degraded"
		dbHealth["status"] = "error"
		dbHealth["error"] = err.Error()
	} else {
		// Get database file size
		dbPath := config.Get().DatabasePath
		if info, err := os.Stat(dbPath); err == nil {
			dbHealth["size_bytes"] = info.Size()
		}
	}
	health["database"] = dbHealth

	// Get *arr instance status - check all instances concurrently
	var totalArr, onlineArr int
	var mu sync.Mutex
	var wg sync.WaitGroup

	rows, err := s.db.QueryContext(ctx, "SELECT url, api_key FROM arr_instances WHERE enabled = 1")
	if err == nil {
		defer rows.Close()
		client := &http.Client{Timeout: 2 * time.Second}

		// Collect all instances first
		type arrInstance struct {
			url    string
			apiKey string
		}
		var instances []arrInstance
		for rows.Next() {
			var url, encryptedKey string
			if err := rows.Scan(&url, &encryptedKey); err != nil {
				continue
			}
			// Decrypt API key
			decryptedKey, err := crypto.Decrypt(encryptedKey)
			if err != nil {
				logger.Errorf("Failed to decrypt API key in health check: %v", err)
				continue
			}
			instances = append(instances, arrInstance{url, decryptedKey})
		}

		totalArr = len(instances)

		// Check all instances in parallel with context
		for _, inst := range instances {
			wg.Add(1)
			go func(url, apiKey string) {
				defer wg.Done()
				testURL := strings.TrimSuffix(url, "/") + "/api/v3/system/status?apikey=" + apiKey
				req, err := http.NewRequestWithContext(ctx, "GET", testURL, nil)
				if err != nil {
					return
				}
				if resp, err := client.Do(req); err == nil {
					if err := resp.Body.Close(); err != nil {
						logger.Debugf("Failed to close response body: %v", err)
					}
					if resp.StatusCode == 200 {
						mu.Lock()
						onlineArr++
						mu.Unlock()
					}
				}
			}(inst.url, inst.apiKey)
		}
		wg.Wait()
	}
	health["arr_instances"] = gin.H{"online": onlineArr, "total": totalArr}

	// Get active scans count - this should be fast (just a map read)
	activeScans := len(s.scanner.GetActiveScans())
	health["active_scans"] = activeScans

	// Get pending corruptions count with timeout
	var pending int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM corruption_status WHERE current_state = 'CorruptionDetected'").Scan(&pending); err != nil {
		logger.Debugf("Failed to query pending corruptions: %v", err)
	}
	health["pending_corruptions"] = pending

	// Get WebSocket connections count - this should be fast (just a map len)
	health["websocket_clients"] = s.hub.ClientCount()

	// Include any configuration warnings
	if warnings := config.GetWarnings(); len(warnings) > 0 {
		health["config_warnings"] = warnings
	}

	// Determine overall status
	if health["status"] == "healthy" && (totalArr > 0 && onlineArr < totalArr) {
		health["status"] = "degraded"
	}

	// Send notification if degraded
	if health["status"] == "degraded" && s.notifier != nil {
		offlineCount := totalArr - onlineArr
		s.notifier.SendSystemHealthDegraded(map[string]interface{}{
			"status":              health["status"],
			"uptime":              health["uptime"],
			"arr_online":          onlineArr,
			"arr_total":           totalArr,
			"arr_offline":         offlineCount,
			"database_status":     dbHealth["status"],
			"pending_corruptions": pending,
		})
	}

	c.JSON(http.StatusOK, health)
}
