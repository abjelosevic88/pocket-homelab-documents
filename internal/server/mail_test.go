package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-documents/internal/mail"
	"github.com/abjelosevic88/pocket-homelab-documents/internal/store"
)

// mailHarness serves /api/v1/mail/* from mountMail (inside an authenticated
// /api/v1 group of its own, independent of what server.go mounts) and
// everything else from the full router.
func mailHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	full := h.s.Router()
	mailR := chi.NewRouter()
	mailR.Route("/api/v1", func(r chi.Router) {
		r.Group(func(r chi.Router) {
			r.Use(h.s.requireAuth)
			h.s.mountMail(r)
		})
	})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/mail/") {
			mailR.ServeHTTP(w, r)
			return
		}
		full.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	h2 := *h
	h2.ts = ts
	return &h2
}

func TestMailAccountsAndRulesPerUser(t *testing.T) {
	h := mailHarness(t)
	_, aTok := h.user("alice@example.com")
	_, bTok := h.user("bob@example.com")

	// create: password stored but never returned
	var acct store.MailAccount
	if code := h.do(aTok, "POST", "/api/v1/mail/accounts", map[string]any{"name": "Work", "host": "imap.example.com", "security": "ssl", "username": "alice", "password": "s3cret", "poll_minutes": 5, "enabled": true}, &acct); code != 201 {
		t.Fatalf("create account: %d", code)
	}
	if acct.ID == 0 || acct.Port != 993 || !acct.PasswordSet || acct.Password != "" || acct.PollMinutes != 5 {
		t.Fatalf("account: %+v", acct)
	}
	var raw string
	h.do(aTok, "GET", "/api/v1/mail/accounts", nil, &raw)
	if strings.Contains(raw, "s3cret") || !strings.Contains(raw, `"password_set":true`) {
		t.Fatalf("password leaks or password_set missing: %s", raw)
	}
	// bad input
	if code := h.do(aTok, "POST", "/api/v1/mail/accounts", map[string]any{"name": "", "host": "x"}, nil); code != 400 {
		t.Errorf("nameless account: %d", code)
	}
	if code := h.do(aTok, "POST", "/api/v1/mail/accounts", map[string]any{"name": "x", "host": "x", "security": "tls"}, nil); code != 400 {
		t.Errorf("bad security: %d", code)
	}
	// update keeps the password when none is sent, or when "" is sent
	var upd store.MailAccount
	if code := h.do(aTok, "PUT", "/api/v1/mail/accounts/"+itoa(acct.ID), map[string]any{"name": "Work mail", "password": ""}, &upd); code != 200 {
		t.Fatalf("update: %d", code)
	}
	if upd.Name != "Work mail" || !upd.PasswordSet || upd.Host != "imap.example.com" {
		t.Fatalf("update result: %+v", upd)
	}
	stored, _ := h.st.GetMailAccount(context.Background(), upd.UserID, acct.ID)
	if stored == nil {
		stored, _ = h.st.GetMailAccount(context.Background(), 1, acct.ID)
	}
	if stored == nil || stored.Password != "s3cret" {
		t.Fatalf("password not kept: %+v", stored)
	}
	h.do(aTok, "PUT", "/api/v1/mail/accounts/"+itoa(acct.ID), map[string]any{"password": "new"}, &upd)
	stored, _ = h.st.GetMailAccount(context.Background(), stored.UserID, acct.ID)
	if stored.Password != "new" {
		t.Fatalf("password not replaced: %q", stored.Password)
	}

	// rules
	var tag store.Tag
	h.do(aTok, "POST", "/api/v1/tags", map[string]any{"name": "Bills"}, &tag)
	var corr store.Named
	h.do(aTok, "POST", "/api/v1/correspondents", map[string]any{"name": "Power"}, &corr)
	var bobTag store.Tag
	h.do(bTok, "POST", "/api/v1/tags", map[string]any{"name": "BobOnly"}, &bobTag)
	var rule store.MailRule
	body := map[string]any{"account_id": acct.ID, "name": "Bills", "filter_from": "epbih", "filter_attachment": "*.pdf", "consume": "both", "assign_tags": []int64{tag.ID, bobTag.ID, 999}, "assign_correspondent": corr.ID, "action": "move", "action_folder": "Archive", "max_age_days": 30}
	if code := h.do(aTok, "POST", "/api/v1/mail/rules", body, &rule); code != 201 {
		t.Fatalf("create rule: %d", code)
	}
	if rule.Folder != "INBOX" || rule.TitleFrom != "subject" || rule.CorrespondentFrom != "nothing" || !rule.Enabled || !rule.OnlyUnseen {
		t.Errorf("rule defaults: %+v", rule)
	}
	if len(rule.AssignTags) != 1 || rule.AssignTags[0] != tag.ID {
		t.Errorf("foreign tags not dropped: %v", rule.AssignTags)
	}
	if rule.AssignCorrespondent == nil || *rule.AssignCorrespondent != corr.ID {
		t.Errorf("correspondent: %v", rule.AssignCorrespondent)
	}
	// validation
	for _, bad := range []map[string]any{
		{"account_id": acct.ID, "name": "x", "consume": "all"},
		{"account_id": acct.ID, "name": "x", "action": "move"},
		{"account_id": acct.ID, "name": "x", "title_from": "body"},
		{"account_id": 999, "name": "x"},
		{"account_id": acct.ID, "name": ""},
	} {
		if code := h.do(aTok, "POST", "/api/v1/mail/rules", bad, nil); code != 400 {
			t.Errorf("bad rule %v: %d", bad, code)
		}
	}
	// bob cannot hang a rule on alice's account, nor use her correspondent
	if code := h.do(bTok, "POST", "/api/v1/mail/rules", map[string]any{"account_id": acct.ID, "name": "steal"}, nil); code != 400 {
		t.Errorf("bob rule on alice account: %d", code)
	}
	var bobAcct store.MailAccount
	h.do(bTok, "POST", "/api/v1/mail/accounts", map[string]any{"name": "Bob", "host": "mail.bob", "security": "starttls"}, &bobAcct)
	if bobAcct.Port != 143 {
		t.Errorf("starttls default port: %d", bobAcct.Port)
	}
	if code := h.do(bTok, "POST", "/api/v1/mail/rules", map[string]any{"account_id": bobAcct.ID, "name": "x", "assign_correspondent": corr.ID}, nil); code != 400 {
		t.Errorf("bob uses alice's correspondent: %d", code)
	}
	// update
	var rule2 store.MailRule
	if code := h.do(aTok, "PUT", "/api/v1/mail/rules/"+itoa(rule.ID), map[string]any{"name": "Bills v2", "action": "mark_read", "assign_correspondent": nil, "correspondent_from": "name"}, &rule2); code != 200 {
		t.Fatalf("update rule: %d", code)
	}
	if rule2.Name != "Bills v2" || rule2.Action != "mark_read" || rule2.AssignCorrespondent != nil || rule2.CorrespondentFrom != "name" || rule2.FilterFrom != "epbih" {
		t.Errorf("rule update: %+v", rule2)
	}

	// per-user: bob sees nothing of alice's and gets 404 touching it
	var accts []store.MailAccount
	h.do(bTok, "GET", "/api/v1/mail/accounts", nil, &accts)
	if len(accts) != 1 || accts[0].ID != bobAcct.ID {
		t.Errorf("bob lists alice's accounts: %+v", accts)
	}
	var rules []store.MailRule
	h.do(bTok, "GET", "/api/v1/mail/rules", nil, &rules)
	if len(rules) != 0 {
		t.Errorf("bob lists alice's rules: %+v", rules)
	}
	for _, c := range []struct{ method, path string }{
		{"PUT", "/api/v1/mail/accounts/" + itoa(acct.ID)}, {"DELETE", "/api/v1/mail/accounts/" + itoa(acct.ID)},
		{"POST", "/api/v1/mail/accounts/" + itoa(acct.ID) + "/test"}, {"POST", "/api/v1/mail/accounts/" + itoa(acct.ID) + "/fetch"},
		{"PUT", "/api/v1/mail/rules/" + itoa(rule.ID)}, {"DELETE", "/api/v1/mail/rules/" + itoa(rule.ID)},
	} {
		var body any
		if c.method == "PUT" {
			body = `{"name":"x"}`
		}
		if code := h.do(bTok, c.method, c.path, body, nil); code != 404 {
			t.Errorf("bob %s %s → %d, want 404", c.method, c.path, code)
		}
	}
	if code := h.do("", "GET", "/api/v1/mail/accounts", nil, nil); code != 401 {
		t.Errorf("anonymous: %d", code)
	}
	// deleting the account takes its rules along
	if code := h.do(aTok, "DELETE", "/api/v1/mail/accounts/"+itoa(acct.ID), nil, nil); code != 204 {
		t.Fatalf("delete account: %d", code)
	}
	h.do(aTok, "GET", "/api/v1/mail/rules", nil, &rules)
	if len(rules) != 0 {
		t.Errorf("rules survived the account: %+v", rules)
	}
}

func TestMailFetchUnreachable(t *testing.T) {
	h := mailHarness(t)
	_, tok := h.user("alice@example.com")
	var acct store.MailAccount
	h.do(tok, "POST", "/api/v1/mail/accounts", map[string]any{"name": "Dead", "host": "127.0.0.1", "port": 1, "security": "none", "username": "u", "password": "p"}, &acct)
	var out map[string]any
	// no rules: nothing to do, no connection made, the check is recorded
	if code := h.do(tok, "POST", "/api/v1/mail/accounts/"+itoa(acct.ID)+"/fetch", nil, &out); code != 200 {
		t.Fatalf("fetch: %d", code)
	}
	if out["consumed"].(float64) != 0 || len(out["errors"].([]any)) != 0 {
		t.Fatalf("fetch without rules: %v", out)
	}
	h.do(tok, "POST", "/api/v1/mail/rules", map[string]any{"account_id": acct.ID, "name": "r"}, nil)
	if code := h.do(tok, "POST", "/api/v1/mail/accounts/"+itoa(acct.ID)+"/fetch", nil, &out); code != 200 {
		t.Fatalf("fetch: %d", code)
	}
	if len(out["errors"].([]any)) != 1 {
		t.Fatalf("unreachable server: %v", out)
	}
	var accts []store.MailAccount
	h.do(tok, "GET", "/api/v1/mail/accounts", nil, &accts)
	if accts[0].LastCheckedAt == nil || accts[0].LastError == "" {
		t.Errorf("check not recorded: %+v", accts[0])
	}
	if code := h.do(tok, "POST", "/api/v1/mail/accounts/"+itoa(acct.ID)+"/test", nil, nil); code != 502 {
		t.Errorf("test unreachable: %d", code)
	}
	// the account is now not due again until poll_minutes pass
	due, _ := h.st.DueMailAccounts(context.Background(), time.Now())
	if len(due) != 0 {
		t.Errorf("just-checked account is due: %+v", due)
	}
	due, _ = h.st.DueMailAccounts(context.Background(), time.Now().Add(11*time.Minute))
	if len(due) != 1 {
		t.Errorf("account not due after the interval: %+v", due)
	}
}

const testMail = "From: Elektroprivreda <racuni@epbih.ba>\r\nTo: alice@example.com\r\nSubject: Racun za struju\r\nDate: Sat, 14 Mar 2026 10:15:00 +0100\r\nMessage-ID: <m1@epbih.ba>\r\n" +
	"Content-Type: multipart/mixed; boundary=\"b\"\r\n\r\n--b\r\nContent-Type: text/plain\r\n\r\nU prilogu je racun.\r\n" +
	"--b\r\nContent-Type: text/plain; name=\"racun.txt\"\r\nContent-Disposition: attachment; filename=\"racun.txt\"\r\n\r\nRacun broj 1187, iznos 58,40 EUR\r\n" +
	"--b\r\nContent-Type: application/octet-stream; name=\"virus.exe\"\r\nContent-Disposition: attachment; filename=\"virus.exe\"\r\n\r\nMZ\r\n" +
	"--b\r\nContent-Type: image/png; name=\"sig.png\"\r\nContent-Disposition: inline; filename=\"sig.png\"\r\n\r\ntiny\r\n--b--\r\n"

func TestMailConsume(t *testing.T) {
	h := mailHarness(t)
	u, tok := h.user("alice@example.com")
	var tag store.Tag
	h.do(tok, "POST", "/api/v1/tags", map[string]any{"name": "Bills"}, &tag)
	var acct store.MailAccount
	h.do(tok, "POST", "/api/v1/mail/accounts", map[string]any{"name": "Work", "host": "imap.example.com"}, &acct)
	var rule store.MailRule
	h.do(tok, "POST", "/api/v1/mail/rules", map[string]any{"account_id": acct.ID, "name": "Bills", "consume": "both", "assign_tags": []int64{tag.ID}, "correspondent_from": "name"}, &rule)
	m, err := mail.Parse([]byte(testMail))
	if err != nil {
		t.Fatal(err)
	}
	m.UID = 7
	a, _ := h.st.GetMailAccount(context.Background(), u.ID, acct.ID)
	r, _ := h.st.GetMailRule(context.Background(), u.ID, rule.ID)
	n, errs := h.s.consumeMail(context.Background(), a, r, m)
	if n != 2 || len(errs) != 0 {
		t.Fatalf("consumed %d, errs %v", n, errs)
	}
	h.drain()
	var l listOut
	h.do(tok, "GET", "/api/v1/documents", nil, &l)
	if l.Count != 2 {
		t.Fatalf("documents: %d", l.Count)
	}
	byName := map[string]store.Document{}
	for _, d := range l.Results {
		byName[d.OriginalName] = d
	}
	att, ok := byName["racun.txt"]
	if !ok {
		t.Fatalf("attachment document missing: %+v", l.Results)
	}
	eml, ok := byName["Racun za struju.eml"]
	if !ok {
		t.Fatalf("eml document missing: %+v", l.Results)
	}
	for _, d := range []store.Document{att, eml} {
		if d.Title != "Racun za struju" || d.Created != "2026-03-14" || d.Source != "mail" {
			t.Errorf("document %s: title %q created %s source %s", d.OriginalName, d.Title, d.Created, d.Source)
		}
		if d.CorrespondentID == nil {
			t.Errorf("document %s: no correspondent from the sender", d.OriginalName)
		} else if c, err := h.st.GetCorrespondent(context.Background(), u.ID, *d.CorrespondentID); err != nil || c.Name != "Elektroprivreda" {
			t.Errorf("document %s: correspondent %v %v", d.OriginalName, c, err)
		}
		has := false
		for _, x := range d.Tags {
			if x == tag.ID {
				has = true
			}
		}
		if !has {
			t.Errorf("document %s: tag not assigned: %v", d.OriginalName, d.Tags)
		}
	}
	content := func(id int64) string {
		var got struct {
			Document store.Document `json:"document"`
		}
		h.do(tok, "GET", "/api/v1/documents/"+itoa(id), nil, &got)
		return got.Document.Content
	}
	if !strings.Contains(content(eml.ID), "U prilogu je racun") || !strings.Contains(content(att.ID), "1187") {
		t.Errorf("content: eml %q att %q", content(eml.ID), content(att.ID))
	}
	// the same message again: duplicates count as taken, nothing new is made
	n, errs = h.s.consumeMail(context.Background(), a, r, m)
	if n != 2 || len(errs) != 0 {
		t.Errorf("second run: %d %v", n, errs)
	}
	h.do(tok, "GET", "/api/v1/documents", nil, &l)
	if l.Count != 2 {
		t.Errorf("duplicates made documents: %d", l.Count)
	}
	// title from filename, attachments only, glob that misses
	r.TitleFrom, r.Consume, r.FilterAttachment = "filename", "attachments", "*.pdf"
	if n, _ := h.s.consumeMail(context.Background(), a, r, m); n != 0 {
		t.Errorf("glob miss consumed %d", n)
	}
	r.FilterAttachment = "*.txt"
	m.Attachments[0].Data = []byte("another bill, 2026")
	if n, errs := h.s.consumeMail(context.Background(), a, r, m); n != 1 || len(errs) != 0 {
		t.Errorf("attachments only: %d %v", n, errs)
	}
	h.drain()
	h.do(tok, "GET", "/api/v1/documents?q=another", nil, &l)
	if l.Count != 1 || l.Results[0].Title != "racun" {
		t.Errorf("title from filename: %+v", l.Results)
	}
	// seen bookkeeping
	if h.st.MailSeen(context.Background(), a.ID, r.ID, 1, 7) {
		t.Error("seen before marking")
	}
	_ = h.st.MarkMailSeen(context.Background(), a.ID, r.ID, 1, 7, m.MessageID)
	if !h.st.MailSeen(context.Background(), a.ID, r.ID, 1, 7) {
		t.Error("not seen after marking")
	}
	seen, _ := h.st.MailSeenUIDs(context.Background(), a.ID, r.ID, 1)
	if !seen[7] || seen[8] {
		t.Errorf("seen set: %v", seen)
	}
	if h.st.MailSeen(context.Background(), a.ID, r.ID, 2, 7) {
		t.Error("another uidvalidity is seen")
	}
}

func TestMailFileName(t *testing.T) {
	for in, want := range map[string]string{
		"Račun: struja / mart 2026": "Račun struja mart 2026",
		"  spaces   everywhere  ":   "spaces everywhere",
		`a<b>c"d|e?f*g`:             "a b c d e f g",
		"...":                       "",
		strings.Repeat("x", 200):    strings.Repeat("x", 120),
	} {
		if got := mailFileName(in); got != want {
			t.Errorf("mailFileName(%q) = %q, want %q", in, got, want)
		}
	}
}
