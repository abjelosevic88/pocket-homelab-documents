package ocr

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Month names in a few languages, lowercased, mapped to the month.
var months = map[string]time.Month{}

func init() {
	add := func(m time.Month, names ...string) {
		for _, n := range names {
			months[n] = m
		}
	}
	add(1, "january", "jan", "januar", "jänner", "janvier", "januari", "siječanj", "sijecanj", "januar", "enero", "gennaio")
	add(2, "february", "feb", "februar", "février", "fevrier", "februari", "veljača", "veljaca", "febrero", "febbraio")
	add(3, "march", "mar", "märz", "maerz", "mars", "maart", "ožujak", "ozujak", "mart", "marzo")
	add(4, "april", "apr", "avril", "travanj", "abril", "aprile")
	add(5, "may", "mai", "mei", "svibanj", "maj", "mayo", "maggio")
	add(6, "june", "jun", "juni", "juin", "lipanj", "junio", "giugno")
	add(7, "july", "jul", "juli", "juillet", "srpanj", "julio", "luglio")
	add(8, "august", "aug", "août", "aout", "augustus", "kolovoz", "avgust", "agosto")
	add(9, "september", "sep", "sept", "septembre", "rujan", "septembar", "septiembre", "settembre")
	add(10, "october", "oct", "oktober", "octobre", "listopad", "oktobar", "octubre", "ottobre")
	add(11, "november", "nov", "novembre", "studeni", "novembar", "noviembre")
	add(12, "december", "dec", "dezember", "décembre", "decembre", "prosinac", "decembar", "diciembre", "dicembre")
}

var (
	numericDate = regexp.MustCompile(`\b(\d{1,2})[./-](\d{1,2})[./-](\d{4}|\d{2})\b`)
	isoDate     = regexp.MustCompile(`\b(\d{4})-(\d{2})-(\d{2})\b`)
	wordDate    = regexp.MustCompile(`(?i)\b(\d{1,2})\.?\s+([\p{L}]{3,10})\.?\s+(\d{4})\b`)
	usWordDate  = regexp.MustCompile(`(?i)\b([\p{L}]{3,10})\.?\s+(\d{1,2}),?\s+(\d{4})\b`)
)

// FindDates returns the plausible dates mentioned in a text, in the order
// found. Years outside [1900, now+1] are dropped.
func FindDates(text string, dayFirst bool) []time.Time {
	var out []time.Time
	seen := map[string]bool{}
	now := time.Now()
	push := func(y, m, d int) {
		if y < 100 {
			y += 2000
			if y > now.Year()+1 {
				y -= 100
			}
		}
		if y < 1900 || y > now.Year()+1 || m < 1 || m > 12 || d < 1 || d > 31 {
			return
		}
		t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
		if t.Day() != d {
			return
		}
		k := t.Format("2006-01-02")
		if !seen[k] {
			seen[k] = true
			out = append(out, t)
		}
	}
	atoi := func(s string) int { n, _ := strconv.Atoi(s); return n }
	// A type for found matches with position, so results come in text order.
	type hit struct {
		pos     int
		y, m, d int
	}
	var hits []hit
	for _, m := range isoDate.FindAllStringSubmatchIndex(text, -1) {
		hits = append(hits, hit{m[0], atoi(text[m[2]:m[3]]), atoi(text[m[4]:m[5]]), atoi(text[m[6]:m[7]])})
	}
	for _, m := range numericDate.FindAllStringSubmatchIndex(text, -1) {
		a, b, y := atoi(text[m[2]:m[3]]), atoi(text[m[4]:m[5]]), atoi(text[m[6]:m[7]])
		d, mo := a, b
		if !dayFirst || a > 12 && b <= 12 {
			if a <= 12 && b > 12 || !dayFirst {
				d, mo = b, a
			}
		}
		if a > 12 && b <= 12 {
			d, mo = a, b
		} else if b > 12 && a <= 12 {
			d, mo = b, a
		}
		hits = append(hits, hit{m[0], y, mo, d})
	}
	for _, m := range wordDate.FindAllStringSubmatchIndex(text, -1) {
		if mo, ok := months[strings.ToLower(text[m[4]:m[5]])]; ok {
			hits = append(hits, hit{m[0], atoi(text[m[6]:m[7]]), int(mo), atoi(text[m[2]:m[3]])})
		}
	}
	for _, m := range usWordDate.FindAllStringSubmatchIndex(text, -1) {
		if mo, ok := months[strings.ToLower(text[m[2]:m[3]])]; ok {
			hits = append(hits, hit{m[0], atoi(text[m[6]:m[7]]), int(mo), atoi(text[m[4]:m[5]])})
		}
	}
	// text order
	for i := 1; i < len(hits); i++ {
		for j := i; j > 0 && hits[j].pos < hits[j-1].pos; j-- {
			hits[j], hits[j-1] = hits[j-1], hits[j]
		}
	}
	for _, h := range hits {
		push(h.y, h.m, h.d)
	}
	return out
}

// GuessCreated picks the document's date from its text: the first date
// found near the top that is not in the future. Returns "" when none.
func GuessCreated(text string, dayFirst bool) string {
	head := text
	if len(head) > 6000 {
		head = head[:6000]
	}
	now := time.Now().AddDate(0, 0, 1)
	for _, d := range FindDates(head, dayFirst) {
		if d.Before(now) {
			return d.Format("2006-01-02")
		}
	}
	return ""
}
