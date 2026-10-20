// Package search parses the one search box: free text plus field filters
// such as tag:invoice, from:ikea, type:receipt, created:2024, asn:12,
// is:inbox, has:notes, -word and "a phrase".
package search

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Parsed is a query taken apart. Names (tags, correspondents…) are resolved
// to ids by the caller, since they belong to a person.
type Parsed struct {
	Text            string   // what is left for full text
	Tags            []string // tag:x, #x
	NotTags         []string // -tag:x
	Correspondents  []string // from:x, correspondent:x
	Types           []string // type:x
	Paths           []string // path:x
	ASN             *int64
	ASNFrom, ASNTo  *int64
	CreatedFrom     string
	CreatedTo       string
	AddedFrom       string
	AddedTo         string
	Inbox           bool
	Untagged        bool
	Favorite        *bool
	HasNotes        *bool
	HasASN          *bool
	NoCorrespondent bool
	NoType          bool
	Status          string
	Mime            string
	Fields          map[string]string // field:name=value
	Title           string            // title:x
	Unknown         []string          // filters that were not understood (kept as text)
}

var tokenRe = regexp.MustCompile(`[^\s"]*"[^"]*"|\S+`)

// Parse takes the query apart.
func Parse(q string) Parsed {
	p := Parsed{Fields: map[string]string{}}
	var text []string
	for _, tok := range tokenRe.FindAllString(q, -1) {
		if strings.HasPrefix(tok, `"`) {
			text = append(text, tok)
			continue
		}
		if strings.HasPrefix(tok, "#") && len(tok) > 1 {
			p.Tags = append(p.Tags, strings.TrimPrefix(tok, "#"))
			continue
		}
		neg := strings.HasPrefix(tok, "-")
		key, val, ok := strings.Cut(strings.TrimPrefix(tok, "-"), ":")
		if !ok || key == "" || val == "" {
			text = append(text, tok)
			continue
		}
		key = strings.ToLower(key)
		val = strings.Trim(val, `"`)
		switch key {
		case "tag", "tags":
			if neg {
				p.NotTags = append(p.NotTags, val)
			} else {
				p.Tags = append(p.Tags, val)
			}
		case "from", "correspondent", "sender", "corr":
			if strings.EqualFold(val, "none") {
				p.NoCorrespondent = true
			} else {
				p.Correspondents = append(p.Correspondents, val)
			}
		case "type", "kind", "doctype":
			if strings.EqualFold(val, "none") {
				p.NoType = true
			} else {
				p.Types = append(p.Types, val)
			}
		case "path", "folder", "storage":
			p.Paths = append(p.Paths, val)
		case "asn":
			p.ASNFrom, p.ASNTo, p.ASN = parseIntRange(val)
		case "created", "date", "year":
			p.CreatedFrom, p.CreatedTo = DateRange(val)
		case "added":
			p.AddedFrom, p.AddedTo = DateRange(val)
		case "is":
			switch strings.ToLower(val) {
			case "inbox":
				p.Inbox = true
			case "untagged":
				p.Untagged = true
			case "favorite", "favourite", "starred", "fav":
				t := !neg
				p.Favorite = &t
			case "pending", "processing", "done", "failed":
				p.Status = strings.ToLower(val)
			case "pdf":
				p.Mime = "application/pdf"
			case "image":
				p.Mime = "image/"
			default:
				p.Unknown = append(p.Unknown, tok)
				text = append(text, val)
			}
		case "has":
			t := !neg
			switch strings.ToLower(val) {
			case "notes", "note":
				p.HasNotes = &t
			case "asn":
				p.HasASN = &t
			default:
				p.Unknown = append(p.Unknown, tok)
				text = append(text, val)
			}
		case "status":
			p.Status = strings.ToLower(val)
		case "mime":
			p.Mime = val
		case "title":
			p.Title = val
		case "field", "cf":
			name, v, _ := strings.Cut(val, "=")
			p.Fields[strings.ToLower(name)] = v
		default:
			// "https://x" or "10:30" is text, not a filter
			text = append(text, tok)
		}
	}
	p.Text = strings.TrimSpace(strings.Join(text, " "))
	return p
}

func parseIntRange(v string) (from, to, exact *int64) {
	if a, b, ok := strings.Cut(v, ".."); ok {
		if n, err := strconv.ParseInt(a, 10, 64); err == nil {
			from = &n
		}
		if n, err := strconv.ParseInt(b, 10, 64); err == nil {
			to = &n
		}
		return
	}
	if strings.HasPrefix(v, ">=") || strings.HasPrefix(v, ">") {
		if n, err := strconv.ParseInt(strings.TrimLeft(v, ">="), 10, 64); err == nil {
			if !strings.HasPrefix(v, ">=") {
				n++
			}
			from = &n
		}
		return
	}
	if strings.HasPrefix(v, "<=") || strings.HasPrefix(v, "<") {
		if n, err := strconv.ParseInt(strings.TrimLeft(v, "<="), 10, 64); err == nil {
			if !strings.HasPrefix(v, "<=") {
				n--
			}
			to = &n
		}
		return
	}
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		exact = &n
	}
	return
}

// DateRange turns 2024, 2024-05, 2024-05-03, 2024..2025, >2024-01-01,
// <2024-06, today, week, month, year, 30d into an inclusive from/to pair.
func DateRange(v string) (from, to string) {
	v = strings.ToLower(strings.TrimSpace(v))
	now := time.Now()
	day := func(t time.Time) string { return t.Format("2006-01-02") }
	switch v {
	case "today":
		return day(now), day(now)
	case "yesterday":
		return day(now.AddDate(0, 0, -1)), day(now.AddDate(0, 0, -1))
	case "week", "thisweek", "this-week":
		wd := int(now.Weekday())
		if wd == 0 {
			wd = 7
		}
		return day(now.AddDate(0, 0, -(wd - 1))), day(now)
	case "month", "thismonth", "this-month":
		return now.Format("2006-01") + "-01", day(now)
	case "year", "thisyear", "this-year":
		return now.Format("2006") + "-01-01", day(now)
	case "lastyear", "last-year":
		y := now.AddDate(-1, 0, 0).Format("2006")
		return y + "-01-01", y + "-12-31"
	}
	if strings.HasSuffix(v, "d") {
		if n, err := strconv.Atoi(strings.TrimSuffix(v, "d")); err == nil {
			return day(now.AddDate(0, 0, -n)), ""
		}
	}
	if a, b, ok := strings.Cut(v, ".."); ok {
		fa, _ := DateRange(a)
		_, tb := DateRange(b)
		return fa, tb
	}
	if strings.HasPrefix(v, ">=") {
		f, _ := DateRange(v[2:])
		return f, ""
	}
	if strings.HasPrefix(v, ">") {
		_, t := DateRange(v[1:])
		if t == "" {
			return "", ""
		}
		d, _ := time.Parse("2006-01-02", t)
		return day(d.AddDate(0, 0, 1)), ""
	}
	if strings.HasPrefix(v, "<=") {
		_, t := DateRange(v[2:])
		return "", t
	}
	if strings.HasPrefix(v, "<") {
		f, _ := DateRange(v[1:])
		if f == "" {
			return "", ""
		}
		d, _ := time.Parse("2006-01-02", f)
		return "", day(d.AddDate(0, 0, -1))
	}
	switch len(v) {
	case 4:
		if _, err := strconv.Atoi(v); err == nil {
			return v + "-01-01", v + "-12-31"
		}
	case 7:
		if t, err := time.Parse("2006-01", v); err == nil {
			return day(t), day(t.AddDate(0, 1, -1))
		}
	case 10:
		if t, err := time.Parse("2006-01-02", v); err == nil {
			return day(t), day(t)
		}
		if t, err := time.Parse("02.01.2006", v); err == nil {
			return day(t), day(t)
		}
	}
	return "", ""
}
