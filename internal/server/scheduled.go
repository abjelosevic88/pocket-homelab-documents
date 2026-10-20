package server

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-documents/internal/store"
)

// Scheduled rules fire once per document on a date relative to one of its
// dates: "30 days before the custom field Expires", "7 days after created".
// They run with the janitor, once an hour, and after a rule is saved.

// scheduleDue says whether a scheduled rule is due for a document today.
func scheduleDue(r store.Rule, d *store.Document, today time.Time) bool {
	f := r.Filters
	var base string
	switch {
	case f.ScheduleField == "" || f.ScheduleField == "created":
		base = d.Created
	case f.ScheduleField == "added":
		base = strings.SplitN(d.AddedAt, "T", 2)[0]
	case strings.HasPrefix(f.ScheduleField, "cf:"):
		id, _ := strconv.ParseInt(strings.TrimPrefix(f.ScheduleField, "cf:"), 10, 64)
		for _, v := range d.Fields {
			if v.FieldID == id {
				base = v.Value
			}
		}
	}
	if len(base) < 10 {
		return false
	}
	t, err := time.Parse("2006-01-02", base[:10])
	if err != nil {
		return false
	}
	due := t.AddDate(0, 0, f.ScheduleOffsetDays)
	// due today or in the past (so a rule made later still catches up), but not more than a year late
	return !today.Before(due) && today.Sub(due) < 366*24*time.Hour
}

// runScheduled applies every person's scheduled rules that are due.
func (s *Server) runScheduled(ctx context.Context) {
	today := time.Now().Truncate(24 * time.Hour)
	for _, uid := range s.store.UserIDs(ctx) {
		rules, err := s.store.ListRules(ctx, uid)
		if err != nil {
			continue
		}
		var sched []store.Rule
		for _, r := range rules {
			if r.Enabled && r.Trigger == "scheduled" {
				sched = append(sched, r)
			}
		}
		if len(sched) == 0 {
			continue
		}
		for _, id := range s.store.DocumentIDs(ctx, uid, false) {
			d, err := s.store.GetDocument(ctx, uid, id, true)
			if err != nil {
				continue
			}
			for _, r := range sched {
				if s.store.RuleRan(ctx, r.ID, id) || !scheduleDue(r, d, today) || !ruleFits(r, d, "") {
					continue
				}
				s.applyRule(ctx, r, d, "rule: "+r.Name)
				s.store.MarkRuleRan(ctx, r.ID, id)
			}
		}
	}
}

// applyRule runs a rule's actions on a document: the changes, then the message.
func (s *Server) applyRule(ctx context.Context, r store.Rule, d *store.Document, actor string) {
	c := s.ruleChanges(ctx, r, d)
	if changed, err := s.store.UpdateDocument(ctx, d.UserID, d.ID, c); err == nil && len(changed) > 0 {
		s.store.AddHistory(ctx, d.ID, d.UserID, actor, "rule", changed)
		if nd, err := s.store.GetDocumentAny(ctx, d.ID, true); err == nil {
			*d = *nd
		}
		_ = s.placeInFolder(ctx, d)
	}
	if r.Actions.Notify {
		st, _ := s.store.GetSettings(ctx, d.UserID)
		if strings.TrimSpace(st.NotifyURL) == "" {
			s.store.AddHistory(ctx, d.ID, d.UserID, actor, "notify", map[string]any{"error": "no notification URL in Settings → Integrations"})
			return
		}
		names := s.store.Names(ctx, d.UserID)
		msg := strings.TrimSpace(r.Actions.NotifyMessage)
		if msg == "" {
			msg = "{title}"
		}
		msg = strings.NewReplacer("{title}", d.Title, "{created}", d.Created, "{correspondent}", nameOr(names.Correspondents, d.CorrespondentID), "{document_type}", nameOr(names.Types, d.DocumentTypeID), "{rule}", r.Name, "{asn}", asnStr(d.ASN)).Replace(msg)
		if s.cfg.BaseURL != "" {
			msg += "\n" + s.cfg.BaseURL + "/documents/" + strconv.FormatInt(d.ID, 10)
		}
		if err := sendNotify(st.NotifyURL, "Pocket Documents: "+r.Name, msg); err != nil {
			s.store.AddHistory(ctx, d.ID, d.UserID, actor, "notify", map[string]any{"error": err.Error()})
		} else {
			s.store.AddHistory(ctx, d.ID, d.UserID, actor, "notify", map[string]any{"message": msg})
		}
	}
}

func nameOr(m map[int64]string, id *int64) string {
	if id == nil {
		return ""
	}
	return m[*id]
}

func asnStr(n *int64) string {
	if n == nil {
		return ""
	}
	return fmt.Sprint(*n)
}
