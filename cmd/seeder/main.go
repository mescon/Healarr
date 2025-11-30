package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
)

func main() {
	db, err := sql.Open("sqlite3", "./healarr.db")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	fmt.Println("Seeding database...")

	// Seed Scans
	scans := []struct {
		Path             string
		Status           string
		FilesScanned     int
		CorruptionsFound int
		StartedAt        time.Time
		CompletedAt      time.Time
	}{
		{"/mnt/media/Movies", "completed", 15420, 3, time.Now().Add(-24 * time.Hour), time.Now().Add(-23 * time.Hour)},
		{"/mnt/media/TV", "completed", 45200, 12, time.Now().Add(-12 * time.Hour), time.Now().Add(-10 * time.Hour)},
		{"/mnt/media/Music", "failed", 1200, 0, time.Now().Add(-5 * time.Hour), time.Now().Add(-5 * time.Hour)},
		{"/mnt/media/Downloads", "running", 500, 1, time.Now().Add(-10 * time.Minute), time.Time{}},
	}

	for _, s := range scans {
		_, err := db.Exec("INSERT INTO scans (path, status, files_scanned, corruptions_found, started_at, completed_at) VALUES (?, ?, ?, ?, ?, ?)",
			s.Path, s.Status, s.FilesScanned, s.CorruptionsFound, s.StartedAt, s.CompletedAt)
		if err != nil {
			log.Printf("Failed to insert scan: %v", err)
		}
	}

	// Seed Events (to populate corruptions view)
	// We need to simulate the event stream
	events := []struct {
		Type string
		Data map[string]interface{}
	}{
		{
			"CorruptionDetected",
			map[string]interface{}{
				"file_path":       "/mnt/media/Movies/Avatar (2009)/Avatar.mkv",
				"corruption_type": "video_stream_error",
				"details":         "Stream 0: data integrity issue",
			},
		},
		{
			"CorruptionDetected",
			map[string]interface{}{
				"file_path":       "/mnt/media/TV/Breaking Bad/S01E01.mkv",
				"corruption_type": "audio_sync_error",
				"details":         "Audio skew > 500ms",
			},
		},
		{
			"CorruptionDetected",
			map[string]interface{}{
				"file_path":       "/mnt/media/Movies/Inception/Inception.mkv",
				"corruption_type": "container_error",
				"details":         "Moov atom missing",
			},
		},
	}

	for _, e := range events {
		id := uuid.New().String()
		data, _ := json.Marshal(e.Data)

		// 1. Detect
		_, err := db.Exec("INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data) VALUES (?, ?, ?, ?)",
			"corruption", id, "CorruptionDetected", string(data))
		if err != nil {
			log.Printf("Failed to insert event: %v", err)
		}

		// 2. Simulate some state changes
		if e.Data["file_path"] == "/mnt/media/Movies/Inception/Inception.mkv" {
			// Mark as resolved
			_, _ = db.Exec("INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data) VALUES (?, ?, ?, ?)",
				"corruption", id, "RemediationStarted", "{}")
			_, _ = db.Exec("INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data) VALUES (?, ?, ?, ?)",
				"corruption", id, "FileDeleted", "{}") // Or whatever resolves it
			_, _ = db.Exec("INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data) VALUES (?, ?, ?, ?)",
				"corruption", id, "SearchCompleted", "{}") // This might mark it resolved in our logic?
			// Actually the view looks for latest event_type.
			// If we want 'resolved', we need an event that maps to that state or logic.
			// The view just takes the latest event_type as current_state.
			// So let's emit a 'CorruptionResolved' event (if it exists) or just leave it as SearchCompleted.
			// Let's assume SearchCompleted implies resolution for now or add a specific event.
		}
	}

	fmt.Println("Seeding complete.")
}
