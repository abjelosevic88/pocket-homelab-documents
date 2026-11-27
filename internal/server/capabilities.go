package server

import (
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-documents/internal/search"
	"github.com/abjelosevic88/pocket-homelab-documents/internal/store"
)

// What Pocket Search, Pocket Day and Pocket Hub ask Documents (the
// capabilities value in links.go): one route each, in the shape every Pocket
// app answers in. They read what the Documents page, its filters and the
// dashboard read, as the request's person, and change nothing.

// docKey is a document's id as the answers carry it.
func docKey(d store.Document) string { return strconv.FormatInt(d.ID, 10) }

// docURL opens a document in the app.
func docURL(d store.Document) string { return "/documents/" + docKey(d) }

func docTitle(d store.Document) string {
	return firstNonEmpty(strings.TrimSpace(d.Title), d.OriginalName)
}

// dots joins the parts of a line that are there: "IKEA · Invoice".
func dots(parts ...string) string {
	out := parts[:0:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " · ")
}

// docFrom says who a document is from and what it is.
func docFrom(d store.Document) string { return dots(d.CorrespondentName, d.DocumentTypeName) }

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// ---------- search ----------

// snippetMarks are what a search puts around the words it found, for the web
// app to highlight; a line of plain text goes without them.
var snippetMarks = strings.NewReplacer("[[[", "", "]]]", "")

// pocketSearch is the search box of the Documents page (GET /documents?q=)
// with nothing but the words: its filters (tag:, from:, type:, created:…)
// work here too, and the best match comes first, as on the page.
func (s *Server) pocketSearch(r *http.Request, q string, limit int) ([]core.SearchItem, error) {
	// only what was asked: no filter of the list page comes in through the address
	r2 := r.Clone(r.Context())
	r2.URL.RawQuery = url.Values{"q": {q}, "sort": {"rank"}, "limit": {strconv.Itoa(limit)}, "no_facets": {"1"}}.Encode()
	f, p := s.listFilter(r2)
	if f.FTS == "" {
		// nothing to look up (only signs, or only words to leave out) and no
		// filter either: the page would list every document, Search gets none
		p.Text, p.Unknown = "", nil
		if reflect.DeepEqual(p, search.Parsed{Fields: map[string]string{}}) {
			return nil, nil
		}
	}
	docs, _, _, err := s.store.ListDocuments(r.Context(), f)
	if err != nil {
		return nil, err
	}
	out := make([]core.SearchItem, 0, len(docs))
	for _, d := range docs {
		it := core.SearchItem{ID: docKey(d), Title: docTitle(d), Kind: "document", Date: d.Created, URL: docURL(d), Score: d.Rank}
		if strings.Contains(d.Snippet, "[[[") {
			// the part of the text around the words
			it.Snippet = snippetMarks.Replace(d.Snippet)
		} else {
			// found by its title, a name or a note: who it is from, what it is, how it starts
			it.Snippet = dots(docFrom(d), d.Snippet)
		}
		out = append(out, it)
	}
	return out, nil
}

// ---------- day ----------

// dayPage is how many documents one look at a day's additions takes.
const dayPage = 500

// broughtOver reports a document that came with a move from Paperless or from
// an export: it arrived that day, but nobody filed it that day (Pocket Day's
// own Documents source leaves these out the same way).
func broughtOver(d store.Document) bool { return d.Source == "paperless" || d.Source == "import" }

// pocketDay says what happened with the person's documents on one day: the
// ones added (scanned, mailed in, uploaded) with the time they came in, and
// the ones whose own date is that day. A document that is both shows once,
// as added. The day is the caller's time zone's when it said one, else the
// server's.
func (s *Server) pocketDay(r *http.Request, date string, tz *time.Location) (*core.Day, error) {
	ctx, uid := r.Context(), me(r)
	loc := time.Local
	if tz != nil {
		loc = tz
	}
	from, err := time.ParseInLocation("2006-01-02", date, loc)
	if err != nil {
		return nil, nil
	}
	to := from.AddDate(0, 0, 1)

	// added_at is kept in UTC and the filter's upper end takes whole UTC
	// dates: ask up to the end of the last one the local day touches and keep
	// the local day below. The lower end has no zone letter, so "…:00Z" and
	// "…:00.123Z" both sort after it.
	f := store.DocFilter{UserID: uid, AddedFrom: from.UTC().Format("2006-01-02T15:04:05"), AddedTo: to.Add(-time.Nanosecond).UTC().Format("2006-01-02"),
		Sort: "added", Limit: dayPage, NoFacets: true}
	day := &core.Day{Date: date}
	seen := map[int64]bool{}
	nAdded, both := 0, 0
	for page := 0; page < 20; page++ {
		f.Offset = page * dayPage
		docs, total, _, err := s.store.ListDocuments(ctx, f)
		if err != nil {
			return nil, err
		}
		for _, d := range docs {
			at, err := time.Parse(time.RFC3339Nano, d.AddedAt)
			if err != nil || at.Before(from) || !at.Before(to) || broughtOver(d) {
				continue
			}
			nAdded++
			if d.Created == date {
				both++
			}
			seen[d.ID] = true
			day.Items = append(day.Items, core.DayItem{ID: docKey(d), Time: at.In(loc).Format("15:04"), Title: docTitle(d), Sub: docFrom(d), Kind: "added", URL: docURL(d)})
		}
		if len(docs) < dayPage || f.Offset+len(docs) >= total {
			break
		}
	}
	dated, nDated, _, err := s.store.ListDocuments(ctx, store.DocFilter{UserID: uid, CreatedFrom: date, CreatedTo: date, Sort: "title", Limit: 200, NoFacets: true})
	if err != nil {
		return nil, err
	}
	nDated -= both
	for _, d := range dated {
		if !seen[d.ID] {
			day.Items = append(day.Items, core.DayItem{ID: docKey(d), Title: docTitle(d), Sub: docFrom(d), Kind: "dated", URL: docURL(d)})
		}
	}
	if len(day.Items) == 0 {
		return nil, nil
	}
	switch {
	case nAdded > 0 && nDated > 0:
		day.Summary = fmt.Sprintf("%s, %d dated that day", plural(nAdded, "document added", "documents added"), nDated)
	case nAdded > 0:
		day.Summary = plural(nAdded, "document added", "documents added")
	case nDated > 0:
		day.Summary = plural(nDated, "document", "documents") + " dated that day"
	}
	return day, nil
}

// ---------- widget ----------

// addedWords says when a document came in: "Added today", "Added 12 Nov".
func addedWords(stamp string, now time.Time) string {
	at, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		return ""
	}
	at = at.In(now.Location())
	switch day := at.Format("2006-01-02"); {
	case day == now.Format("2006-01-02"):
		return "Added today"
	case day == now.AddDate(0, 0, -1).Format("2006-01-02"):
		return "Added yesterday"
	case at.Year() == now.Year():
		return "Added " + at.Format("2 Jan")
	}
	return "Added " + at.Format("2 Jan 2006")
}

// pocketWidget is the dashboard in a few lines: the documents that could not
// be read and the ones waiting in the inbox first, then the ones added last,
// with the dashboard's counts. The counts come from the list's own filters
// (what the dashboard's tiles open), not from the statistics, which read
// every document's text. Someone without documents gets nothing to show.
func (s *Server) pocketWidget(r *http.Request) (*core.WidgetData, error) {
	ctx, uid := r.Context(), me(r)
	const rows = 8
	list := func(f store.DocFilter) ([]store.Document, int, error) {
		f.UserID, f.Sort, f.Limit, f.NoFacets = uid, "-added", rows, true
		docs, n, _, err := s.store.ListDocuments(ctx, f)
		return docs, n, err
	}
	recent, total, err := list(store.DocFilter{})
	if err != nil || total == 0 {
		return nil, err
	}
	failed, nFailed, err := list(store.DocFilter{Status: "failed"})
	if err != nil {
		return nil, err
	}
	inbox, nInbox, err := list(store.DocFilter{Inbox: true})
	if err != nil {
		return nil, err
	}
	untagged, err := s.store.CountDocuments(ctx, store.DocFilter{UserID: uid, Untagged: true})
	if err != nil {
		return nil, err
	}
	now := time.Now()
	week, err := s.store.CountDocuments(ctx, store.DocFilter{UserID: uid, AddedFrom: now.AddDate(0, 0, -7).UTC().Format(time.RFC3339)})
	if err != nil {
		return nil, err
	}

	w := &core.WidgetData{URL: "/"}
	var parts []string
	if nInbox > 0 {
		parts = append(parts, strconv.Itoa(nInbox)+" in the inbox")
		w.URL = "/documents?inbox=1"
	}
	if nFailed > 0 {
		parts = append(parts, strconv.Itoa(nFailed)+" could not be read")
		if nInbox == 0 {
			w.URL = "/documents?status=failed"
		}
	}
	w.Line = strings.Join(parts, ", ")
	// the inbox is counted only for someone who keeps one
	if len(s.store.InboxTagIDs(ctx, uid)) > 0 {
		w.Metrics = append(w.Metrics, core.Metric{Label: "Inbox", Value: strconv.Itoa(nInbox)})
	}
	w.Metrics = append(w.Metrics, core.Metric{Label: "Untagged", Value: strconv.Itoa(untagged)},
		core.Metric{Label: "Added this week", Value: strconv.Itoa(week)}, core.Metric{Label: "Documents", Value: strconv.Itoa(total)})

	seen := map[int64]bool{}
	add := func(d store.Document, detail string) {
		if seen[d.ID] || len(w.Items) == rows {
			return
		}
		seen[d.ID] = true
		w.Items = append(w.Items, core.WidgetItem{ID: docKey(d), Title: docTitle(d), Detail: detail, URL: docURL(d)})
	}
	for _, d := range failed {
		add(d, "Could not be read")
	}
	for _, d := range inbox {
		add(d, dots("In the inbox", docFrom(d)))
	}
	for _, d := range recent {
		add(d, firstNonEmpty(docFrom(d), addedWords(d.AddedAt, now)))
	}
	return w, nil
}
