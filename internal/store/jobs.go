package store

import (
	"context"
	"encoding/json"
	"strings"
)

// EnqueueJob adds background work. One queued/running job of the same kind
// per document is enough: a second request returns the existing one.
func (s *Store) EnqueueJob(ctx context.Context, userID int64, documentID *int64, kind string, params any) (*Job, error) {
	if documentID != nil {
		var id int64
		if s.DB.QueryRowContext(ctx, `SELECT id FROM jobs WHERE document_id = ? AND kind = ? AND status IN ('queued', 'running') LIMIT 1`, *documentID, kind).Scan(&id) == nil {
			return s.GetJob(ctx, id)
		}
	}
	b := []byte("{}")
	if params != nil {
		if j, err := json.Marshal(params); err == nil {
			b = j
		}
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO jobs (user_id, document_id, kind, params) VALUES (?, ?, ?, ?)`, userID, documentID, kind, string(b))
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetJob(ctx, id)
}

const jobCols = `j.id, j.user_id, j.document_id, j.kind, j.status, j.attempts, j.progress, j.log, j.error, j.params, j.created_at, j.started_at, j.finished_at,
	COALESCE((SELECT d.title FROM documents d WHERE d.id = j.document_id), '')`

func scanJob(row interface{ Scan(...any) error }) (*Job, error) {
	var j Job
	var params string
	if err := row.Scan(&j.ID, &j.UserID, &j.DocumentID, &j.Kind, &j.Status, &j.Attempts, &j.Progress, &j.Log, &j.Error, &params, &j.CreatedAt, &j.StartedAt, &j.FinishedAt, &j.DocumentTitle); err != nil {
		return nil, notFound(err)
	}
	j.Params = json.RawMessage(params)
	return &j, nil
}

// GetJob returns a job.
func (s *Store) GetJob(ctx context.Context, id int64) (*Job, error) {
	return scanJob(s.DB.QueryRowContext(ctx, `SELECT `+jobCols+` FROM jobs j WHERE j.id = ?`, id))
}

// ClaimJob takes the oldest queued job and marks it running. nil = nothing to do.
func (s *Store) ClaimJob(ctx context.Context, kinds []string) (*Job, error) {
	ph := make([]string, len(kinds))
	args := []any{}
	for i, k := range kinds {
		ph[i] = "?"
		args = append(args, k)
	}
	var id int64
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM jobs WHERE status = 'queued' AND kind IN (`+strings.Join(ph, ",")+`) ORDER BY id LIMIT 1`, args...).Scan(&id)
	if err != nil {
		return nil, nil //nolint:nilerr
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE jobs SET status = 'running', attempts = attempts + 1, started_at = ?, error = '' WHERE id = ? AND status = 'queued'`, Now(), id)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, nil
	}
	return s.GetJob(ctx, id)
}

// JobProgress updates the progress line and appends to the log.
func (s *Store) JobProgress(ctx context.Context, id int64, progress, logLine string) {
	if logLine != "" {
		_, _ = s.DB.ExecContext(ctx, `UPDATE jobs SET progress = ?, log = substr(log || ? || char(10), -20000) WHERE id = ?`, progress, logLine, id)
		return
	}
	_, _ = s.DB.ExecContext(ctx, `UPDATE jobs SET progress = ? WHERE id = ?`, progress, id)
}

// FinishJob ends a job as done or failed.
func (s *Store) FinishJob(ctx context.Context, id int64, errMsg string) {
	status := "done"
	if errMsg != "" {
		status = "failed"
	}
	_, _ = s.DB.ExecContext(ctx, `UPDATE jobs SET status = ?, error = ?, finished_at = ?, progress = '' WHERE id = ?`, status, errMsg, Now(), id)
}

// RequeueRunning puts jobs left "running" by a crash back in the queue.
func (s *Store) RequeueRunning(ctx context.Context) int64 {
	res, err := s.DB.ExecContext(ctx, `UPDATE jobs SET status = 'queued', started_at = NULL, progress = '' WHERE status = 'running'`)
	if err != nil {
		return 0
	}
	n, _ := res.RowsAffected()
	return n
}

// RetryJob puts a failed job back in the queue.
func (s *Store) RetryJob(ctx context.Context, userID, id int64) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE jobs SET status = 'queued', error = '', finished_at = NULL, progress = '' WHERE id = ? AND user_id = ? AND status IN ('failed', 'cancelled')`, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// CancelJob drops a queued job.
func (s *Store) CancelJob(ctx context.Context, userID, id int64) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE jobs SET status = 'cancelled', finished_at = ? WHERE id = ? AND user_id = ? AND status = 'queued'`, Now(), id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListJobs lists a person's jobs, newest first; status "" = all, "active" = queued + running.
func (s *Store) ListJobs(ctx context.Context, userID int64, status string, limit int) ([]Job, error) {
	if limit <= 0 {
		limit = 100
	}
	q := `SELECT ` + jobCols + ` FROM jobs j WHERE j.user_id = ?`
	args := []any{userID}
	switch status {
	case "":
	case "active":
		q += ` AND j.status IN ('queued', 'running')`
	default:
		q += ` AND j.status = ?`
		args = append(args, status)
	}
	q += ` ORDER BY j.id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		j.Log = "" // the list stays light; GET /jobs/{id} has it
		out = append(out, *j)
	}
	return out, rows.Err()
}

// JobCounts counts queued and running jobs (everyone's for the worker, one person's otherwise).
func (s *Store) JobCounts(ctx context.Context, userID int64) (queued, running, failed int) {
	q := `SELECT SUM(status = 'queued'), SUM(status = 'running'), SUM(status = 'failed') FROM jobs`
	args := []any{}
	if userID > 0 {
		q += ` WHERE user_id = ?`
		args = append(args, userID)
	}
	var a, b, c *int
	_ = s.DB.QueryRowContext(ctx, q, args...).Scan(&a, &b, &c)
	if a != nil {
		queued = *a
	}
	if b != nil {
		running = *b
	}
	if c != nil {
		failed = *c
	}
	return
}

// PruneJobs removes finished jobs older than the newest keep.
func (s *Store) PruneJobs(ctx context.Context, keep int) {
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM jobs WHERE status IN ('done', 'cancelled') AND id NOT IN (SELECT id FROM jobs WHERE status IN ('done', 'cancelled') ORDER BY id DESC LIMIT ?)`, keep)
}

// ClearJobs removes a person's finished jobs.
func (s *Store) ClearJobs(ctx context.Context, userID int64) {
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM jobs WHERE user_id = ? AND status IN ('done', 'failed', 'cancelled')`, userID)
}
