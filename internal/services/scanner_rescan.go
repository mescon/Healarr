package services

import (
	"context"
	"os"
	"time"

	"github.com/google/uuid"

	"github.com/mescon/Healarr/internal/domain"
	"github.com/mescon/Healarr/internal/integration"
	"github.com/mescon/Healarr/internal/logger"
	"github.com/mescon/Healarr/internal/safego"
)

// queueForRescan adds a file to the pending_rescans table for later retry
// when infrastructure issues are resolved
func (s *ScannerService) queueForRescan(filePath string, pathID int64, errorType, errorMessage string) {
	// Exponential backoff: 5 minutes initially, doubling each retry, capped at 160 minutes (5 * 2^5)
	ctx, cancel := context.WithTimeout(context.Background(), scannerQueryTimeout)
	defer cancel()

	if err := s.rescanRepo().Queue(ctx, filePath, pathID, errorType, errorMessage); err != nil {
		logger.Errorf("Failed to queue file for rescan: %s: %v", filePath, err)
	} else {
		logger.Debugf("Queued for rescan: %s", filePath)
	}
}

// StartRescanWorker starts a background worker that periodically processes pending rescans
func (s *ScannerService) StartRescanWorker() {
	s.wg.Add(1)
	safego.Run("scanner-rescan-worker", func() {
		defer s.wg.Done()
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-s.shutdownCh:
				logger.Infof("Rescan worker shutting down")
				return
			case <-ticker.C:
				s.processPendingRescans()
			}
		}
	})
	logger.Infof("Rescan worker started (checks every 5 minutes)")
}

// =============================================================================
// processPendingRescans helpers - extracted to reduce cognitive complexity
// =============================================================================

// pendingRescanFile represents a file pending rescan
type pendingRescanFile struct {
	ID         int64
	FilePath   string
	PathID     int64
	RetryCount int
	MaxRetries int
}

// loadPendingRescanFiles loads files that are ready for retry
func (s *ScannerService) loadPendingRescanFiles() ([]pendingRescanFile, error) {
	ctx, cancel := context.WithTimeout(context.Background(), scannerQueryTimeout)
	defer cancel()

	rows, err := s.rescanRepo().ListReady(ctx, 50)
	if err != nil {
		return nil, err
	}

	files := make([]pendingRescanFile, 0, len(rows))
	for _, row := range rows {
		f := pendingRescanFile{
			ID:         row.ID,
			FilePath:   row.FilePath,
			RetryCount: row.RetryCount,
			MaxRetries: row.MaxRetries,
		}
		if row.PathID.Valid {
			f.PathID = row.PathID.Int64
		}
		files = append(files, f)
	}

	return files, nil
}

// markRescanResolved marks a pending rescan as resolved with the given resolution
func (s *ScannerService) markRescanResolved(id int64, resolution string) {
	ctx, cancel := context.WithTimeout(context.Background(), scannerQueryTimeout)
	defer cancel()

	// For 'abandoned' resolution, set status to 'abandoned'; otherwise 'resolved'
	status := "resolved"
	if resolution == "abandoned" {
		status = "abandoned"
	}

	if err := s.rescanRepo().MarkResolved(ctx, id, status, resolution); err != nil {
		logger.Warnf("Failed to mark pending rescan %d as %s: %v", id, resolution, err)
	}
}

// updateRescanRetry updates the retry state for a pending rescan
func (s *ScannerService) updateRescanRetry(f pendingRescanFile, healthErr *integration.HealthCheckError) {
	ctx, cancel := context.WithTimeout(context.Background(), scannerQueryTimeout)
	defer cancel()

	if err := s.rescanRepo().BumpRetry(ctx, f.ID, healthErr.Type, healthErr.Message); err != nil {
		logger.Warnf("Failed to update pending rescan %d retry state: %v", f.ID, err)
	}

	// Check if we've exceeded max retries
	if f.RetryCount+1 >= f.MaxRetries {
		s.markRescanResolved(f.ID, "abandoned")
		logger.Infof("Pending rescan abandoned after %d retries: %s", f.MaxRetries, f.FilePath)
	} else {
		logger.Debugf("Pending rescan still inaccessible, will retry: %s", f.FilePath)
	}
}

// emitRescanCorruption emits a corruption event for a rescan that found actual corruption
func (s *ScannerService) emitRescanCorruption(f pendingRescanFile, healthErr *integration.HealthCheckError) {
	autoRemediate, dryRun, _, _ := s.getScanPathConfig(f.FilePath)

	var fileSize int64
	if info, err := os.Stat(f.FilePath); err == nil {
		fileSize = info.Size()
	}

	// Critical entry point for remediation journey, use retry
	if err := s.eventBus.PublishWithRetry(domain.Event{
		AggregateType: "corruption",
		AggregateID:   uuid.New().String(),
		EventType:     domain.CorruptionDetected,
		EventData: map[string]interface{}{
			"file_path":       f.FilePath,
			"file_size":       fileSize,
			"path_id":         f.PathID,
			"corruption_type": healthErr.Type,
			"error_details":   healthErr.Message,
			"source":          "rescan_worker",
			"auto_remediate":  autoRemediate,
			"dry_run":         dryRun,
		},
	}); err != nil {
		logger.Errorf("Failed to publish corruption event for rescan after retries: %v", err)
	}
}

// processPendingRescans checks files that previously had infrastructure errors
func (s *ScannerService) processPendingRescans() {
	files, err := s.loadPendingRescanFiles()
	if err != nil {
		logger.Errorf("Failed to query pending rescans: %v", err)
		return
	}

	if len(files) == 0 {
		return
	}

	logger.Infof("Processing %d pending rescans", len(files))

	for _, f := range files {
		// Check for shutdown
		select {
		case <-s.shutdownCh:
			return
		default:
		}

		healthy, healthErr := s.detector.Check(f.FilePath, "quick")

		if healthy {
			s.markRescanResolved(f.ID, "healthy")
			logger.Infof("Pending rescan resolved as healthy: %s", f.FilePath)
			continue
		}

		if healthErr.IsRecoverable() {
			s.updateRescanRetry(f, healthErr)
			continue
		}

		// File is accessible but actually corrupt
		logger.Infof("Pending rescan revealed corruption: %s (Type: %s)", f.FilePath, healthErr.Type)
		s.markRescanResolved(f.ID, "corrupt")
		s.emitRescanCorruption(f, healthErr)
	}
}

// GetPendingRescanStats returns statistics about pending rescans
func (s *ScannerService) GetPendingRescanStats() (pending, abandoned, resolved int, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), scannerQueryTimeout)
	defer cancel()

	stats, err := s.rescanRepo().Stats(ctx)
	if err != nil {
		return 0, 0, 0, err
	}
	return stats.Pending, stats.Abandoned, stats.Resolved, nil
}
