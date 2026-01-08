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

// HealthNotifier defines the interface for health-related notifications.
// This allows for easier testing by enabling mock implementations.
type HealthNotifier interface {
	SendSystemHealthDegraded(data map[string]interface{})
}

// formatUptime returns a human-readable uptime string
func formatUptime(uptime time.Duration) string {
	days := int(uptime.Hours()) / 24
	hours := int(uptime.Hours()) % 24
	minutes := int(uptime.Minutes()) % 60

	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}

// arrHealthResult holds the result of checking arr instances
type arrHealthResult struct {
	online int
	total  int
}

// checkArrInstancesHealth checks all enabled arr instances concurrently
func (s *RESTServer) checkArrInstancesHealth(ctx context.Context) arrHealthResult {
	result := arrHealthResult{}

	rows, err := s.db.QueryContext(ctx, "SELECT url, api_key FROM arr_instances WHERE enabled = 1")
	if err != nil {
		return result
	}
	defer rows.Close()

	// Collect all instances first
	type arrInstance struct {
		url    string
		apiKey string
	}
	var instances []arrInstance

	for rows.Next() {
		var url, encryptedKey string
		if rows.Scan(&url, &encryptedKey) != nil {
			continue
		}
		decryptedKey, err := crypto.Decrypt(encryptedKey)
		if err != nil {
			logger.Errorf("Failed to decrypt API key in health check: %v", err)
			continue
		}
		instances = append(instances, arrInstance{url, decryptedKey})
	}

	result.total = len(instances)
	if result.total == 0 {
		return result
	}

	// Check all instances in parallel
	var mu sync.Mutex
	var wg sync.WaitGroup
	client := &http.Client{Timeout: 2 * time.Second}

	for _, inst := range instances {
		wg.Add(1)
		go func(url, apiKey string) {
			defer wg.Done()
			if s.checkSingleArrInstance(ctx, client, url, apiKey) {
				mu.Lock()
				result.online++
				mu.Unlock()
			}
		}(inst.url, inst.apiKey)
	}
	wg.Wait()

	return result
}

// checkSingleArrInstance checks if a single arr instance is online
func (s *RESTServer) checkSingleArrInstance(ctx context.Context, client *http.Client, url, apiKey string) bool {
	testURL := strings.TrimSuffix(url, "/") + "/api/v3/system/status?apikey=" + apiKey
	req, err := http.NewRequestWithContext(ctx, "GET", testURL, nil)
	if err != nil {
		return false
	}

	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			logger.Debugf("Failed to close response body: %v", err)
		}
	}()

	return resp.StatusCode == http.StatusOK
}

// checkDatabaseHealth checks database connectivity and returns status
func (s *RESTServer) checkDatabaseHealth(ctx context.Context) (gin.H, bool) {
	dbHealth := gin.H{"status": "connected"}
	healthy := true

	if err := s.db.PingContext(ctx); err != nil {
		healthy = false
		dbHealth["status"] = "error"
		dbHealth["error"] = err.Error()
	} else {
		dbPath := config.Get().DatabasePath
		if info, err := os.Stat(dbPath); err == nil {
			dbHealth["size_bytes"] = info.Size()
		}
	}

	return dbHealth, healthy
}

// handleHealth returns server health status for container orchestration.
// This endpoint must return quickly (within 5 seconds) for Docker healthchecks.
func (s *RESTServer) handleHealth(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	// Check database health
	dbHealth, dbHealthy := s.checkDatabaseHealth(ctx)

	// Check arr instances health
	arrHealth := s.checkArrInstancesHealth(ctx)

	// Get pending corruptions count
	var pending int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM corruption_status WHERE current_state = 'CorruptionDetected'").Scan(&pending); err != nil {
		logger.Debugf("Failed to query pending corruptions: %v", err)
	}

	// Determine overall status
	status := "healthy"
	if !dbHealthy {
		status = "degraded"
	} else if arrHealth.total > 0 && arrHealth.online < arrHealth.total {
		status = "degraded"
	}

	// Build health response
	health := gin.H{
		"status":              status,
		"version":             config.Version,
		"uptime":              formatUptime(time.Since(s.startTime)),
		"database":            dbHealth,
		"arr_instances":       gin.H{"online": arrHealth.online, "total": arrHealth.total},
		"active_scans":        len(s.scanner.GetActiveScans()),
		"pending_corruptions": pending,
		"websocket_clients":   s.hub.ClientCount(),
	}

	// Include any configuration warnings
	if warnings := config.GetWarnings(); len(warnings) > 0 {
		health["config_warnings"] = warnings
	}

	// Send notification if degraded
	if status == "degraded" && s.healthNotifier != nil {
		s.sendHealthDegradedNotification(health, dbHealth, arrHealth, pending)
	}

	c.JSON(http.StatusOK, health)
}

// sendHealthDegradedNotification sends a notification when health is degraded
func (s *RESTServer) sendHealthDegradedNotification(health, dbHealth gin.H, arrHealth arrHealthResult, pending int) {
	s.healthNotifier.SendSystemHealthDegraded(map[string]interface{}{
		"status":              health["status"],
		"uptime":              health["uptime"],
		"arr_online":          arrHealth.online,
		"arr_total":           arrHealth.total,
		"arr_offline":         arrHealth.total - arrHealth.online,
		"database_status":     dbHealth["status"],
		"pending_corruptions": pending,
	})
}
