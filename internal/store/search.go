package store

import (
	"sort"
	"strings"
	"unicode"
)

// FTSQuery turns free text into an FTS5 MATCH expression: every word becomes
// a quoted prefix token, quoted phrases stay phrases, and words starting with
// - are excluded. Returns "" when nothing is left.
func FTSQuery(q string) string { return ftsQuery(q, true) }

func ftsQuery(q string, translit bool) string {
	var pos, neg []string
	for _, tok := range splitQuery(q) {
		negate := false
		if strings.HasPrefix(tok, "-") && len(tok) > 1 {
			negate, tok = true, tok[1:]
		}
		phrase := false
		if strings.HasPrefix(tok, `"`) && strings.HasSuffix(tok, `"`) && len(tok) > 2 {
			phrase, tok = true, tok[1:len(tok)-1]
		}
		tok = strings.Map(func(r rune) rune {
			if r == '"' || r == '\'' || r == '(' || r == ')' || r == '*' || r == ':' || r == '^' || r == '{' || r == '}' {
				return ' '
			}
			return r
		}, tok)
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		var expr string
		if phrase {
			expr = `"` + tok + `"`
		} else {
			words := strings.Fields(tok)
			for i, w := range words {
				words[i] = `"` + w + `"*`
			}
			expr = strings.Join(words, " ")
		}
		if negate {
			neg = append(neg, expr)
		} else {
			pos = append(pos, expr)
		}
	}
	if len(pos) == 0 {
		return ""
	}
	out := strings.Join(pos, " ")
	// a query in one Serbian alphabet is tried in the other too
	if translit && HasCyrillic(q) {
		if alt := ftsQuery(CyrToLat(q), false); alt != "" && alt != out {
			out = "(" + out + ") OR (" + alt + ")"
		}
	} else if translit && IsLatinSerbianish(q) {
		if alt := ftsQuery(LatToCyr(q), false); alt != "" && alt != out {
			out = "(" + out + ") OR (" + alt + ")"
		}
	}
	for _, n := range neg {
		out = "(" + out + ") NOT (" + n + ")"
	}
	return out
}

// splitQuery splits on spaces but keeps "quoted phrases" together.
func splitQuery(q string) []string {
	var out []string
	var cur strings.Builder
	inQ := false
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range q {
		switch {
		case r == '"':
			cur.WriteRune(r)
			if inQ {
				flush()
			}
			inQ = !inQ
		case unicode.IsSpace(r) && !inQ:
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return out
}

// LikePattern escapes q for a LIKE … ESCAPE '\' query.
func LikePattern(q string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(q) + "%"
}

// stopwords are too common to tell documents apart (a few languages).
var stopwords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`the and for with that this from are was were have has not you your our its der die das und ist von mit den dem des ein eine für auf nicht sie wir
	les des une est pour dans sur par avec que qui pas vous nous il elle
	i u na za je se su sa od do ne da ili ali kao što sve ovo ova ovaj koji koja koje biti bio bila bili
	и у на за је се су са од до не да или али као што све ово ова овај који која које бити био била били
	de la el en los las por con para una del que como más sus
	page total date invoice number amount tel fax email www http https com net org gmbh ltd inc llc
	jan feb mar apr may jun jul aug sep oct nov dec`) {
		stopwords[w] = true
	}
}

// Tokens splits text into lowercase word tokens of 3+ letters, without
// stopwords or pure numbers; this is what the classifier and "similar" use.
func Tokens(text string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	}) {
		if len([]rune(w)) < 3 || len(w) > 40 || stopwords[w] {
			continue
		}
		digits := 0
		for _, r := range w {
			if unicode.IsDigit(r) {
				digits++
			}
		}
		if digits > len([]rune(w))/2 {
			continue
		}
		out = append(out, w)
	}
	return out
}

// TopTerms returns the n most frequent tokens of a text, most frequent first.
func TopTerms(text string, n int) []string {
	freq := map[string]int{}
	for _, t := range Tokens(text) {
		freq[t]++
	}
	type kv struct {
		k string
		v int
	}
	var all []kv
	for k, v := range freq {
		all = append(all, kv{k, v})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].v != all[j].v {
			return all[i].v > all[j].v
		}
		return all[i].k < all[j].k
	})
	var out []string
	for i := 0; i < len(all) && i < n; i++ {
		out = append(out, all[i].k)
	}
	return out
}
