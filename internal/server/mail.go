package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-documents/internal/extract"
	"github.com/abjelosevic88/pocket-homelab-documents/internal/mail"
	"github.com/abjelosevic88/pocket-homelab-documents/internal/store"
)

// Mail: IMAP accounts are polled on a schedule; each rule picks messages in
// a folder by sender, recipient, subject, body and attachment name and turns
// the attachments (or the whole mail as .eml) into documents, then marks,
// flags, moves or deletes the message. A message is consumed once per rule.

const (
	mailFetchTimeout = 10 * time.Minute
	mailMaxPerRun    = 500 // newest candidates per rule per run
	mailFetchChunk   = 20
	mailTinyImage    = 20 << 10 // smaller images are signatures and logos
)

// mailBusy holds the ids of accounts being fetched right now, so the loop
// and "Fetch now" never run the same account twice at once.
var mailBusy sync.Map

// mountMail registers the mail routes inside the authenticated API group.
func (s *Server) mountMail(r chi.Router) {
	r.Get("/mail/accounts", s.handleListMailAccounts)
	r.Post("/mail/accounts", s.handleCreateMailAccount)
	r.Put("/mail/accounts/{id}", s.handleUpdateMailAccount)
	r.Delete("/mail/accounts/{id}", s.handleDeleteMailAccount)
	r.Post("/mail/accounts/{id}/test", s.handleTestMailAccount)
	r.Post("/mail/accounts/{id}/fetch", s.handleFetchMailAccount)
	r.Get("/mail/rules", s.handleListMailRules)
	r.Post("/mail/rules", s.handleCreateMailRule)
	r.Put("/mail/rules/{id}", s.handleUpdateMailRule)
	r.Delete("/mail/rules/{id}", s.handleDeleteMailRule)
}

// mailLoop checks, once a minute, every enabled account whose last check is
// older than its poll interval.
func (s *Server) mailLoop(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		due, err := s.store.DueMailAccounts(ctx, time.Now())
		if err != nil {
			s.log.Warn("mail: cannot list accounts", "err", err)
			continue
		}
		for i := range due {
			a := due[i]
			if _, running := mailBusy.Load(a.ID); running {
				continue
			}
			s.fetchMailAccount(ctx, &a)
		}
	}
}

// ---- accounts ----

type mailAccountIn struct {
	store.MailAccount
	Password string `json:"password"`
}

func (s *Server) handleListMailAccounts(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.ListMailAccounts(r.Context(), me(r))
	if err != nil {
		s.fail(w, err, "list mail accounts")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateMailAccount(w http.ResponseWriter, r *http.Request) {
	in := mailAccountIn{MailAccount: store.MailAccount{Enabled: true}}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	a := in.MailAccount
	a.UserID, a.Password = me(r), in.Password
	if err := s.store.CreateMailAccount(r.Context(), &a); err != nil {
		if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrConflict) {
			s.fail(w, err, "create mail account")
			return
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

func (s *Server) handleUpdateMailAccount(w http.ResponseWriter, r *http.Request) {
	ctx, uid, id := r.Context(), me(r), idParam(r, "id")
	cur, err := s.store.GetMailAccount(ctx, uid, id)
	if err != nil {
		s.fail(w, err, "mail account")
		return
	}
	in := mailAccountIn{MailAccount: *cur}
	keys, err := decodeKeys(r, &in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	a := in.MailAccount
	a.ID, a.UserID = id, uid
	a.Password = ""
	if keys["password"] {
		a.Password = in.Password // "" keeps the stored one
	}
	if err := s.store.UpdateMailAccount(ctx, &a); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.fail(w, err, "mail account")
			return
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) handleDeleteMailAccount(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteMailAccount(r.Context(), me(r), idParam(r, "id")); err != nil {
		s.fail(w, err, "delete mail account")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleTestMailAccount signs in and lists the folders. The body may carry
// unsaved host/port/security/username/password to try before saving; an
// empty password falls back to the stored one.
func (s *Server) handleTestMailAccount(w http.ResponseWriter, r *http.Request) {
	ctx, uid, id := r.Context(), me(r), idParam(r, "id")
	a, err := s.store.GetMailAccount(ctx, uid, id)
	if err != nil {
		s.fail(w, err, "mail account")
		return
	}
	if r.ContentLength != 0 {
		in := mailAccountIn{MailAccount: *a}
		if err := decode(r, &in); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid json")
			return
		}
		pw := a.Password
		*a = in.MailAccount
		if in.Password != "" {
			pw = in.Password
		}
		a.Password = pw
	}
	tctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	folders, err := mail.Test(tctx, a.Host, a.Port, a.Security, a.Username, a.Password)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"folders": folders})
}

func (s *Server) handleFetchMailAccount(w http.ResponseWriter, r *http.Request) {
	ctx, uid, id := r.Context(), me(r), idParam(r, "id")
	a, err := s.store.GetMailAccount(ctx, uid, id)
	if err != nil {
		s.fail(w, err, "mail account")
		return
	}
	if _, running := mailBusy.Load(a.ID); running {
		writeErr(w, http.StatusConflict, "this account is being checked right now")
		return
	}
	consumed, errs := s.fetchMailAccount(ctx, a)
	if errs == nil {
		errs = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"consumed": consumed, "errors": errs})
}

// ---- rules ----

func (s *Server) handleListMailRules(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.ListMailRules(r.Context(), me(r))
	if err != nil {
		s.fail(w, err, "list mail rules")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ownTagIDs keeps only the ids of tags that belong to the person.
func (s *Server) ownTagIDs(ctx contextT, uid int64, ids []int64) []int64 {
	out := []int64{}
	if len(ids) == 0 {
		return out
	}
	tags, _ := s.store.ListTags(ctx, uid)
	mine := map[int64]bool{}
	for _, t := range tags {
		mine[t.ID] = true
	}
	seen := map[int64]bool{}
	for _, id := range ids {
		if mine[id] && !seen[id] {
			out = append(out, id)
			seen[id] = true
		}
	}
	return out
}

func (s *Server) checkRuleRefs(ctx contextT, uid int64, r *store.MailRule) error {
	r.AssignTags = s.ownTagIDs(ctx, uid, r.AssignTags)
	if r.AssignCorrespondent != nil {
		if _, err := s.store.GetCorrespondent(ctx, uid, *r.AssignCorrespondent); err != nil {
			return errors.New("unknown correspondent")
		}
	}
	if r.AssignDocumentType != nil {
		if _, err := s.store.GetDocumentType(ctx, uid, *r.AssignDocumentType); err != nil {
			return errors.New("unknown document type")
		}
	}
	if r.AssignStoragePath != nil {
		if _, err := s.store.GetStoragePath(ctx, uid, *r.AssignStoragePath); err != nil {
			return errors.New("unknown storage path")
		}
	}
	return nil
}

func (s *Server) handleCreateMailRule(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), me(r)
	in := store.MailRule{Enabled: true, OnlyUnseen: true}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	in.UserID = uid
	if err := s.checkRuleRefs(ctx, uid, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.CreateMailRule(ctx, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, in)
}

func (s *Server) handleUpdateMailRule(w http.ResponseWriter, r *http.Request) {
	ctx, uid, id := r.Context(), me(r), idParam(r, "id")
	cur, err := s.store.GetMailRule(ctx, uid, id)
	if err != nil {
		s.fail(w, err, "mail rule")
		return
	}
	in := *cur
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	in.ID, in.UserID = id, uid
	if err := s.checkRuleRefs(ctx, uid, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.UpdateMailRule(ctx, &in); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.fail(w, err, "mail rule")
			return
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, in)
}

func (s *Server) handleDeleteMailRule(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteMailRule(r.Context(), me(r), idParam(r, "id")); err != nil {
		s.fail(w, err, "delete mail rule")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- fetching ----

// fetchMailAccount runs every enabled rule of an account once and records
// the check on the account. It returns how many documents came in (duplicates
// count: the mail was handled) and what went wrong.
func (s *Server) fetchMailAccount(ctx context.Context, a *store.MailAccount) (consumed int, errs []string) {
	if _, loaded := mailBusy.LoadOrStore(a.ID, true); loaded {
		return 0, []string{"already running"}
	}
	defer mailBusy.Delete(a.ID)
	ctx, cancel := context.WithTimeout(ctx, mailFetchTimeout)
	defer cancel()
	fail := func(err error) (int, []string) {
		s.log.Warn("mail: check failed", "account", a.Name, "err", err)
		_ = s.store.MailAccountChecked(context.WithoutCancel(ctx), a.ID, err.Error())
		return consumed, append(errs, err.Error())
	}
	rules, err := s.store.AccountMailRules(ctx, a.ID)
	if err != nil {
		return fail(err)
	}
	if len(rules) == 0 {
		_ = s.store.MailAccountChecked(ctx, a.ID, "")
		return 0, nil
	}
	c, err := mail.Dial(ctx, a.Host, a.Port, a.Security)
	if err != nil {
		return fail(err)
	}
	defer c.Close()
	if err := c.Login(a.Username, a.Password); err != nil {
		return fail(err)
	}
	now := time.Now()
	for i := range rules {
		rule := &rules[i]
		if ctx.Err() != nil {
			return fail(ctx.Err())
		}
		n, ruleErrs := s.runMailRule(ctx, c, a, rule, now)
		consumed += n
		for _, e := range ruleErrs {
			errs = append(errs, rule.Name+": "+e)
		}
	}
	msg := ""
	if len(errs) > 0 {
		msg = strings.Join(errs, "; ")
	}
	_ = s.store.MailAccountChecked(context.WithoutCancel(ctx), a.ID, msg)
	if consumed > 0 || len(errs) > 0 {
		s.log.Info("mail: checked", "account", a.Name, "consumed", consumed, "errors", len(errs))
	}
	return consumed, errs
}

// runMailRule selects the rule's folder, finds candidates, filters, consumes.
func (s *Server) runMailRule(ctx context.Context, c *mail.Client, a *store.MailAccount, rule *store.MailRule, now time.Time) (consumed int, errs []string) {
	validity, err := c.Select(rule.Folder)
	if err != nil {
		return 0, []string{err.Error()}
	}
	uids, err := c.Search(rule.OnlyUnseen, mail.SinceFor(rule.MaxAgeDays, now))
	if err != nil {
		return 0, []string{err.Error()}
	}
	seen, err := s.store.MailSeenUIDs(ctx, a.ID, rule.ID, validity)
	if err != nil {
		return 0, []string{err.Error()}
	}
	var todo []uint32
	for _, u := range uids {
		if !seen[u] {
			todo = append(todo, u)
		}
	}
	if len(todo) > mailMaxPerRun {
		sort.Slice(todo, func(i, j int) bool { return todo[i] > todo[j] })
		todo = todo[:mailMaxPerRun]
		sort.Slice(todo, func(i, j int) bool { return todo[i] < todo[j] })
	}
	filter := mail.Filter{From: rule.FilterFrom, To: rule.FilterTo, Subject: rule.FilterSubject, Body: rule.FilterBody, Attachment: rule.FilterAttachment, MaxAgeDays: rule.MaxAgeDays}
	for start := 0; start < len(todo); start += mailFetchChunk {
		if ctx.Err() != nil {
			return consumed, append(errs, ctx.Err().Error())
		}
		end := min(start+mailFetchChunk, len(todo))
		msgs, err := c.Fetch(todo[start:end])
		if err != nil {
			return consumed, append(errs, err.Error())
		}
		for _, m := range msgs {
			if !filter.Matches(m, now) {
				continue
			}
			n, msgErrs := s.consumeMail(ctx, a, rule, m)
			errs = append(errs, msgErrs...)
			consumed += n
			_ = s.store.MarkMailSeen(ctx, a.ID, rule.ID, validity, m.UID, m.MessageID)
			if n == 0 {
				continue
			}
			if err := mailAction(c, rule, m.UID); err != nil {
				errs = append(errs, fmt.Sprintf("%s after %q: %v", rule.Action, m.Subject, err))
			}
		}
	}
	return consumed, errs
}

func mailAction(c *mail.Client, rule *store.MailRule, uid uint32) error {
	switch rule.Action {
	case "mark_read":
		return c.MarkSeen(uid)
	case "flag":
		return c.Flag(uid)
	case "move":
		return c.Move(uid, rule.ActionFolder)
	case "delete":
		return c.Delete(uid)
	}
	return nil
}

// consumeMail turns one matching message into documents per the rule.
// Returns how many were taken (duplicates included) and the errors.
func (s *Server) consumeMail(ctx context.Context, a *store.MailAccount, rule *store.MailRule, m *mail.Message) (taken int, errs []string) {
	uid := a.UserID
	base := incoming{Tags: append([]int64{}, rule.AssignTags...), Correspondent: rule.AssignCorrespondent, DocumentType: rule.AssignDocumentType, StoragePath: rule.AssignStoragePath, Source: "mail", Actor: "mail: " + a.Name}
	if !m.Date.IsZero() {
		base.Created = m.Date.Local().Format("2006-01-02")
	}
	if base.Correspondent == nil && rule.CorrespondentFrom != "nothing" && rule.CorrespondentFrom != "" {
		base.Correspondent = s.mailCorrespondent(ctx, uid, rule.CorrespondentFrom, m.From)
	}
	subject := strings.TrimSpace(m.Subject)
	take := func(name string, data []byte, title string) {
		ext := strings.ToLower(filepath.Ext(name))
		if ext == "" {
			ext = ".bin"
		}
		f, err := os.CreateTemp(s.cfg.TmpDir, "mail-*"+ext)
		if err != nil {
			errs = append(errs, err.Error())
			return
		}
		tmp := f.Name()
		_, werr := f.Write(data)
		f.Close()
		if werr != nil {
			os.Remove(tmp)
			errs = append(errs, werr.Error())
			return
		}
		in := base
		in.Path, in.Name, in.Title = tmp, name, title
		d, err := s.ingest(ctx, uid, in)
		if err != nil {
			var dup *ErrDuplicate
			if errors.As(err, &dup) {
				taken++
				s.log.Debug("mail: already in", "file", name, "document", dup.Existing.ID)
				return
			}
			os.Remove(tmp)
			errs = append(errs, name+": "+err.Error())
			return
		}
		taken++
		s.log.Info("mail: consumed", "account", a.Name, "rule", rule.Name, "file", name, "document", d.ID, "user", uid)
	}
	if rule.Consume == "attachments" || rule.Consume == "both" {
		for _, att := range m.Attachments {
			ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(att.Name)), ".")
			if !mail.GlobMatch(rule.FilterAttachment, att.Name) {
				continue
			}
			kind := extract.Kind(ext)
			if kind == "" {
				continue
			}
			if kind == "image" && len(att.Data) < mailTinyImage {
				continue
			}
			if s.cfg.MaxUploadBytes > 0 && int64(len(att.Data)) > s.cfg.MaxUploadBytes {
				errs = append(errs, fmt.Sprintf("%s: larger than %d MB", att.Name, s.cfg.MaxUploadBytes>>20))
				continue
			}
			title := ""
			if rule.TitleFrom == "subject" {
				title = subject
			}
			take(att.Name, att.Data, title)
		}
	}
	if rule.Consume == "eml" || rule.Consume == "both" {
		name := mailFileName(subject)
		if name == "" {
			name = fmt.Sprintf("message-%d", m.UID)
		}
		take(name+".eml", m.Raw, subject)
	}
	return taken, errs
}

// mailCorrespondent finds or makes the correspondent for a sender.
func (s *Server) mailCorrespondent(ctx contextT, uid int64, from string, addr mail.Address) *int64 {
	name := strings.TrimSpace(addr.Address)
	if from == "name" && strings.TrimSpace(addr.Name) != "" {
		name = strings.TrimSpace(addr.Name)
	}
	if name == "" {
		return nil
	}
	if c, err := s.store.FindCorrespondent(ctx, uid, name); err == nil {
		return &c.ID
	}
	c := &store.Named{UserID: uid, Name: name, Matchable: store.Matchable{MatchingAlgorithm: store.MatchAuto, IsInsensitive: true}}
	if err := s.store.CreateCorrespondent(ctx, c); err != nil {
		if c2, err2 := s.store.FindCorrespondent(ctx, uid, name); err2 == nil {
			return &c2.ID
		}
		s.log.Warn("mail: cannot create correspondent", "name", name, "err", err)
		return nil
	}
	return &c.ID
}

var mailUnsafeName = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]+`)

// mailFileName makes a file name stem out of a subject.
func mailFileName(subject string) string {
	n := strings.TrimSpace(mailUnsafeName.ReplaceAllString(subject, " "))
	n = strings.Join(strings.Fields(n), " ")
	if len(n) > 120 {
		n = n[:120]
	}
	return strings.Trim(n, ". ")
}
