package repository

import (
	"context"
	"database/sql"
	"testing"
)

// Terminal writes must clear file_list: the enumeration snapshot exists only
// so interrupted scans can resume, and keeping it on completed/cancelled rows
// stored the full path list of every scan forever (multi-MB per row on large
// libraries).

func TestScanRepository_Finalize_ClearsFileList(t *testing.T) {
	t.Parallel()
	db := newScanTestDB(t)
	repo := NewScanRepository(db)
	mustExecScan(t, db, `INSERT INTO scans (path, status, file_list, current_file_index) VALUES ('/media', 'scanning', '["a.mkv","b.mkv"]', 1)`)
	var id int64
	if err := db.QueryRow(`SELECT id FROM scans`).Scan(&id); err != nil {
		t.Fatalf("fetch scan id: %v", err)
	}

	if err := repo.Finalize(context.Background(), id, "completed", 2); err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	var status string
	var fileList sql.NullString
	if err := db.QueryRow(`SELECT status, file_list FROM scans WHERE id = ?`, id).Scan(&status, &fileList); err != nil {
		t.Fatalf("read back scan: %v", err)
	}
	if status != "completed" {
		t.Errorf("status = %q, want completed", status)
	}
	if fileList.Valid {
		t.Errorf("file_list survived Finalize (snapshot kept forever): %q", fileList.String)
	}
}

func TestScanRepository_MarkCancelled_ClearsFileList(t *testing.T) {
	t.Parallel()
	db := newScanTestDB(t)
	repo := NewScanRepository(db)
	mustExecScan(t, db, `INSERT INTO scans (path, status, file_list, current_file_index) VALUES ('/media', 'scanning', '["a.mkv","b.mkv"]', 1)`)
	var id int64
	if err := db.QueryRow(`SELECT id FROM scans`).Scan(&id); err != nil {
		t.Fatalf("fetch scan id: %v", err)
	}

	ok, err := repo.MarkCancelled(context.Background(), id, "user cancelled")
	if err != nil || !ok {
		t.Fatalf("MarkCancelled = (%v, %v), want (true, nil)", ok, err)
	}

	var fileList sql.NullString
	if err := db.QueryRow(`SELECT file_list FROM scans WHERE id = ?`, id).Scan(&fileList); err != nil {
		t.Fatalf("read back scan: %v", err)
	}
	if fileList.Valid {
		t.Errorf("file_list survived MarkCancelled (snapshot kept forever): %q", fileList.String)
	}
}
