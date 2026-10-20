package mail

import (
	"path"
	"strings"
	"time"
)

// Filter is what a mail rule looks for. Every text field is a case-insensitive
// "contains"; empty means any. Attachment is a glob on the file name.
type Filter struct {
	From       string
	To         string
	Subject    string
	Body       string
	Attachment string
	MaxAgeDays int
}

func contains(hay, needle string) bool {
	needle = strings.TrimSpace(needle)
	if needle == "" {
		return true
	}
	return strings.Contains(strings.ToLower(hay), strings.ToLower(needle))
}

// Matches reports whether the message passes the filter. now is the time
// the age is measured against.
func (f Filter) Matches(m *Message, now time.Time) bool {
	if m == nil {
		return false
	}
	if !contains(m.From.String(), f.From) {
		return false
	}
	if strings.TrimSpace(f.To) != "" {
		ok := false
		for _, a := range m.To {
			if contains(a.String(), f.To) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if !contains(m.Subject, f.Subject) {
		return false
	}
	if !contains(m.Text, f.Body) {
		return false
	}
	if TooOld(m.Date, f.MaxAgeDays, now) {
		return false
	}
	if strings.TrimSpace(f.Attachment) != "" {
		ok := false
		for _, a := range m.Attachments {
			if GlobMatch(f.Attachment, a.Name) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// TooOld reports whether a message dated at is older than maxAgeDays
// (0 = never too old). An unknown date is never too old.
func TooOld(at time.Time, maxAgeDays int, now time.Time) bool {
	if maxAgeDays <= 0 || at.IsZero() {
		return false
	}
	return at.Before(now.AddDate(0, 0, -maxAgeDays))
}

// GlobMatch matches a file name against a shell-style pattern, ignoring
// case. A pattern without a wildcard matches as "contains"; a bad pattern
// matches nothing. Several patterns may be given separated by commas.
func GlobMatch(pattern, name string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return true
	}
	name = strings.ToLower(name)
	for _, p := range strings.Split(pattern, ",") {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		if !strings.ContainsAny(p, "*?[") {
			if strings.Contains(name, p) {
				return true
			}
			continue
		}
		if ok, err := path.Match(p, name); err == nil && ok {
			return true
		}
	}
	return false
}

// SinceFor is the SEARCH SINCE date of a rule: zero when any age is fine.
func SinceFor(maxAgeDays int, now time.Time) time.Time {
	if maxAgeDays <= 0 {
		return time.Time{}
	}
	return now.AddDate(0, 0, -maxAgeDays)
}
