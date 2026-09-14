package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "github.com/lib/pq"
)

var ErrNotFound = errors.New("job not found")

type Store struct {
	db      *sql.DB
	timeout time.Duration
}

type Job struct {
	ID               string          `json:"id"`
	Status           string          `json:"status"`
	Error            string          `json:"error,omitempty"`
	OriginalFilename string          `json:"original_filename"`
	FilePath         string          `json:"-"`
	Report           json.RawMessage `json:"-"`
}

func Open(databaseURL string, maxOpen, maxIdle int, timeout time.Duration) (*Store, error) {
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxIdle)
	db.SetConnMaxLifetime(30 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	return &Store{db: db, timeout: timeout}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Stats() sql.DBStats { return s.db.Stats() }

func (s *Store) FailInterrupted(ctx context.Context) error {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	_, err := s.db.ExecContext(ctx, `
		UPDATE jobs
		SET status = 'failed', error = 'обработка прервана перезапуском', finished_at = now()
		WHERE status IN ('queued', 'running')`)
	return err
}

func (s *Store) CreateJob(ctx context.Context, id, filePath, originalFilename string) error {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO jobs (id, status, file_path, original_filename)
		VALUES ($1, 'queued', $2, $3)`, id, filePath, originalFilename)
	return err
}

func (s *Store) GetJob(ctx context.Context, id string) (Job, error) {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	var job Job
	var errorText sql.NullString
	var report []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT id, status, error, original_filename, file_path, report
		FROM jobs WHERE id = $1`, id).Scan(
		&job.ID, &job.Status, &errorText, &job.OriginalFilename, &job.FilePath, &report,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, err
	}
	job.Error = errorText.String
	job.Report = report
	return job, nil
}

func (s *Store) ClaimJob(ctx context.Context) (Job, error) {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	var job Job
	err := s.db.QueryRowContext(ctx, `
		WITH next AS (
			SELECT id FROM jobs
			WHERE status = 'queued'
			ORDER BY created_at, id
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE jobs
		SET status = 'running', started_at = now(), error = NULL
		FROM next
		WHERE jobs.id = next.id
		RETURNING jobs.id, jobs.file_path, jobs.original_filename`).Scan(
		&job.ID, &job.FilePath, &job.OriginalFilename,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	return job, err
}

func (s *Store) MarkDone(ctx context.Context, id string, report []byte) error {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	_, err := s.db.ExecContext(ctx, `
		UPDATE jobs SET status = 'done', report = $2, error = NULL, finished_at = now()
		WHERE id = $1 AND status = 'running'`, id, string(report))
	return err
}

func (s *Store) MarkFailed(ctx context.Context, id, errorText string) error {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	_, err := s.db.ExecContext(ctx, `
		UPDATE jobs SET status = 'failed', report = NULL, error = $2, finished_at = now()
		WHERE id = $1 AND status = 'running'`, id, errorText)
	return err
}

func (s *Store) withTimeout(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, s.timeout)
}
