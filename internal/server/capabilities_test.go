package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-documents/internal/config"
	"github.com/abjelosevic88/pocket-homelab-documents/internal/store"
)

// fakeCapCore is a Pocket Core that vouches for link tokens
// pcx_<alice|bob>_<search|day|widget|capture|read|write> from Pocket Hub and
// remembers the manifest Documents registers with.
type fakeCapCore struct {
	*httptest.Server
	mu       sync.Mutex
	manifest map[string]any
}

func capHarness(t *testing.T) (*harness, *fakeCapCore) {
	t.Helper()
	fc := &fakeCapCore{}
	scopes := map[string]string{"search": core.ScopeSearch, "day": core.ScopeDay, "widget": core.ScopeWidget, "capture": core.ScopeCapture,
		"read": "documents:read", "write": "documents:write"}
	fc.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/apps/register":
			var m map[string]any
			_ = json.NewDecoder(r.Body).Decode(&m)
			fc.mu.Lock()
			fc.manifest = m
			fc.mu.Unlock()
			io.WriteString(w, `{}`)
		case "/api/v1/auth/introspect":
			var in struct{ Token string }
			_ = json.NewDecoder(r.Body).Decode(&in)
			parts := strings.Split(in.Token, "_")
			if len(parts) != 3 || parts[0] != "pcx" || scopes[parts[2]] == "" {
				io.WriteString(w, `{"active":false}`)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"active": true, "kind": "delegated", "audience": "documents", "scope": scopes[parts[2]],
				"user": map[string]any{"id": 1, "sub": parts[1] + "@example.com", "email": parts[1] + "@example.com", "name": parts[1], "role": "member"},
				"app":  map[string]any{"id": 9, "slug": "hub", "name": "Pocket Hub"}})
		default:
			io.WriteString(w, `{}`)
		}
	}))
	t.Cleanup(fc.Close)
	return newHarness(t, func(c *config.Config) { c.CoreURL, c.CoreRegistrationKey = fc.URL, "key" }), fc
}

// ask calls a route and wants 200 with a JSON object.
func (h *harness) ask(tok, path string) map[string]any {
	h.t.Helper()
	var m map[string]any
	if code := h.do(tok, "GET", path, nil, &m); code != 200 {
		h.t.Fatalf("GET %s → %d %v", path, code, m)
	}
	return m
}

// capItems are the rows of a capability answer.
func capItems(t *testing.T, m map[string]any) []map[string]any {
	t.Helper()
	l, ok := m["items"].([]any)
	if !ok {
		t.Fatalf("no items list in %v", m)
	}
	out := []map[string]any{}
	for _, v := range l {
		out = append(out, v.(map[string]any))
	}
	return out
}

// capItem finds the row with that title.
func capItem(t *testing.T, m map[string]any, title string) map[string]any {
	t.Helper()
	for _, it := range capItems(t, m) {
		if it["title"] == title {
			return it
		}
	}
	t.Fatalf("no %q in %v", title, m["items"])
	return nil
}

// titles are the rows' titles, in order.
func titles(t *testing.T, m map[string]any) string {
	t.Helper()
	var out []string
	for _, it := range capItems(t, m) {
		out = append(out, it["title"].(string))
	}
	return strings.Join(out, " | ")
}

// file uploads a text document with a title, reads it and returns it.
func (h *harness) file(tok, title, text string, change map[string]any) *store.Document {
	h.t.Helper()
	d, code := h.upload(tok, strings.ReplaceAll(strings.ToLower(title), " ", "_")+".txt", text, map[string]string{"title": title})
	if code != 201 {
		h.t.Fatalf("upload %q → %d", title, code)
	}
	h.drain()
	if change != nil {
		if code := h.do(tok, "PUT", "/api/v1/documents/"+itoa(d.ID), change, nil); code != 200 {
			h.t.Fatalf("change %q → %d", title, code)
		}
	}
	return d
}

// set writes columns the API does not take (when a document came in, how).
func (h *harness) set(d *store.Document, col, val string) {
	h.t.Helper()
	if _, err := h.st.DB.Exec(`UPDATE documents SET `+col+` = ? WHERE id = ?`, val, d.ID); err != nil {
		h.t.Fatal(err)
	}
}

// named makes a correspondent or a document type.
func (h *harness) named(tok, path, name string) int64 {
	h.t.Helper()
	var n store.Named
	if code := h.do(tok, "POST", "/api/v1/"+path, map[string]any{"name": name}, &n); code != 201 && code != 200 {
		h.t.Fatalf("%s %q → %d", path, name, code)
	}
	return n.ID
}

func TestPocketSearch(t *testing.T) {
	h := newHarness(t)
	_, alice := h.user("alice@example.com")
	_, bob := h.user("bob@example.com")
	ikea, invoice := h.named(alice, "correspondents", "IKEA"), h.named(alice, "document_types", "Invoice")
	sofa := h.file(alice, "Sofa", "Thank you for buying the Ektorp three-seat sofa. Delivery within two weeks.",
		map[string]any{"correspondent": ikea, "document_type": invoice, "created": "2026-03-14"})
	lease := h.file(alice, "Flat lease", "The tenant pays the rent on the first of every month.", map[string]any{"created": "2025-01-01"})

	// words from the text: the part around them, without the marks the web app highlights with
	res := h.ask(alice, "/api/v1/pocket/search?q=ektorp")
	got := capItems(t, res)
	if len(got) != 1 || got[0]["id"] != itoa(sofa.ID) || got[0]["title"] != "Sofa" || got[0]["kind"] != "document" || got[0]["date"] != "2026-03-14" ||
		got[0]["url"] != "/documents/"+itoa(sofa.ID) {
		t.Fatalf("by text: %v", got)
	}
	if snip, _ := got[0]["snippet"].(string); !strings.Contains(snip, "buying the Ektorp three-seat sofa") || strings.ContainsAny(snip, "[]") {
		t.Fatalf("snippet around the words: %q", snip)
	}
	// found by its title or by who it is from: who, what, and how the text starts
	if it := capItem(t, h.ask(alice, "/api/v1/pocket/search?q=lease"), "Flat lease"); it["snippet"] != "The tenant pays the rent on the first of every month." || it["date"] != "2025-01-01" {
		t.Fatalf("by title: %v", it)
	}
	if it := capItem(t, h.ask(alice, "/api/v1/pocket/search?q=ikea"), "Sofa"); !strings.HasPrefix(it["snippet"].(string), "IKEA · Invoice · Thank you for buying") {
		t.Fatalf("by correspondent: %v", it)
	}
	// part of a word, when whole words find nothing
	if got := capItems(t, h.ask(alice, "/api/v1/pocket/search?q=ktorp")); len(got) != 1 || got[0]["title"] != "Sofa" || strings.ContainsAny(got[0]["snippet"].(string), "[]") {
		t.Fatalf("by part of a word: %v", got)
	}
	// the page's filters work, alone and with words
	if got := capItems(t, h.ask(alice, "/api/v1/pocket/search?q=type:invoice")); len(got) != 1 || got[0]["title"] != "Sofa" || got[0]["snippet"] != "IKEA · Invoice" {
		t.Fatalf("by type: %v", got)
	}
	if got := capItems(t, h.ask(alice, "/api/v1/pocket/search?q=the+from:ikea")); len(got) != 1 || got[0]["title"] != "Sofa" {
		t.Fatalf("words and a filter: %v", got)
	}
	// the best match first, and no more than asked
	both := h.ask(alice, "/api/v1/pocket/search?q=the")
	if len(capItems(t, both)) != 2 {
		t.Fatalf("both documents have the word: %v", both)
	}
	if got := capItems(t, h.ask(alice, "/api/v1/pocket/search?q=the&limit=1")); len(got) != 1 || got[0]["title"] != capItems(t, both)[0]["title"] {
		t.Fatalf("limit 1: %v", got)
	}
	// a filter of the list page in the address changes nothing
	if got := capItems(t, h.ask(alice, "/api/v1/pocket/search?q=ektorp&trash=1&document_type=999")); len(got) != 1 {
		t.Fatalf("with list filters in the address: %v", got)
	}
	// nothing to look up never lists everything
	for _, q := range []string{"", "*", "%22", "-sofa", "()"} {
		if got := capItems(t, h.ask(alice, "/api/v1/pocket/search?q="+q)); len(got) != 0 {
			t.Fatalf("q=%q: %v", q, got)
		}
	}
	// a document in the trash is not found
	h.do(alice, "DELETE", "/api/v1/documents/"+itoa(lease.ID), nil, nil)
	if got := capItems(t, h.ask(alice, "/api/v1/pocket/search?q=lease")); len(got) != 0 {
		t.Fatalf("from the trash: %v", got)
	}
	// another person's documents never appear
	for _, q := range []string{"ektorp", "sofa", "ikea", "type:invoice", "the"} {
		if got := capItems(t, h.ask(bob, "/api/v1/pocket/search?q="+q)); len(got) != 0 {
			t.Fatalf("bob finds alice's %q: %v", q, got)
		}
	}
	if code := h.do("", "GET", "/api/v1/pocket/search?q=ektorp", nil, nil); code != 401 {
		t.Fatalf("nobody → %d", code)
	}
	// the list itself answers as before, marks included
	var l listOut
	h.do(alice, "GET", "/api/v1/documents?q=ektorp", nil, &l)
	if l.Count != 1 || !strings.Contains(l.Results[0].Snippet, "[[[Ektorp]]]") {
		t.Fatalf("/documents changed: %+v", l)
	}
}

func TestPocketDay(t *testing.T) {
	h := newHarness(t)
	_, alice := h.user("alice@example.com")
	_, bob := h.user("bob@example.com")
	power, invoice := h.named(alice, "correspondents", "Elektro"), h.named(alice, "document_types", "Invoice")
	// added and dated the same day
	bill := h.file(alice, "Electricity bill", "Power used in February.", map[string]any{"correspondent": power, "document_type": invoice, "created": "2026-03-10"})
	h.set(bill, "added_at", "2026-03-10T12:00:00.250Z")
	// late in the evening in UTC, already the next day in Sarajevo
	lease := h.file(alice, "Flat lease", "The tenant pays the rent.", map[string]any{"created": "2025-01-01"})
	h.set(lease, "added_at", "2026-03-10T23:30:00Z")
	// added long before, dated that day
	letter := h.file(alice, "Old letter", "Dear tenant.", map[string]any{"created": "2026-03-10"})
	h.set(letter, "added_at", "2026-02-01T10:00:00Z")
	// brought over from Paperless that day: not added that day, but dated it
	moved := h.file(alice, "From Paperless", "A scan from before.", map[string]any{"created": "2026-03-10"})
	h.set(moved, "added_at", "2026-03-10T09:00:00Z")
	h.set(moved, "source", "paperless")
	// restored from an export that day, dated another: no part of the day
	restored := h.file(alice, "Restored", "From a backup.", map[string]any{"created": "2020-01-01"})
	h.set(restored, "added_at", "2026-03-10T10:00:00Z")
	h.set(restored, "source", "import")
	// in the trash: gone from the day
	binned := h.file(alice, "Thrown away", "Not needed.", map[string]any{"created": "2026-03-10"})
	h.set(binned, "added_at", "2026-03-10T11:00:00Z")
	h.do(alice, "DELETE", "/api/v1/documents/"+itoa(binned.ID), nil, nil)

	day := h.ask(alice, "/api/v1/pocket/day/2026-03-10?tz=Europe/Sarajevo")
	if day["date"] != "2026-03-10" || day["summary"] != "1 document added, 2 dated that day" || titles(t, day) != "Electricity bill | From Paperless | Old letter" {
		t.Fatalf("the day in Sarajevo: %v", day)
	}
	if it := capItem(t, day, "Electricity bill"); it["id"] != itoa(bill.ID) || it["kind"] != "added" || it["time"] != "13:00" || it["sub"] != "Elektro · Invoice" ||
		it["url"] != "/documents/"+itoa(bill.ID) {
		t.Fatalf("added and dated shows once, as added: %v", it)
	}
	if it := capItem(t, day, "Old letter"); it["kind"] != "dated" || it["time"] != nil || it["sub"] != nil || it["url"] != "/documents/"+itoa(letter.ID) {
		t.Fatalf("dated: %v", it)
	}
	if it := capItem(t, day, "From Paperless"); it["kind"] != "dated" || it["time"] != nil {
		t.Fatalf("brought over: %v", it)
	}
	next := h.ask(alice, "/api/v1/pocket/day/2026-03-11?tz=Europe/Sarajevo")
	if next["summary"] != "1 document added" || titles(t, next) != "Flat lease" || capItems(t, next)[0]["time"] != "00:30" || capItems(t, next)[0]["kind"] != "added" {
		t.Fatalf("the next day in Sarajevo: %v", next)
	}
	// the same documents by another clock
	utc := h.ask(alice, "/api/v1/pocket/day/2026-03-10?tz=UTC")
	if utc["summary"] != "2 documents added, 2 dated that day" || titles(t, utc) != "Electricity bill | Flat lease | From Paperless | Old letter" ||
		capItem(t, utc, "Flat lease")["time"] != "23:30" {
		t.Fatalf("the day in UTC: %v", utc)
	}
	if d := h.ask(alice, "/api/v1/pocket/day/2026-03-11?tz=UTC"); d["summary"] != nil || len(capItems(t, d)) != 0 {
		t.Fatalf("the next day in UTC: %v", d)
	}
	// no zone said: the server's own; noon in UTC is that day almost everywhere
	capItem(t, h.ask(alice, "/api/v1/pocket/day/2026-03-10"), "Electricity bill")
	// only dated
	if d := h.ask(alice, "/api/v1/pocket/day/2025-01-01?tz=UTC"); d["summary"] != "1 document dated that day" || titles(t, d) != "Flat lease" || capItems(t, d)[0]["kind"] != "dated" {
		t.Fatalf("a day with one dated document: %v", d)
	}
	h.do(alice, "PUT", "/api/v1/documents/"+itoa(letter.ID), map[string]any{"created": "2025-01-01"}, nil)
	if d := h.ask(alice, "/api/v1/pocket/day/2025-01-01?tz=UTC"); d["summary"] != "2 documents dated that day" {
		t.Fatalf("a day with two dated documents: %v", d)
	}
	// a day with nothing, somebody else's day, a day that is none
	if d := h.ask(alice, "/api/v1/pocket/day/2019-03-04"); d["summary"] != nil || len(capItems(t, d)) != 0 {
		t.Fatalf("an empty day: %v", d)
	}
	if d := h.ask(bob, "/api/v1/pocket/day/2026-03-10?tz=Europe/Sarajevo"); d["summary"] != nil || len(capItems(t, d)) != 0 {
		t.Fatalf("bob sees alice's day: %v", d)
	}
	if code := h.do(alice, "GET", "/api/v1/pocket/day/yesterday", nil, nil); code != 400 {
		t.Fatalf("a day that is none → %d", code)
	}
}

func TestPocketWidget(t *testing.T) {
	h := newHarness(t)
	_, alice := h.user("alice@example.com")
	_, bob := h.user("bob@example.com")
	// no documents: nothing to show
	if w := h.ask(bob, "/api/v1/pocket/widget"); w["line"] != nil || w["metrics"] != nil || w["url"] != nil || len(capItems(t, w)) != 0 {
		t.Fatalf("without documents: %v", w)
	}
	post, power := h.named(alice, "correspondents", "Post office"), h.named(alice, "correspondents", "Elektro")
	now := time.Now().UTC()
	at := func(ago time.Duration) string { return now.Add(-ago).Format(time.RFC3339Nano) }
	// every upload lands in the inbox
	bill := h.file(alice, "Electricity bill", "Power used in February.", map[string]any{"correspondent": power})
	h.set(bill, "added_at", at(3*time.Hour))
	lease := h.file(alice, "Flat lease", "The tenant pays the rent.", nil)
	h.set(lease, "added_at", at(2*time.Hour))
	letter := h.file(alice, "Letter", "Dear tenant.", map[string]any{"correspondent": post, "remove_inbox_tags": true})
	h.set(letter, "added_at", at(time.Hour))
	old := h.file(alice, "Old contract", "Signed long ago.", map[string]any{"correspondent": post})
	h.set(old, "added_at", at(30*24*time.Hour))
	if err := h.st.SetStatus(context.Background(), lease.ID, "failed", "the file is damaged"); err != nil {
		t.Fatal(err)
	}

	metrics := func(w map[string]any) string {
		var out []string
		for _, m := range w["metrics"].([]any) {
			out = append(out, m.(map[string]any)["label"].(string)+" "+m.(map[string]any)["value"].(string))
		}
		return strings.Join(out, " | ")
	}
	w := h.ask(alice, "/api/v1/pocket/widget")
	if w["line"] != "3 in the inbox, 1 could not be read" || w["url"] != "/documents?inbox=1" || metrics(w) != "Inbox 3 | Untagged 1 | Added this week 3 | Documents 4" {
		t.Fatalf("widget: %v", w)
	}
	// what needs a look first, then the inbox, then the latest; each document once
	rows := capItems(t, w)
	if titles(t, w) != "Flat lease | Electricity bill | Old contract | Letter" || rows[0]["detail"] != "Could not be read" || rows[0]["id"] != itoa(lease.ID) ||
		rows[0]["url"] != "/documents/"+itoa(lease.ID) || rows[1]["detail"] != "In the inbox · Elektro" || rows[2]["detail"] != "In the inbox · Post office" || rows[3]["detail"] != "Post office" {
		t.Fatalf("rows: %v", rows)
	}
	// the same numbers as the dashboard
	var dash map[string]any
	h.do(alice, "GET", "/api/v1/dashboard", nil, &dash)
	if dash["inbox"].(float64) != 3 || dash["untagged"].(float64) != 1 || dash["added_week"].(float64) != 3 || dash["documents"].(float64) != 4 || dash["failed"].(float64) != 1 {
		t.Fatalf("the dashboard counts differently: inbox %v untagged %v week %v documents %v failed %v", dash["inbox"], dash["untagged"], dash["added_week"], dash["documents"], dash["failed"])
	}

	// the inbox emptied: only what could not be read waits
	for _, d := range []*store.Document{bill, lease, old} {
		h.do(alice, "PUT", "/api/v1/documents/"+itoa(d.ID), map[string]any{"remove_inbox_tags": true}, nil)
	}
	if w := h.ask(alice, "/api/v1/pocket/widget"); w["line"] != "1 could not be read" || w["url"] != "/documents?status=failed" ||
		titles(t, w) != "Flat lease | Letter | Electricity bill | Old contract" {
		t.Fatalf("an empty inbox: %v", w)
	}
	// nothing waits: no line, the latest documents
	if err := h.st.SetStatus(context.Background(), lease.ID, "done", ""); err != nil {
		t.Fatal(err)
	}
	w = h.ask(alice, "/api/v1/pocket/widget")
	if w["line"] != nil || w["url"] != "/" || titles(t, w) != "Letter | Flat lease | Electricity bill | Old contract" || metrics(w) != "Inbox 0 | Untagged 4 | Added this week 3 | Documents 4" {
		t.Fatalf("nothing waits: %v", w)
	}
	if d := capItems(t, w)[1]["detail"].(string); !strings.HasPrefix(d, "Added ") {
		t.Fatalf("a document from nobody says when it came: %q", d)
	}

	// someone who keeps no inbox gets no inbox count; never more than eight rows
	var tags []store.Tag
	h.do(bob, "GET", "/api/v1/tags", nil, &tags)
	for _, tag := range tags {
		h.do(bob, "DELETE", "/api/v1/tags/"+itoa(tag.ID), nil, nil)
	}
	for _, name := range []string{"One", "Two", "Three", "Four", "Five", "Six", "Seven", "Eight", "Nine"} {
		h.file(bob, name, "Page "+name+".", nil)
	}
	w = h.ask(bob, "/api/v1/pocket/widget")
	if w["line"] != nil || metrics(w) != "Untagged 9 | Added this week 9 | Documents 9" || len(capItems(t, w)) != 8 {
		t.Fatalf("bob's widget: %v", w)
	}
	for _, it := range capItems(t, w) {
		if strings.Contains("Electricity bill Flat lease Letter Old contract", it["title"].(string)) {
			t.Fatalf("bob sees alice's document: %v", it)
		}
	}
}

func TestAddedWords(t *testing.T) {
	loc := time.FixedZone("CET", 3600)
	now := time.Date(2026, 11, 27, 9, 0, 0, 0, loc)
	for stamp, want := range map[string]string{
		"2026-11-27T07:59:00Z":     "Added today",
		"2026-11-26T23:30:00.123Z": "Added today", // half past midnight here
		"2026-11-26T12:00:00Z":     "Added yesterday",
		"2026-11-12T12:00:00Z":     "Added 12 Nov",
		"2025-11-12T12:00:00Z":     "Added 12 Nov 2025",
		"sometime":                 "",
	} {
		if got := addedWords(stamp, now); got != want {
			t.Errorf("%s → %q, want %q", stamp, got, want)
		}
	}
}

// A link token with one pocket:* scope reaches its one route and nothing else.
func TestPocketLinkTokens(t *testing.T) {
	h, _ := capHarness(t)
	_, alice := h.user("alice@example.com")
	h.user("bob@example.com")
	sofa := h.file(alice, "Sofa", "Thank you for buying the Ektorp sofa.", map[string]any{"created": "2026-03-14"})
	id := itoa(sofa.ID)
	const day = "/api/v1/pocket/day/2026-03-14"
	for _, c := range []struct {
		tok, method, path string
		body              any
		want              int
	}{
		{"pcx_alice_search", "GET", "/api/v1/pocket/search?q=ektorp", nil, 200},
		{"pcx_alice_day", "GET", day, nil, 200},
		{"pcx_alice_widget", "GET", "/api/v1/pocket/widget", nil, 200},
		{"pcx_alice_widget", "GET", "/api/v1/auth/me", nil, 200},
		// another capability's route
		{"pcx_alice_search", "GET", day, nil, 403},
		{"pcx_alice_search", "GET", "/api/v1/pocket/widget", nil, 403},
		{"pcx_alice_day", "GET", "/api/v1/pocket/search?q=ektorp", nil, 403},
		{"pcx_alice_day", "GET", "/api/v1/pocket/widget", nil, 403},
		{"pcx_alice_widget", "GET", "/api/v1/pocket/search?q=ektorp", nil, 403},
		{"pcx_alice_widget", "GET", day, nil, 403},
		// Documents keeps no quick lines: the scope opens nothing
		{"pcx_alice_capture", "GET", "/api/v1/pocket/search?q=ektorp", nil, 403},
		{"pcx_alice_capture", "GET", "/api/v1/pocket/widget", nil, 403},
		{"pcx_alice_capture", "GET", "/api/v1/documents", nil, 403},
		// the rest of the app
		{"pcx_alice_search", "GET", "/api/v1/documents?q=ektorp", nil, 403},
		{"pcx_alice_search", "GET", "/api/v1/documents/" + id, nil, 403},
		{"pcx_alice_search", "GET", "/api/v1/documents/" + id + "/text", nil, 403},
		{"pcx_alice_search", "GET", "/api/v1/documents/" + id + "/download", nil, 403},
		{"pcx_alice_day", "GET", "/api/v1/documents?added=today", nil, 403},
		{"pcx_alice_day", "GET", "/api/v1/changes", nil, 403},
		{"pcx_alice_widget", "GET", "/api/v1/dashboard", nil, 403},
		{"pcx_alice_widget", "GET", "/api/v1/statistics", nil, 403},
		{"pcx_alice_widget", "GET", "/api/v1/tags", nil, 403},
		{"pcx_alice_widget", "GET", "/api/v1/settings", nil, 403},
		{"pcx_alice_widget", "GET", "/api/v1/export", nil, 403},
		{"pcx_alice_widget", "PUT", "/api/v1/documents/" + id, map[string]any{"title": "x"}, 403},
		{"pcx_alice_widget", "DELETE", "/api/v1/documents/" + id, nil, 403},
		{"pcx_alice_search", "POST", "/api/v1/documents/" + id + "/notes", map[string]any{"text": "x"}, 403},
		// the app's own scopes do not open the new routes
		{"pcx_alice_read", "GET", "/api/v1/pocket/search?q=ektorp", nil, 403},
		{"pcx_alice_read", "GET", day, nil, 403},
		{"pcx_alice_read", "GET", "/api/v1/pocket/widget", nil, 403},
		{"pcx_alice_write", "GET", "/api/v1/pocket/widget", nil, 403},
		{"pcx_alice_read", "GET", "/api/v1/documents?q=ektorp", nil, 200},
		{"pcx_alice_read", "GET", "/api/v1/dashboard", nil, 200},
		{"pcx_nobody", "GET", "/api/v1/pocket/widget", nil, 401},
	} {
		if code := h.do(c.tok, c.method, c.path, c.body, nil); code != c.want {
			t.Errorf("%s %s %s → %d, want %d", c.tok, c.method, c.path, code, c.want)
		}
	}
	// there is no capture route, for a link or for the person
	for _, tok := range []string{"pcx_alice_capture", alice} {
		if code := h.do(tok, "POST", "/api/v1/pocket/capture", map[string]any{"request_id": "req-0000000000000001", "text": "A line"}, nil); code != 404 {
			t.Errorf("capture as %s → %d, want 404", tok, code)
		}
	}
	var l listOut
	if h.do(alice, "GET", "/api/v1/documents", nil, &l); l.Count != 1 || l.Results[0].Title != "Sofa" {
		t.Fatalf("something was changed or kept: %+v", l.Results)
	}
	// the token acts for its person only
	if got := capItems(t, h.ask("pcx_alice_search", "/api/v1/pocket/search?q=ektorp")); len(got) != 1 || got[0]["title"] != "Sofa" {
		t.Fatalf("alice through the link: %v", got)
	}
	if got := capItems(t, h.ask("pcx_bob_search", "/api/v1/pocket/search?q=ektorp")); len(got) != 0 {
		t.Fatalf("bob through the link finds alice's document: %v", got)
	}
	if d := h.ask("pcx_alice_day", day+"?tz=UTC"); titles(t, d) != "Sofa" {
		t.Fatalf("alice's day through the link: %v", d)
	}
	if d := h.ask("pcx_bob_day", day+"?tz=UTC"); len(capItems(t, d)) != 0 {
		t.Fatalf("bob's day through the link: %v", d)
	}
	if w := h.ask("pcx_alice_widget", "/api/v1/pocket/widget"); w["line"] != "1 in the inbox" || titles(t, w) != "Sofa" {
		t.Fatalf("alice's widget through the link: %v", w)
	}
	if w := h.ask("pcx_bob_widget", "/api/v1/pocket/widget"); len(capItems(t, w)) != 0 || w["line"] != nil {
		t.Fatalf("bob's widget through the link: %v", w)
	}
}

func TestPocketLinkScopes(t *testing.T) {
	req := func(m, p string) *http.Request { return httptest.NewRequest(m, p, nil) }
	for _, c := range []struct {
		scope string
		r     *http.Request
		want  bool
	}{
		{core.ScopeSearch, req("GET", "/api/v1/pocket/search?q=x"), true},
		{core.ScopeDay, req("GET", "/api/v1/pocket/day/2026-01-02"), true},
		{core.ScopeWidget, req("GET", "/api/v1/pocket/widget"), true},
		{core.ScopeWidget, req("GET", "/api/v1/auth/me"), true},
		{core.ScopeSearch, req("POST", "/api/v1/pocket/search"), false},
		{core.ScopeDay, req("GET", "/api/v1/pocket/day/today"), false},
		{core.ScopeDay, req("GET", "/api/v1/documents"), false},
		{core.ScopeWidget, req("GET", "/api/v1/dashboard"), false},
		{core.ScopeSearch, req("GET", "/api/v1/documents/1/text"), false},
		// not declared, so the scope opens nothing
		{core.ScopeCapture, req("POST", "/api/v1/pocket/capture"), false},
		{core.ScopeCapture, req("POST", "/api/v1/documents"), false},
		{"documents:read", req("GET", "/api/v1/pocket/search?q=x"), false},
		{"documents:read", req("GET", "/api/v1/pocket/widget"), false},
		{"documents:write", req("POST", "/api/v1/pocket/capture"), false},
		{"documents:read", req("GET", "/api/v1/documents"), true},
	} {
		if got := linkAllows([]string{c.scope}, c.r); got != c.want {
			t.Errorf("%s %s %s = %v, want %v", c.scope, c.r.Method, c.r.URL.Path, got, c.want)
		}
	}
}

// Documents tells Core what it can do, so Search, Day and Hub find it on their own.
func TestManifestCapabilities(t *testing.T) {
	h, fc := capHarness(t)
	if err := h.s.registerWithCore(context.Background()); err != nil {
		t.Fatal(err)
	}
	fc.mu.Lock()
	m := fc.manifest
	fc.mu.Unlock()
	if m["slug"] != "documents" || m["category"] != "Files" {
		t.Fatalf("manifest: %v %v", m["slug"], m["category"])
	}
	search, _ := m["search"].(map[string]any)
	day, _ := m["day"].(map[string]any)
	widget, _ := m["widget"].(map[string]any)
	if search == nil || day == nil || widget == nil || m["capture"] != nil {
		t.Fatalf("capabilities: %v %v %v %v", search, day, widget, m["capture"])
	}
	if b := search["bangs"].([]any); len(b) != 2 || b[0] != "docs" || b[1] != "documents" || search["kinds"].([]any)[0] != "document" {
		t.Fatalf("search: %v", search)
	}
	if day["label"] != "Documents" || day["one"] != "document" || day["many"] != "documents" || day["unit"] != "count" || widget["title"] != "Documents" {
		t.Fatalf("day %v, widget %v", day, widget)
	}
	offered := map[string]bool{}
	for _, o := range m["offers"].([]any) {
		offered[o.(map[string]any)["scope"].(string)] = true
	}
	for _, sc := range []string{"documents:read", "documents:write", core.ScopeSearch, core.ScopeDay, core.ScopeWidget, core.ScopeMoveExport, core.ScopeMoveImport} {
		if !offered[sc] {
			t.Errorf("%s is not offered", sc)
		}
	}
	if offered[core.ScopeCapture] {
		t.Errorf("%s is offered without a capture", core.ScopeCapture)
	}
}
