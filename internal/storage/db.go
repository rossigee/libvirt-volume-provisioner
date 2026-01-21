package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3" // Register SQLite driver
	"github.com/rossigee/libvirt-volume-provisioner/pkg/types"
	"github.com/sirupsen/logrus"
)

// JobRecord represents a job stored in the database
type JobRecord struct {
	ID           string
	Status       string
	RequestJSON  string
	ProgressJSON string
	ErrorMessage string
	RetryCount   int
	CreatedAt    time.Time
	UpdatedAt    time.Time
	CompletedAt  *time.Time
}

// Store provides SQLite-based job persistence
type Store struct {
	db     *sql.DB
	dbPath string
	mu     sync.RWMutex
}

// NewStore initializes a new SQLite store
func NewStore(dbPath string) (*Store, error) {
	// Open or create database
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Test connection
	if err := db.PingContext(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Configure connection pool
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(time.Hour)

	store := &Store{
		db:     db,
		dbPath: dbPath,
	}

	// Initialize schema
	if err := store.initSchema(); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			logrus.WithError(closeErr).Warn("Failed to close database connection after init error")
		}
		return nil, err
	}

	logrus.WithField("db_path", dbPath).Info("Initialized job storage database")
	return store, nil
}

// initSchema applies all pending migrations
func (s *Store) initSchema() error {
	// Get current schema version
	currentVersion := 0
	row := s.db.QueryRowContext(context.Background(), "SELECT COALESCE(MAX(version), 0) FROM schema_version")
	_ = row.Scan(&currentVersion) // Ignore error - schema_version table may not exist yet

	// Apply pending migrations
	for _, migration := range Migrations {
		if migration.Version <= currentVersion {
			continue
		}

		logrus.WithField("version", migration.Version).Info("Applying schema migration")

		// Execute migration SQL
		if _, err := s.db.ExecContext(context.Background(), migration.SQL); err != nil {
			return fmt.Errorf("failed to apply migration v%d: %w", migration.Version, err)
		}

		// Record migration
		if _, err := s.db.ExecContext(context.Background(),
			"INSERT INTO schema_version (version, applied_at) VALUES (?, ?)",
			migration.Version,
			time.Now().Unix(),
		); err != nil {
			return fmt.Errorf("failed to record migration v%d: %w", migration.Version, err)
		}

		currentVersion = migration.Version
	}

	return nil
}

// SaveJob persists or updates a job record
func (s *Store) SaveJob(ctx context.Context, record *JobRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			if rollbackErr := tx.Rollback(); rollbackErr != nil {
				logrus.WithError(rollbackErr).Warn("Failed to rollback transaction")
			}
		}
	}()

	// Check if job exists
	var exists bool
	err = tx.QueryRowContext(ctx, "SELECT 1 FROM jobs WHERE id = ?", record.ID).Scan(&exists)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("failed to check job existence: %w", err)
	}

	if exists {
		// Update existing job
		_, err := tx.ExecContext(ctx,
			`UPDATE jobs
			 SET status = ?, progress_json = ?, error_message = ?,
			     retry_count = ?, updated_at = ?, completed_at = ?
			 WHERE id = ?`,
			record.Status,
			record.ProgressJSON,
			record.ErrorMessage,
			record.RetryCount,
			record.UpdatedAt.Unix(),
			timeToUnixPtr(record.CompletedAt),
			record.ID,
		)
		if err != nil {
			return fmt.Errorf("failed to update job: %w", err)
		}
	} else {
		// Insert new job
		_, err := tx.ExecContext(ctx,
			`INSERT INTO jobs
			 (id, status, request_json, progress_json, error_message,
			  retry_count, created_at, updated_at, completed_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			record.ID,
			record.Status,
			record.RequestJSON,
			record.ProgressJSON,
			record.ErrorMessage,
			record.RetryCount,
			record.CreatedAt.Unix(),
			record.UpdatedAt.Unix(),
			timeToUnixPtr(record.CompletedAt),
		)
		if err != nil {
			return fmt.Errorf("failed to insert job: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	committed = true

	return nil
}

// GetJob retrieves a job by ID
func (s *Store) GetJob(id string) (*JobRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	record := &JobRecord{}
	var createdAtUnix, updatedAtUnix int64
	var completedAtUnix *int64

	err := s.db.QueryRowContext(context.Background(),
		`SELECT id, status, request_json, progress_json, error_message,
		        retry_count, created_at, updated_at, completed_at
		 FROM jobs WHERE id = ?`,
		id,
	).Scan(
		&record.ID,
		&record.Status,
		&record.RequestJSON,
		&record.ProgressJSON,
		&record.ErrorMessage,
		&record.RetryCount,
		&createdAtUnix,
		&updatedAtUnix,
		&completedAtUnix,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("job not found: %s", id)
		}
		return nil, fmt.Errorf("failed to query job: %w", err)
	}

	record.CreatedAt = time.Unix(createdAtUnix, 0)
	record.UpdatedAt = time.Unix(updatedAtUnix, 0)
	if completedAtUnix != nil {
		t := time.Unix(*completedAtUnix, 0)
		record.CompletedAt = &t
	}

	return record, nil
}

// ListJobsFilter defines filtering options for ListJobs
type ListJobsFilter struct {
	Status string // optional: filter by status
	Limit  int    // default: 100
	Offset int    // default: 0
}

// ListJobs retrieves jobs with optional filtering
func (s *Store) ListJobs(filter ListJobsFilter) ([]*JobRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if filter.Limit == 0 {
		filter.Limit = 100
	}
	if filter.Limit > 10000 {
		filter.Limit = 10000 // Cap limit to prevent excessive queries
	}

	query := "SELECT id, status, request_json, progress_json, error_message, " +
		"retry_count, created_at, updated_at, completed_at FROM jobs"
	args := []interface{}{}

	if filter.Status != "" {
		query += " WHERE status = ?"
		args = append(args, filter.Status)
	}

	query += " ORDER BY updated_at DESC LIMIT ? OFFSET ?"
	args = append(args, filter.Limit, filter.Offset)

	rows, err := s.db.QueryContext(context.Background(), query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query jobs: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			logrus.WithError(closeErr).Warn("Failed to close database rows")
		}
	}()

	var records []*JobRecord
	for rows.Next() {
		record := &JobRecord{}
		var completedAtUnix *int64
		var createdAtUnix, updatedAtUnix int64

		if err := rows.Scan(
			&record.ID,
			&record.Status,
			&record.RequestJSON,
			&record.ProgressJSON,
			&record.ErrorMessage,
			&record.RetryCount,
			&createdAtUnix,
			&updatedAtUnix,
			&completedAtUnix,
		); err != nil {
			return nil, fmt.Errorf("failed to scan job: %w", err)
		}

		record.CreatedAt = time.Unix(createdAtUnix, 0)
		record.UpdatedAt = time.Unix(updatedAtUnix, 0)
		if completedAtUnix != nil {
			t := time.Unix(*completedAtUnix, 0)
			record.CompletedAt = &t
		}

		records = append(records, record)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating jobs: %w", err)
	}

	return records, nil
}

// MarkInProgressJobsFailed marks all running/pending jobs as failed (called at startup)
func (s *Store) MarkInProgressJobsFailed() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().Unix()
	_, err := s.db.ExecContext(context.Background(),
		`UPDATE jobs
		 SET status = ?, error_message = ?, updated_at = ?, completed_at = ?
		 WHERE status IN (?, ?)`,
		string(types.StatusFailed),
		"daemon restarted while job in progress",
		now,
		now,
		string(types.StatusRunning),
		string(types.StatusPending),
	)

	if err != nil {
		return fmt.Errorf("failed to mark in-progress jobs as failed: %w", err)
	}

	return nil
}

// DeleteOldJobs deletes jobs older than the specified duration (for cleanup)
func (s *Store) DeleteOldJobs(olderThan time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Now().Add(-olderThan).Unix()

	result, err := s.db.ExecContext(context.Background(),
		`DELETE FROM jobs
		 WHERE status IN (?, ?) AND updated_at < ?`,
		string(types.StatusCompleted),
		string(types.StatusFailed),
		cutoff,
	)

	if err != nil {
		return fmt.Errorf("failed to delete old jobs: %w", err)
	}

	deleted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get affected rows: %w", err)
	}

	if deleted > 0 {
		logrus.WithField("deleted_count", deleted).Debug("Cleaned up old job records")
	}

	return nil
}

// Close closes the database connection
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db != nil {
		if err := s.db.Close(); err != nil {
			return fmt.Errorf("failed to close database connection: %w", err)
		}
	}
	return nil
}

// Helper functions

// timeToUnixPtr converts a time pointer to Unix timestamp pointer
func timeToUnixPtr(t *time.Time) interface{} {
	if t == nil {
		return nil
	}
	return t.Unix()
}

// GetJobCount returns the count of jobs with a given status
func (s *Store) GetJobCount(status string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var count int
	err := s.db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM jobs WHERE status = ?", status).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to get job count: %w", err)
	}

	return count, nil
}
