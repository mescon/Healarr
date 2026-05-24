package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Notification is a row from the notifications table.
//
// ProviderType is a plain string here; callers convert via
// notifier.ParseProviderType at the HTTP/service boundary. Keeping the
// repo notifier-package-free preserves the leaf-package property
// (repository → no internal deps).
//
// Config is stored encrypted at rest. The repo never encrypts/decrypts;
// callers do that at the boundary (matches ArrInstance / scan_paths).
// EventsJSON is the raw JSON-encoded []string the notifier persists.
type Notification struct {
	ID              int64
	Name            string
	ProviderType    string
	EncryptedConfig string
	EventsJSON      string
	Enabled         bool
	ThrottleSeconds int
	CreatedAt       string
	UpdatedAt       string
}

// NotificationFields is the shared input shape for Create and Update.
type NotificationFields struct {
	Name            string
	ProviderType    string
	EncryptedConfig string
	EventsJSON      string
	Enabled         bool
	ThrottleSeconds int
}

// NotificationLogEntry is a row from the notification_log table.
type NotificationLogEntry struct {
	ID             int64
	NotificationID int64
	EventType      string
	Message        string
	Status         string
	Error          sql.NullString
	SentAt         string
}

// NotificationRepository wraps the notifications + notification_log tables.
type NotificationRepository struct {
	db *sql.DB
}

// NewNotificationRepository returns a repository backed by db.
func NewNotificationRepository(db *sql.DB) *NotificationRepository {
	return &NotificationRepository{db: db}
}

const notificationColumns = `id, name, provider_type, config, events, enabled, throttle_seconds, created_at, updated_at`

func scanNotificationRow(scanner interface {
	Scan(dest ...interface{}) error
}, n *Notification) error {
	return scanner.Scan(
		&n.ID, &n.Name, &n.ProviderType, &n.EncryptedConfig, &n.EventsJSON,
		&n.Enabled, &n.ThrottleSeconds, &n.CreatedAt, &n.UpdatedAt,
	)
}

// ListEnabled returns notifications where enabled = 1.
func (r *NotificationRepository) ListEnabled(ctx context.Context) ([]Notification, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+notificationColumns+` FROM notifications WHERE enabled = 1`)
	if err != nil {
		return nil, fmt.Errorf("query enabled notifications: %w", err)
	}
	defer rows.Close()

	var out []Notification
	for rows.Next() {
		var n Notification
		if err := scanNotificationRow(rows, &n); err != nil {
			return nil, fmt.Errorf("scan notification row: %w", err)
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate notifications: %w", err)
	}
	return out, nil
}

// ListAll returns every notification, ordered by name.
func (r *NotificationRepository) ListAll(ctx context.Context) ([]Notification, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+notificationColumns+` FROM notifications ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("query all notifications: %w", err)
	}
	defer rows.Close()

	var out []Notification
	for rows.Next() {
		var n Notification
		if err := scanNotificationRow(rows, &n); err != nil {
			return nil, fmt.Errorf("scan notification row: %w", err)
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate notifications: %w", err)
	}
	return out, nil
}

// GetByID returns the notification matching id, or ErrNotFound.
func (r *NotificationRepository) GetByID(ctx context.Context, id int64) (Notification, error) {
	var n Notification
	err := scanNotificationRow(
		r.db.QueryRowContext(ctx, `SELECT `+notificationColumns+` FROM notifications WHERE id = ?`, id),
		&n,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Notification{}, ErrNotFound
	}
	if err != nil {
		return Notification{}, fmt.Errorf("get notification: %w", err)
	}
	return n, nil
}

// Create inserts a new notification and returns its id.
func (r *NotificationRepository) Create(ctx context.Context, f NotificationFields) (int64, error) {
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO notifications (name, provider_type, config, events, enabled, throttle_seconds)
		VALUES (?, ?, ?, ?, ?, ?)
	`, f.Name, f.ProviderType, f.EncryptedConfig, f.EventsJSON, f.Enabled, f.ThrottleSeconds)
	if err != nil {
		return 0, fmt.Errorf("insert notification: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("last insert id: %w", err)
	}
	return id, nil
}

// Update replaces the editable fields of the row matching id. The
// updated_at column is bumped to now().
func (r *NotificationRepository) Update(ctx context.Context, id int64, f NotificationFields) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE notifications
		SET name = ?, provider_type = ?, config = ?, events = ?, enabled = ?, throttle_seconds = ?,
		    updated_at = datetime('now')
		WHERE id = ?
	`, f.Name, f.ProviderType, f.EncryptedConfig, f.EventsJSON, f.Enabled, f.ThrottleSeconds, id)
	if err != nil {
		return fmt.Errorf("update notification: %w", err)
	}
	return nil
}

// Delete removes the notification matching id. Also cascades to its log
// entries (notification_log has ON DELETE CASCADE in production, but
// not on every test schema, so the repo issues an explicit DELETE on
// the log table too — the second DELETE is best-effort).
func (r *NotificationRepository) Delete(ctx context.Context, id int64) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM notifications WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete notification: %w", err)
	}
	if _, err := r.db.ExecContext(ctx, `DELETE FROM notification_log WHERE notification_id = ?`, id); err != nil {
		// Treated as best-effort: the row is gone, log cleanup is housekeeping.
		// Caller logs but doesn't fail; we return nil to match the existing
		// semantic from notifier.DeleteConfig.
		_ = err
	}
	return nil
}

// AppendLog inserts a notification_log row. Errors are returned for the
// caller to log — the notifier's existing logNotification helper logs
// and continues, since failure to record a log entry shouldn't drop the
// in-flight notification.
func (r *NotificationRepository) AppendLog(ctx context.Context, notificationID int64, eventType, message, status, errMsg string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO notification_log (notification_id, event_type, message, status, error)
		VALUES (?, ?, ?, ?, ?)
	`, notificationID, eventType, message, status, errMsg)
	if err != nil {
		return fmt.Errorf("append notification log: %w", err)
	}
	return nil
}

// SweepLogsOlderThan deletes log rows whose sent_at is older than the
// given number of days. Returns the row count.
func (r *NotificationRepository) SweepLogsOlderThan(ctx context.Context, days int) (int64, error) {
	// SQLite-specific datetime arithmetic; the cutoff string is a literal
	// like '-7 days' that SQLite's datetime() understands.
	cutoff := fmt.Sprintf("-%d days", days)
	res, err := r.db.ExecContext(ctx, `
		DELETE FROM notification_log
		WHERE sent_at < datetime('now', ?)
	`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("sweep old notification logs: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected: %w", err)
	}
	return n, nil
}

// LimitLogTotal keeps only the most recent maxRows log entries (across
// all notifications). Rows outside that window are deleted.
func (r *NotificationRepository) LimitLogTotal(ctx context.Context, maxRows int) error {
	_, err := r.db.ExecContext(ctx, `
		DELETE FROM notification_log
		WHERE id NOT IN (
			SELECT id FROM notification_log
			ORDER BY sent_at DESC
			LIMIT ?
		)
	`, maxRows)
	if err != nil {
		return fmt.Errorf("limit notification logs: %w", err)
	}
	return nil
}

// ListLog returns the most recent log entries. If notificationID > 0,
// results are filtered to that notification; otherwise all entries are
// returned. Capped at limit rows, ordered by sent_at DESC.
func (r *NotificationRepository) ListLog(ctx context.Context, notificationID int64, limit int) ([]NotificationLogEntry, error) {
	query := `SELECT id, notification_id, event_type, message, status, error, sent_at FROM notification_log`
	args := []interface{}{}
	if notificationID > 0 {
		query += ` WHERE notification_id = ?`
		args = append(args, notificationID)
	}
	query += ` ORDER BY sent_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query notification logs: %w", err)
	}
	defer rows.Close()

	entries := make([]NotificationLogEntry, 0)
	for rows.Next() {
		var e NotificationLogEntry
		if err := rows.Scan(&e.ID, &e.NotificationID, &e.EventType, &e.Message, &e.Status, &e.Error, &e.SentAt); err != nil {
			return nil, fmt.Errorf("scan notification log row: %w", err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate notification logs: %w", err)
	}
	return entries, nil
}
