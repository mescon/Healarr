package services

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/mescon/Healarr/internal/logger"
	"github.com/mescon/Healarr/internal/repository"
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
	db        *sql.DB
	schedules *repository.ScheduleRepository
	scanPaths *repository.ScanPathRepository
	scanner   *ScannerService
	cron      *cron.Cron
	jobs      map[int]cron.EntryID
	mu        sync.Mutex
}

// NewSchedulerService creates a new SchedulerService with the given database and scanner.
func NewSchedulerService(db *sql.DB, scanner *ScannerService) *SchedulerService {
	return &SchedulerService{
		db:        db,
		schedules: repository.NewScheduleRepository(db),
		scanPaths: repository.NewScanPathRepository(db),
		scanner:   scanner,
		cron:      cron.New(),
		jobs:      make(map[int]cron.EntryID),
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

	enabled, err := s.schedules.ListEnabled(ctx)
	if err != nil {
		return fmt.Errorf("failed to query schedules: %w", err)
	}

	logger.Debugf("Scheduler: iterating over schedules...")
	count := 0
	skipped := 0
	for _, sched := range enabled {
		logger.Debugf("Scheduler: processing schedule %d (path_id=%d, cron=%s)", sched.ID, sched.ScanPathID, sched.CronExpression)

		// Pre-validate cron expression before attempting to add job
		if _, parseErr := cron.ParseStandard(sched.CronExpression); parseErr != nil {
			logger.Errorf("Schedule %d has invalid cron expression '%s': %v - skipping", sched.ID, sched.CronExpression, parseErr)
			skipped++
			continue
		}

		if err := s.addJob(int(sched.ID), int(sched.ScanPathID), sched.CronExpression); err != nil {
			logger.Errorf("Failed to add job for schedule %d: %v", sched.ID, err)
			skipped++
		} else {
			count++
		}
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
	scanPath, err := s.scanPaths.GetByID(ctx, int64(scanPathID))
	if errors.Is(err, repository.ErrNotFound) {
		return fmt.Errorf("scan path %d not found (may have been deleted)", scanPathID)
	}
	if err != nil {
		return fmt.Errorf("failed to query scan path %d: %w", scanPathID, err)
	}
	localPath := scanPath.LocalPath

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

	id, err := s.schedules.Create(context.Background(), int64(scanPathID), cronExpr, true)
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
	if err := s.schedules.Delete(context.Background(), int64(id)); err != nil {
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

	affected, err := s.schedules.DeleteOrphaned(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup orphaned schedules: %w", err)
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
	if err := s.schedules.Update(context.Background(), int64(id), cronExpr, enabled); err != nil {
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
		sched, err := s.schedules.GetByID(context.Background(), int64(id))
		if err != nil {
			return fmt.Errorf("failed to fetch updated schedule: %v", err)
		}

		if err := s.addJob(id, int(sched.ScanPathID), sched.CronExpression); err != nil {
			logger.Errorf("Failed to reschedule job %d: %v", id, err)
		}
	}

	return nil
}
