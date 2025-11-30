package services

import (
	"database/sql"
	"fmt"
	"sync"

	"github.com/robfig/cron/v3"
	"github.com/mescon/Healarr/internal/logger"
)

type SchedulerService struct {
	db      *sql.DB
	scanner *ScannerService
	cron    *cron.Cron
	jobs    map[int]cron.EntryID
	mu      sync.Mutex
}

func NewSchedulerService(db *sql.DB, scanner *ScannerService) *SchedulerService {
	return &SchedulerService{
		db:      db,
		scanner: scanner,
		cron:    cron.New(),
		jobs:    make(map[int]cron.EntryID),
	}
}

func (s *SchedulerService) Start() {
	logger.Infof("Starting Scheduler Service...")
	s.cron.Start()
	if err := s.LoadSchedules(); err != nil {
		logger.Errorf("Failed to load schedules: %v", err)
	}
}

func (s *SchedulerService) Stop() {
	s.cron.Stop()
}

func (s *SchedulerService) LoadSchedules() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Clear existing jobs
	for _, entryID := range s.jobs {
		s.cron.Remove(entryID)
	}
	s.jobs = make(map[int]cron.EntryID)

	rows, err := s.db.Query("SELECT id, scan_path_id, cron_expression, enabled FROM scan_schedules WHERE enabled = 1")
	if err != nil {
		return err
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var id, scanPathID int
		var cronExpr string
		var enabled bool
		if err := rows.Scan(&id, &scanPathID, &cronExpr, &enabled); err != nil {
			logger.Errorf("Failed to scan schedule: %v", err)
			continue
		}

		if err := s.addJob(id, scanPathID, cronExpr); err != nil {
			logger.Errorf("Failed to add job for schedule %d: %v", id, err)
		} else {
			count++
		}
	}
	logger.Infof("Loaded %d active scan schedules", count)
	return nil
}

func (s *SchedulerService) addJob(scheduleID, scanPathID int, cronExpr string) error {
	// Verify scan path exists
	var localPath string
	err := s.db.QueryRow("SELECT local_path FROM scan_paths WHERE id = ?", scanPathID).Scan(&localPath)
	if err != nil {
		return fmt.Errorf("scan path %d not found", scanPathID)
	}

	entryID, err := s.cron.AddFunc(cronExpr, func() {
		logger.Infof("Executing scheduled scan for path: %s (Schedule ID: %d)", localPath, scheduleID)
		if err := s.scanner.ScanPath(int64(scanPathID), localPath); err != nil {
			logger.Errorf("Scheduled scan failed for path %s: %v", localPath, err)
		}
	})

	if err != nil {
		return err
	}

	s.jobs[scheduleID] = entryID
	return nil
}

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
