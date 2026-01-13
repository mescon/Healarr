package services

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/mescon/Healarr/internal/logger"
)

// dbQueryTimeout is the maximum time to wait for a database query during scheduler operations.
// This prevents indefinite hangs if the database is locked or unresponsive.
const dbQueryTimeout = 10 * time.Second

// Scheduler defines the interface for schedule management operations.
// This interface enables mocking in tests while allowing the concrete
// SchedulerService to be used in production.
type Scheduler interface {
	Start()
	Stop()
	LoadSchedules() error
	AddSchedule(scanPathID int, cronExpr string) (int64, error)
	DeleteSchedule(id int) error
	UpdateSchedule(id int, cronExpr string, enabled bool) error
	CleanupOrphanedSchedules() (int, error)
}

// SchedulerService manages scheduled scan jobs using cron expressions.
type SchedulerService struct {
	db      *sql.DB
	scanner *ScannerService
	cron    *cron.Cron
	jobs    map[int]cron.EntryID
	mu      sync.Mutex
}

// NewSchedulerService creates a new SchedulerService with the given database and scanner.
func NewSchedulerService(db *sql.DB, scanner *ScannerService) *SchedulerService {
	return &SchedulerService{
		db:      db,
		scanner: scanner,
		cron:    cron.New(),
		jobs:    make(map[int]cron.EntryID),
	}
}

// Start initializes the cron engine and loads schedules from the database.
func (s *SchedulerService) Start() {
	logger.Debugf("Scheduler: initializing cron engine...")
	s.cron.Start()
	logger.Debugf("Scheduler: cron engine started, loading schedules from database...")
	if err := s.LoadSchedules(); err != nil {
		logger.Errorf("Failed to load schedules: %v", err)
	}
}

// Stop stops the cron engine and all scheduled jobs.
func (s *SchedulerService) Stop() {
	s.cron.Stop()
}

// LoadSchedules loads all enabled schedules from the database and registers them with cron.
func (s *SchedulerService) LoadSchedules() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	logger.Debugf("Scheduler: clearing existing jobs...")
	// Clear existing jobs
	for _, entryID := range s.jobs {
		s.cron.Remove(entryID)
	}
	s.jobs = make(map[int]cron.EntryID)

	logger.Debugf("Scheduler: querying scan_schedules table...")

	// Use context with timeout to prevent indefinite hangs
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	rows, err := s.db.QueryContext(ctx, "SELECT id, scan_path_id, cron_expression, enabled FROM scan_schedules WHERE enabled = 1")
	if err != nil {
		return fmt.Errorf("failed to query schedules: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			logger.Debugf("Scheduler: error closing rows: %v", err)
		}
	}()

	logger.Debugf("Scheduler: iterating over schedules...")
	count := 0
	skipped := 0
	for rows.Next() {
		var id, scanPathID int
		var cronExpr string
		var enabled bool
		if err := rows.Scan(&id, &scanPathID, &cronExpr, &enabled); err != nil {
			logger.Errorf("Failed to scan schedule row: %v", err)
			skipped++
			continue
		}

		logger.Debugf("Scheduler: processing schedule %d (path_id=%d, cron=%s)", id, scanPathID, cronExpr)

		// Pre-validate cron expression before attempting to add job
		if _, parseErr := cron.ParseStandard(cronExpr); parseErr != nil {
			logger.Errorf("Schedule %d has invalid cron expression '%s': %v - skipping", id, cronExpr, parseErr)
			skipped++
			continue
		}

		if err := s.addJob(id, scanPathID, cronExpr); err != nil {
			logger.Errorf("Failed to add job for schedule %d: %v", id, err)
			skipped++
		} else {
			count++
		}
	}

	// Check for iteration errors
	if err := rows.Err(); err != nil {
		logger.Errorf("Error iterating schedule rows: %v", err)
	}

	if skipped > 0 {
		logger.Infof("Loaded %d active scan schedules (%d skipped due to errors)", count, skipped)
	} else {
		logger.Infof("Loaded %d active scan schedules", count)
	}
	return nil
}

func (s *SchedulerService) addJob(scheduleID, scanPathID int, cronExpr string) error {
	// Use context with timeout for database query
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	// Verify scan path exists
	var localPath string
	err := s.db.QueryRowContext(ctx, "SELECT local_path FROM scan_paths WHERE id = ?", scanPathID).Scan(&localPath)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("scan path %d not found (may have been deleted)", scanPathID)
		}
		return fmt.Errorf("failed to query scan path %d: %w", scanPathID, err)
	}

	logger.Debugf("Scheduler: adding cron job for schedule %d (path: %s)", scheduleID, localPath)

	entryID, err := s.cron.AddFunc(cronExpr, func() {
		logger.Infof("Executing scheduled scan for path: %s (Schedule ID: %d)", localPath, scheduleID)
		if err := s.scanner.ScanPath(int64(scanPathID), localPath); err != nil {
			logger.Errorf("Scheduled scan failed for path %s: %v", localPath, err)
		}
	})

	if err != nil {
		return fmt.Errorf("failed to register cron job: %w", err)
	}

	s.jobs[scheduleID] = entryID
	logger.Debugf("Scheduler: successfully registered schedule %d with cron entry %d", scheduleID, entryID)
	return nil
}

// AddSchedule creates a new schedule for the given scan path with the specified cron expression.
func (s *SchedulerService) AddSchedule(scanPathID int, cronExpr string) (int64, error) {
	// Validate cron expression
	if _, err := cron.ParseStandard(cronExpr); err != nil {
		return 0, fmt.Errorf("invalid cron expression: %v", err)
	}

	res, err := s.db.Exec("INSERT INTO scan_schedules (scan_path_id, cron_expression, enabled) VALUES (?, ?, 1)", scanPathID, cronExpr)
	if err != nil {
		return 0, err
	}

	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.addJob(int(id), scanPathID, cronExpr); err != nil {
		return id, fmt.Errorf("saved to DB but failed to schedule: %v", err)
	}

	return id, nil
}

// DeleteSchedule removes a schedule by ID from the database and cron engine.
func (s *SchedulerService) DeleteSchedule(id int) error {
	_, err := s.db.Exec("DELETE FROM scan_schedules WHERE id = ?", id)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if entryID, ok := s.jobs[id]; ok {
		s.cron.Remove(entryID)
		delete(s.jobs, id)
	}

	return nil
}

// CleanupOrphanedSchedules removes schedules that reference scan paths that no longer exist.
// This can happen if foreign key constraints weren't properly enforced in older database versions.
// Returns the number of orphaned schedules that were removed.
func (s *SchedulerService) CleanupOrphanedSchedules() (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	// Find and delete schedules where the scan_path no longer exists
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM scan_schedules
		WHERE scan_path_id NOT IN (SELECT id FROM scan_paths)
	`)
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup orphaned schedules: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to get affected rows: %w", err)
	}

	if affected > 0 {
		logger.Infof("Cleaned up %d orphaned schedule(s) referencing deleted scan paths", affected)
	}

	return int(affected), nil
}

// UpdateSchedule updates a schedule's cron expression and enabled state.
func (s *SchedulerService) UpdateSchedule(id int, cronExpr string, enabled bool) error {
	// Validate cron expression if provided
	if cronExpr != "" {
		if _, err := cron.ParseStandard(cronExpr); err != nil {
			return fmt.Errorf("invalid cron expression: %v", err)
		}
	}

	// Update DB
	query := "UPDATE scan_schedules SET enabled = ?"
	args := []interface{}{enabled}
	if cronExpr != "" {
		query += ", cron_expression = ?"
		args = append(args, cronExpr)
	}
	query += " WHERE id = ?"
	args = append(args, id)

	_, err := s.db.Exec(query, args...)
	if err != nil {
		return err
	}

	// Update running jobs
	s.mu.Lock()
	defer s.mu.Unlock()

	// Remove existing job
	if entryID, ok := s.jobs[id]; ok {
		s.cron.Remove(entryID)
		delete(s.jobs, id)
	}

	// If enabled, add new job
	if enabled {
		// We need the scan_path_id and current cron expression (if not updated)
		var scanPathID int
		var currentCron string
		err := s.db.QueryRow("SELECT scan_path_id, cron_expression FROM scan_schedules WHERE id = ?", id).Scan(&scanPathID, &currentCron)
		if err != nil {
			return fmt.Errorf("failed to fetch updated schedule: %v", err)
		}

		if err := s.addJob(id, scanPathID, currentCron); err != nil {
			logger.Errorf("Failed to reschedule job %d: %v", id, err)
		}
	}

	return nil
}
