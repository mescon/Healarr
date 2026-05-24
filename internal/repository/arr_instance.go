package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ArrInstance is a row from the arr_instances table.
//
// The api_key and webhook_secret columns hold *encrypted* values; the
// repository never encrypts/decrypts. Callers decrypt on read and encrypt
// on write at the HTTP/service boundary, so the secret-handling policy
// stays out of the persistence layer.
type ArrInstance struct {
	ID                     int64
	Name                   string
	Type                   string // sonarr / radarr / whisparr-v2 / whisparr-v3
	URL                    string
	EncryptedAPIKey        string
	Enabled                bool
	EncryptedWebhookSecret sql.NullString
}

// CreateArrInstanceParams is the input shape for ArrInstanceRepository.Create.
//
// EncryptedWebhookSecret is optional: a zero-length string is stored as
// SQL NULL (legacy instances created before per-instance webhook secrets
// existed). Callers that mint a fresh secret pass it as a non-empty
// encrypted string.
type CreateArrInstanceParams struct {
	Name                   string
	Type                   string
	URL                    string
	EncryptedAPIKey        string
	Enabled                bool
	EncryptedWebhookSecret string
}

// UpdateArrInstanceParams is the input shape for ArrInstanceRepository.Update.
// Webhook secret rotation is intentionally a separate method (UpdateWebhookSecret)
// so an instance-edit form can't accidentally clear or replace the secret.
type UpdateArrInstanceParams struct {
	Name            string
	Type            string
	URL             string
	EncryptedAPIKey string
	Enabled         bool
}

// ArrInstanceRepository wraps the arr_instances table.
type ArrInstanceRepository struct {
	db *sql.DB
}

// NewArrInstanceRepository returns a repository backed by db.
func NewArrInstanceRepository(db *sql.DB) *ArrInstanceRepository {
	return &ArrInstanceRepository{db: db}
}

// Count returns the total number of arr_instances rows.
func (r *ArrInstanceRepository) Count(ctx context.Context) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM arr_instances`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count arr_instances: %w", err)
	}
	return n, nil
}

// CountByType returns the number of arr_instances rows with type = arrType.
func (r *ArrInstanceRepository) CountByType(ctx context.Context, arrType string) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM arr_instances WHERE type = ?`, arrType).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count by type: %w", err)
	}
	return n, nil
}

// CountByTypePrefix returns the number of rows whose type starts with the
// given prefix — used to group whisparr-v2 and whisparr-v3 together when
// generating display names.
func (r *ArrInstanceRepository) CountByTypePrefix(ctx context.Context, prefix string) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM arr_instances WHERE type LIKE ?`, prefix+"%").Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count by type prefix: %w", err)
	}
	return n, nil
}

// ListAll returns every arr_instance row, ordered by id ASC.
func (r *ArrInstanceRepository) ListAll(ctx context.Context) ([]ArrInstance, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, name, type, url, api_key, enabled, webhook_secret FROM arr_instances ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("query arr_instances: %w", err)
	}
	defer rows.Close()

	var instances []ArrInstance
	for rows.Next() {
		var inst ArrInstance
		if err := rows.Scan(
			&inst.ID, &inst.Name, &inst.Type, &inst.URL,
			&inst.EncryptedAPIKey, &inst.Enabled, &inst.EncryptedWebhookSecret,
		); err != nil {
			return nil, fmt.Errorf("scan arr_instance: %w", err)
		}
		instances = append(instances, inst)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate arr_instances: %w", err)
	}
	return instances, nil
}

// ListEnabled returns only rows where enabled = 1, ordered by id ASC.
func (r *ArrInstanceRepository) ListEnabled(ctx context.Context) ([]ArrInstance, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, name, type, url, api_key, enabled, webhook_secret FROM arr_instances WHERE enabled = 1 ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("query enabled arr_instances: %w", err)
	}
	defer rows.Close()

	var instances []ArrInstance
	for rows.Next() {
		var inst ArrInstance
		if err := rows.Scan(
			&inst.ID, &inst.Name, &inst.Type, &inst.URL,
			&inst.EncryptedAPIKey, &inst.Enabled, &inst.EncryptedWebhookSecret,
		); err != nil {
			return nil, fmt.Errorf("scan arr_instance: %w", err)
		}
		instances = append(instances, inst)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate arr_instances: %w", err)
	}
	return instances, nil
}

// GetByID returns the row matching id, or ErrNotFound.
func (r *ArrInstanceRepository) GetByID(ctx context.Context, id int64) (ArrInstance, error) {
	var inst ArrInstance
	err := r.db.QueryRowContext(ctx,
		`SELECT id, name, type, url, api_key, enabled, webhook_secret FROM arr_instances WHERE id = ?`, id).
		Scan(&inst.ID, &inst.Name, &inst.Type, &inst.URL,
			&inst.EncryptedAPIKey, &inst.Enabled, &inst.EncryptedWebhookSecret)
	if errors.Is(err, sql.ErrNoRows) {
		return ArrInstance{}, ErrNotFound
	}
	if err != nil {
		return ArrInstance{}, fmt.Errorf("get arr_instance: %w", err)
	}
	return inst, nil
}

// FindIDByURL returns the id of the row matching url, or ErrNotFound if no
// such row exists. Used by config import to skip duplicate URLs.
func (r *ArrInstanceRepository) FindIDByURL(ctx context.Context, url string) (int64, error) {
	var id int64
	err := r.db.QueryRowContext(ctx,
		`SELECT id FROM arr_instances WHERE url = ?`, url).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("find arr_instance by url: %w", err)
	}
	return id, nil
}

// Create inserts a new row and returns its id.
func (r *ArrInstanceRepository) Create(ctx context.Context, p CreateArrInstanceParams) (int64, error) {
	var webhookSecret interface{}
	if p.EncryptedWebhookSecret != "" {
		webhookSecret = p.EncryptedWebhookSecret
	}
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO arr_instances (name, type, url, api_key, enabled, webhook_secret) VALUES (?, ?, ?, ?, ?, ?)`,
		p.Name, p.Type, p.URL, p.EncryptedAPIKey, p.Enabled, webhookSecret)
	if err != nil {
		return 0, fmt.Errorf("insert arr_instance: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("last insert id: %w", err)
	}
	return id, nil
}

// Update replaces the row's name/type/url/api_key/enabled. Does NOT touch
// webhook_secret — use UpdateWebhookSecret to rotate it.
func (r *ArrInstanceRepository) Update(ctx context.Context, id int64, p UpdateArrInstanceParams) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE arr_instances SET name = ?, type = ?, url = ?, api_key = ?, enabled = ? WHERE id = ?`,
		p.Name, p.Type, p.URL, p.EncryptedAPIKey, p.Enabled, id)
	if err != nil {
		return fmt.Errorf("update arr_instance: %w", err)
	}
	return nil
}

// UpdateWebhookSecret rotates an instance's webhook_secret to the given
// (already-encrypted) value. Returns ErrNotFound if no row matched.
func (r *ArrInstanceRepository) UpdateWebhookSecret(ctx context.Context, id int64, encryptedSecret string) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE arr_instances SET webhook_secret = ? WHERE id = ?`,
		encryptedSecret, id)
	if err != nil {
		return fmt.Errorf("update webhook_secret: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes the row matching id. Returns nil if no row matched.
func (r *ArrInstanceRepository) Delete(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM arr_instances WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete arr_instance: %w", err)
	}
	return nil
}
