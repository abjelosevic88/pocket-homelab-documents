package server

import (
	"context"
	"database/sql"
	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-documents/internal/store"
	"strconv"
)

func (s *Server) documentEvent(ctx context.Context, uid int64, topic string, d *store.Document, extra map[string]any) core.CommitHook {
	if !s.core.Configured() {
		return nil
	}
	// Identity is resolved before taking the application's sole SQL connection.
	u, err := s.store.GetUser(ctx, uid)
	return func(tx *sql.Tx) error {
		if err != nil {
			return err
		}
		payload := map[string]any{"id": d.ID, "title": d.Title, "user_id": uid, "user_email": u.Email}
		for k, v := range extra {
			payload[k] = v
		}
		return core.QueueEvent(ctx, tx, topic, "document:"+strconv.FormatInt(d.ID, 10), payload)
	}
}

// Finish the job with its document status and event. A restart cannot replay a
// completed job merely because it crashed between these writes.
func (s *Server) completeProcessing(ctx context.Context, j *store.Job, d *store.Document, status, msg, topic string, extra map[string]any) error {
	event := s.documentEvent(ctx, d.UserID, topic, d, extra)
	return s.store.SetStatus(ctx, d.ID, status, msg, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE jobs SET status = ?, error = ?, finished_at = ?, progress = '' WHERE id = ?`, status, msg, store.Now(), j.ID); err != nil {
			return err
		}
		if event != nil {
			return event(tx)
		}
		return nil
	})
}
