package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-documents/internal/store"
)

// ---- the AI's queue ----
//
// Every document the AI should read gets a document_ai record, the way Pocket
// Money keeps one per transaction it guesses items for: a new document whose
// AI was not reachable waits as "pending" until it is; a failed run is tried
// again later (aiBackoff); older documents are queued from Settings → AI and
// read a few at a time. The record keeps the AI's summary and what it set.

// aiBackoff is how long a failed run waits before the next try; after the
// last one it stays failed until someone retries it.
var aiBackoff = []time.Duration{10 * time.Minute, 30 * time.Minute, time.Hour, 3 * time.Hour, 6 * time.Hour, 12 * time.Hour, 24 * time.Hour}

// aiRoundSize is how many queued documents one round reads.
const aiRoundSize = 6

// runAI runs the AI on a document now and records the outcome. With
// manual, a failure is only recorded (the person sees it at once);
// otherwise it is tried again later.
func (s *Server) runAI(ctx context.Context, d *store.Document, mode aiFillMode, actor string, manual bool) (aiFillResult, error) {
	rec := &store.DocAI{DocumentID: d.ID, UserID: d.UserID}
	if old, err := s.store.GetDocAI(ctx, d.ID); err == nil {
		rec = old
	}
	params, _ := json.Marshal(mode)
	rec.Params = params
	res, err := s.aiFill(ctx, d, mode, actor)
	rec.Model = firstNonEmpty(res.Model, rec.Model)
	if err != nil {
		rec.Status, rec.Error, rec.NextTryAt = "error", clip(err.Error(), 500), ""
		if !manual {
			if rec.Attempts < len(aiBackoff) {
				rec.NextTryAt = time.Now().UTC().Add(aiBackoff[rec.Attempts]).Format(time.RFC3339Nano)
			}
			rec.Attempts++
		}
		_ = s.store.SaveDocAI(ctx, rec)
		return res, err
	}
	rec.Status, rec.Error, rec.Attempts, rec.NextTryAt = "done", "", 0, ""
	rec.Summary, rec.Reason, rec.Applied = res.Summary, res.Reason, res.Applied
	if res.Applied == nil {
		rec.Applied = []string{}
	}
	if err := s.store.SaveDocAI(ctx, rec); err != nil {
		s.log.Warn("ai record", "doc", d.ID, "err", err)
	}
	if len(res.Applied) > 0 && !mode.InJob {
		s.afterAI(ctx, d)
	}
	return res, nil
}

// afterAI files a document the AI changed: search index, documents folder,
// the classifier.
func (s *Server) afterAI(ctx context.Context, d *store.Document) {
	_ = s.store.IndexDocument(ctx, d.ID)
	if nd, err := s.store.GetDocumentAny(ctx, d.ID, false); err == nil {
		if err := s.placeInFolder(ctx, nd); err != nil {
			s.log.Warn("documents folder", "doc", d.ID, "err", err)
		}
	}
	s.classifiers.Invalidate(d.UserID)
}

// queueAI leaves a document for the AI to read later.
func (s *Server) queueAI(ctx context.Context, d *store.Document, mode aiFillMode, why string) {
	params, _ := json.Marshal(mode)
	_ = s.store.SaveDocAI(ctx, &store.DocAI{DocumentID: d.ID, UserID: d.UserID, Status: "pending", Params: params, Error: why, NextTryAt: store.Now()})
}

// aiLoop reads queued documents every minute, or at once when nudged.
func (s *Server) aiLoop(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		for s.aiRound(ctx) == aiRoundSize && ctx.Err() == nil {
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.aiWake:
		}
	}
}

func (s *Server) nudgeAI() {
	select {
	case s.aiWake <- struct{}{}:
	default:
	}
}

// aiRound reads the documents whose turn has come and reports how many it
// took. A person whose AI is not reachable is skipped: their documents keep
// waiting without using up their tries.
func (s *Server) aiRound(ctx context.Context) int {
	due, err := s.store.DueDocAI(ctx, store.Now(), aiRoundSize)
	if err != nil || len(due) == 0 {
		return 0
	}
	ready := map[int64]bool{}
	taken := 0
	for _, rec := range due {
		if ctx.Err() != nil {
			break
		}
		ok, seen := ready[rec.UserID]
		if !seen {
			client, _, _ := s.aiFor(ctx, rec.UserID)
			ok = client != nil
			ready[rec.UserID] = ok
		}
		if !ok {
			// look again in a while, not every minute
			rec.NextTryAt = time.Now().UTC().Add(15 * time.Minute).Format(time.RFC3339Nano)
			_ = s.store.SaveDocAI(ctx, &rec)
			continue
		}
		taken++
		d, err := s.store.GetDocumentAny(ctx, rec.DocumentID, true)
		if err != nil {
			continue
		}
		if strings.TrimSpace(d.Content) == "" {
			rec.Status, rec.Error, rec.NextTryAt = "skipped", "no text to read", ""
			_ = s.store.SaveDocAI(ctx, &rec)
			continue
		}
		var mode aiFillMode
		_ = json.Unmarshal(rec.Params, &mode)
		mode.Event = true
		if res, err := s.runAI(ctx, d, mode, "ai", false); err != nil {
			s.log.Warn("ai", "doc", d.ID, "err", err)
		} else if len(res.Applied) > 0 {
			s.log.Info("ai filled in", "doc", d.ID, "applied", strings.Join(res.Applied, ", "))
		}
	}
	return taken
}

// ---- endpoints ----

func (s *Server) handleGetDocAI(w http.ResponseWriter, r *http.Request) {
	ctx, uid, id := r.Context(), me(r), idParam(r, "id")
	d, err := s.store.GetDocument(ctx, uid, id, false)
	if err != nil {
		s.fail(w, err, "document")
		return
	}
	if d.Access != "" && d.Access != "owner" {
		writeErr(w, http.StatusForbidden, "only the owner can see this")
		return
	}
	rec, err := s.store.GetDocAI(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusOK, map[string]any{"document_id": id, "status": "none", "applied": []string{}})
		return
	}
	if err != nil {
		s.fail(w, err, "ai")
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (s *Server) handleAIOverview(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.store.DocAICounts(r.Context(), me(r)))
}

// handleAIBackfill queues the person's documents the AI has not read yet
// (and, with retry_failed, the ones it gave up on). It only fills what is
// empty: their own filing stays.
func (s *Server) handleAIBackfill(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), me(r)
	var in struct {
		RetryFailed bool `json:"retry_failed"`
	}
	_ = decode(r, &in)
	if client, _, detail := s.aiFor(ctx, uid); client == nil {
		writeErr(w, http.StatusServiceUnavailable, "AI is not available: "+detail)
		return
	}
	params, _ := json.Marshal(aiFillMode{Fill: true})
	n, err := s.store.QueueDocAI(ctx, uid, string(params), in.RetryFailed)
	if err != nil {
		s.fail(w, err, "queue")
		return
	}
	s.nudgeAI()
	writeJSON(w, http.StatusOK, map[string]any{"queued": n, "counts": s.store.DocAICounts(ctx, uid)})
}
