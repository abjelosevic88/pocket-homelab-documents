package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-documents/internal/ai"
	"github.com/abjelosevic88/pocket-homelab-documents/internal/store"
)

// ---- AI filling in documents ----
//
// The AI is picked the way Pocket Money picks it: "automatic" uses Pocket AI
// when this app's Core link is on (with the person's Pocket AI model unless
// they chose another one here), else their own OpenAI key (gpt-5.4-mini, low
// reasoning effort). It reads the OCR text and files the document the way the
// person files: their correspondents, types, paths and tags with how often
// each is used, and their recent documents as examples.

func (s *Server) inferenceFor(ctx context.Context, uid int64, provider string, directReady bool) (*core.InferenceClient, core.AIStatus) {
	email := ""
	if u, err := s.store.GetUser(ctx, uid); err == nil {
		email = u.Email
	}
	return s.core.SelectAI(ctx, email, provider, directReady)
}

// aiFor is the person's AI, or nil and why not.
func (s *Server) aiFor(ctx context.Context, uid int64) (*ai.Client, store.Settings, string) {
	st, _ := s.store.GetSettings(ctx, uid)
	c := ai.New(st.AIURL, st.AIKey, st.AIModel)
	pocket, status := s.inferenceFor(ctx, uid, st.AIProvider, st.AIKey != "")
	c.Pocket = pocket.WithModel(st.AIPocketModel)
	if !c.Configured() || !status.Ready {
		return nil, st, status.Detail
	}
	return c, st, status.Detail
}

func (s *Server) handleInferenceModels(w http.ResponseWriter, r *http.Request) {
	// the caller's link, even while the provider form has unsaved changes
	c, status := s.inferenceFor(r.Context(), me(r), "pocket", false)
	if !status.Ready {
		writeErr(w, http.StatusServiceUnavailable, status.Detail)
		return
	}
	models, err := c.Models(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, models)
}

// handleAIFill runs the AI on one document now ("Fill in with AI"): what it
// finds replaces what is there (with fill: only what is empty); tags are only
// added.
func (s *Server) handleAIFill(w http.ResponseWriter, r *http.Request) {
	ctx, uid, id := r.Context(), me(r), idParam(r, "id")
	d, err := s.store.GetDocument(ctx, uid, id, true)
	if err != nil {
		s.fail(w, err, "document")
		return
	}
	if d.Access != "" && d.Access != "owner" {
		writeErr(w, http.StatusForbidden, "only the owner can do this")
		return
	}
	if strings.TrimSpace(d.Content) == "" {
		writeErr(w, http.StatusBadRequest, "this document has no text yet")
		return
	}
	var in struct {
		Fill bool `json:"fill"` // only what is empty, as for older documents
	}
	_ = decode(r, &in)
	mode := aiFillMode{Title: true, Date: true, Replace: true}
	if in.Fill {
		mode = aiFillMode{Fill: true}
	}
	mode.Event = true
	res, err := s.runAI(ctx, d, mode, actorName(r), true)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// aiFillMode says what the AI may change. Without Replace it only fills what
// was empty when the document came in (the Keep flags), so an uploader's or a
// mail rule's choice stays; matching guesses made since may be replaced. Fill
// (older documents read later) works the flags out when it runs: whatever is
// set stays, a title made of a scanner's file name and a date that is only
// the day it was added may change. It is saved with a queued run.
type aiFillMode struct {
	Title    bool `json:"title,omitempty"` // the title was made of the file name
	Date     bool `json:"date,omitempty"`  // no date was given (or only the file's or today's)
	Replace  bool `json:"replace,omitempty"`
	Fill     bool `json:"fill,omitempty"`
	KeepCorr bool `json:"keep_correspondent,omitempty"`
	KeepType bool `json:"keep_type,omitempty"`
	KeepPath bool `json:"keep_path,omitempty"`
	Event    bool `json:"-"` // tell other apps (documents.updated); a new document's own event covers it
	InJob    bool `json:"-"` // the OCR job files the document itself afterwards
}

// keepSet marks what the document already has as staying.
func (m aiFillMode) keepSet(d *store.Document) aiFillMode {
	m.KeepCorr, m.KeepType, m.KeepPath = d.CorrespondentID != nil, d.DocumentTypeID != nil, d.StoragePathID != nil
	return m
}

// scannerName: a file name nobody chose by hand (Scan 2026-11-03 21.19,
// IMG_2041, 260914_Contract_x - 15_09_2026, 11_19).
var scannerName = regexp.MustCompile(`(?i)^(scan|img|image|photo|pxl|dsc|cam|screenshot|document|dokument|doc|file|untitled)\b|_|\d{1,2}[.:_ ]\d{2}([.:_ ]\d{2})?\b|^[\d\s,.:_()-]+$`)

// machineTitle reports whether the title is still a scanner's file name.
func machineTitle(d *store.Document) bool {
	t := strings.TrimSpace(d.Title)
	base := strings.TrimSuffix(d.OriginalName, filepath.Ext(d.OriginalName))
	if t == "" || t == "Untitled" {
		return true
	}
	return (t == strings.TrimSpace(base) || t == titleFromName(d.OriginalName, false) || t == titleFromName(d.OriginalName, true)) && scannerName.MatchString(base)
}

type aiFillResult struct {
	Applied []string `json:"applied"`
	Reason  string   `json:"reason"`
	Summary string   `json:"summary"`
	Model   string   `json:"model"`
}

type aiAnswer struct {
	Title            string   `json:"title"`
	Created          *string  `json:"created"`
	CorrespondentID  *FlexID  `json:"correspondent_id"`
	NewCorrespondent *string  `json:"new_correspondent"`
	DocumentTypeID   *FlexID  `json:"document_type_id"`
	StoragePathID    *FlexID  `json:"storage_path_id"`
	TagIDs           []FlexID `json:"tag_ids"`
	Fields           []struct {
		FieldID FlexID `json:"field_id"`
		Value   any    `json:"value"`
	} `json:"fields"`
	NewDocumentType *string  `json:"new_document_type"`
	NewTags         []string `json:"new_tags"`
	NewFields       []struct {
		Name  string `json:"name"`
		Type  string `json:"type"`
		Value any    `json:"value"`
	} `json:"new_fields"`
	Summary string `json:"summary"`
	Reason  string `json:"reason"`
}

func (s *Server) aiFill(ctx context.Context, d *store.Document, mode aiFillMode, actor string) (aiFillResult, error) {
	var res aiFillResult
	client, st, detail := s.aiFor(ctx, d.UserID)
	if client == nil {
		return res, fmt.Errorf("AI is not available: %s", detail)
	}
	uid := d.UserID
	if mode.Fill {
		mode = mode.keepSet(d)
		mode.Title = machineTitle(d)
		mode.Date = d.Created == "" || (len(d.AddedAt) >= 10 && d.Created == d.AddedAt[:10])
	}
	tags, _ := s.store.ListTags(ctx, uid)
	corr, _ := s.store.ListCorrespondents(ctx, uid)
	types, _ := s.store.ListDocumentTypes(ctx, uid)
	paths, _ := s.store.ListStoragePaths(ctx, uid)
	fields, _ := s.store.ListCustomFields(ctx, uid)
	names := s.store.Names(ctx, uid)

	var b strings.Builder
	now := time.Now()
	fmt.Fprintf(&b, "Today is %s.\n", now.Format("2006-01-02"))
	if c := strings.TrimSpace(st.AIContext); c != "" {
		fmt.Fprintf(&b, "Words the person wants you to know: %s\n", c)
	}
	named := func(title string, list []store.Named, withPath bool) {
		fmt.Fprintf(&b, "\n%s (id \"name\": documents):\n", title)
		if len(list) == 0 {
			b.WriteString("(none yet)\n")
		}
		for _, n := range list {
			fmt.Fprintf(&b, "- id %d \"%s\": %d", n.ID, n.Name, n.DocumentCount)
			if withPath && n.Path != "" {
				fmt.Fprintf(&b, " (folder layout %s)", n.Path)
			}
			b.WriteString("\n")
		}
	}
	named("Correspondents", corr, false)
	named("Document types", types, false)
	named("Storage paths", paths, true)
	b.WriteString("\nTags (id \"name\": documents):\n")
	validTag := map[int64]bool{}
	for _, t := range tags {
		if t.IsInbox {
			continue
		}
		validTag[t.ID] = true
		fmt.Fprintf(&b, "- id %d \"%s\": %d\n", t.ID, t.Name, t.DocumentCount)
	}
	if len(fields) > 0 {
		b.WriteString("\nCustom fields (id \"name\" type):\n")
		for _, f := range fields {
			if f.DataType == "documentlink" {
				continue
			}
			fmt.Fprintf(&b, "- id %d \"%s\" %s%s\n", f.ID, f.Name, f.DataType, fieldHint(f))
		}
	}
	if ex := s.store.AIExamples(ctx, uid, d.ID, 40); len(ex) > 0 {
		b.WriteString("\nThe person's recent documents (title | date | correspondent | type | storage path | tags):\n")
		for _, e := range ex {
			fmt.Fprintf(&b, "- %s | %s | %s | %s | %s | %s\n", e.Title, e.Created, dash(e.Correspondent), dash(e.Type), dash(e.Path), dash(strings.Join(e.Tags, ", ")))
		}
	}
	if !st.AICreate {
		b.WriteString("\nDo not suggest anything new: new_correspondent, new_document_type, new_tags and new_fields stay empty.\n")
	}
	if !mode.Replace {
		var g []string
		if d.CorrespondentID != nil {
			g = append(g, "correspondent "+names.Correspondents[*d.CorrespondentID])
		}
		if d.DocumentTypeID != nil {
			g = append(g, "type "+names.Types[*d.DocumentTypeID])
		}
		if d.StoragePathID != nil {
			g = append(g, "storage path "+names.Paths[*d.StoragePathID])
		}
		if len(g) > 0 {
			if mode.Fill {
				fmt.Fprintf(&b, "\nAlready filed by the person as: %s (these stay).\n", strings.Join(g, ", "))
			} else {
				fmt.Fprintf(&b, "\nThe matching rules guessed: %s (they can be wrong).\n", strings.Join(g, ", "))
			}
		}
	}
	fmt.Fprintf(&b, "\nThe document:\nFile name: %s\nCurrent title: %s\nPages: %d\nText (OCR):\n<<<\n%s\n>>>\n", d.OriginalName, d.Title, d.PageCount, aiText(d.Content))

	var out aiAnswer
	actx, cancel := context.WithTimeout(ctx, 120*time.Second)
	usage, err := client.JSON(actx, fillPrompt, b.String(), &out)
	cancel()
	res.Model, res.Reason, res.Summary = usage.Model, strings.TrimSpace(out.Reason), clip(strings.TrimSpace(out.Summary), 600)
	if err != nil {
		return res, err
	}

	// what may change
	free := func(keep bool) bool { return mode.Replace || !keep }
	c := store.DocChanges{}
	if t := strings.TrimSpace(out.Title); t != "" && mode.Title && t != d.Title {
		t = strings.TrimSpace(strings.ReplaceAll(t, "/", "-"))
		if len([]rune(t)) > 200 {
			t = string([]rune(t)[:200])
		}
		c.Title = &t
		res.Applied = append(res.Applied, "title “"+t+"”")
	}
	if out.Created != nil && mode.Date {
		if day, err := time.Parse("2006-01-02", strings.TrimSpace(*out.Created)); err == nil && day.Year() >= 1900 && !day.After(now.AddDate(0, 0, 1)) {
			v := day.Format("2006-01-02")
			if v != d.Created {
				c.Created = &v
				res.Applied = append(res.Applied, "date "+v)
			}
		}
	}
	pick := func(id *FlexID, valid map[int64]string, keep bool, cur *int64, into ***int64, label string) {
		if id == nil || !free(keep) {
			return
		}
		v := int64(*id)
		if _, ok := valid[v]; !ok || (cur != nil && *cur == v) {
			return
		}
		p := &v
		*into = &p
		res.Applied = append(res.Applied, label+" "+valid[v])
	}
	// something new only when nothing in the person's lists fits
	newNamed := func(id **FlexID, valid map[int64]string, keep bool, name *string, find func(context.Context, int64, string) (*store.Named, error), create func(context.Context, *store.Named) error, label string) {
		if !st.AICreate || !free(keep) || name == nil || (*id != nil && valid[int64(**id)] != "") {
			return
		}
		nm := strings.TrimSpace(*name)
		if nm == "" || len([]rune(nm)) > 80 {
			return
		}
		if n, err := find(ctx, uid, nm); err == nil {
			v := FlexID(n.ID)
			*id = &v
			return
		}
		n := &store.Named{UserID: uid, Name: nm, Matchable: store.Matchable{MatchingAlgorithm: store.MatchAuto, IsInsensitive: true}}
		if err := create(ctx, n); err == nil {
			valid[n.ID] = n.Name
			v := FlexID(n.ID)
			*id = &v
			res.Applied = append(res.Applied, "new "+label)
		}
	}
	newNamed(&out.CorrespondentID, names.Correspondents, mode.KeepCorr, out.NewCorrespondent, s.store.FindCorrespondent, s.store.CreateCorrespondent, "correspondent")
	newNamed(&out.DocumentTypeID, names.Types, mode.KeepType, out.NewDocumentType, s.store.FindDocumentType, s.store.CreateDocumentType, "type")
	pick(out.CorrespondentID, names.Correspondents, mode.KeepCorr, d.CorrespondentID, &c.CorrespondentID, "correspondent")
	pick(out.DocumentTypeID, names.Types, mode.KeepType, d.DocumentTypeID, &c.DocumentTypeID, "type")
	pick(out.StoragePathID, names.Paths, mode.KeepPath, d.StoragePathID, &c.StoragePathID, "path")
	addTag := func(id int64) {
		if !slices.Contains(d.Tags, id) && !slices.Contains(c.AddTags, id) {
			c.AddTags = append(c.AddTags, id)
			res.Applied = append(res.Applied, "tag "+names.Tags[id])
		}
	}
	for _, t := range out.TagIDs {
		if id := int64(t); validTag[id] {
			addTag(id)
		}
	}
	if st.AICreate {
		for i, name := range out.NewTags {
			name = strings.TrimSpace(name)
			if i >= 2 || name == "" || len([]rune(name)) > 40 {
				continue
			}
			if t, err := s.store.FindTag(ctx, uid, name); err == nil {
				if !t.IsInbox {
					addTag(t.ID)
				}
				continue
			}
			t := &store.Tag{UserID: uid, Name: name, Color: tagColors[len(tags)%len(tagColors)], Matchable: store.Matchable{MatchingAlgorithm: store.MatchAuto, IsInsensitive: true}}
			if err := s.store.CreateTag(ctx, t); err == nil {
				tags = append(tags, *t)
				names.Tags[t.ID] = t.Name
				res.Applied = append(res.Applied, "new tag")
				addTag(t.ID)
			}
		}
	}
	has := map[int64]bool{}
	for _, f := range d.Fields {
		has[f.FieldID] = strings.TrimSpace(f.Value) != ""
	}
	setField := func(f store.CustomField, raw any) {
		if (has[f.ID] && !mode.Replace) || slices.ContainsFunc(c.SetFields, func(x store.FieldValue) bool { return x.FieldID == f.ID }) {
			return
		}
		if v, ok := fieldValue(f, raw); ok {
			c.SetFields = append(c.SetFields, store.FieldValue{FieldID: f.ID, Value: v})
			res.Applied = append(res.Applied, f.Name+" "+v)
		}
	}
	for _, fv := range out.Fields {
		if f, ok := names.Fields[int64(fv.FieldID)]; ok {
			setField(f, fv.Value)
		}
	}
	if st.AICreate {
		made := 0
		for _, nf := range out.NewFields {
			name := strings.TrimSpace(nf.Name)
			if name == "" || len([]rune(name)) > 40 || !slices.Contains(aiFieldTypes, nf.Type) {
				continue
			}
			if f, err := s.store.FindCustomField(ctx, uid, name); err == nil {
				if f.DataType == nf.Type {
					setField(*f, nf.Value)
				}
				continue
			}
			if _, ok := fieldValue(store.CustomField{DataType: nf.Type}, nf.Value); !ok || made >= 4 {
				continue
			}
			f := store.CustomField{UserID: uid, Name: name, DataType: nf.Type, Position: len(names.Fields)}
			if err := s.store.CreateCustomField(ctx, &f); err == nil {
				made++
				names.Fields[f.ID] = f
				res.Applied = append(res.Applied, "new field")
				setField(f, nf.Value)
			}
		}
	}
	// filed: out of the Inbox
	if st.AIInbox {
		corr, typ := d.CorrespondentID != nil, d.DocumentTypeID != nil
		if c.CorrespondentID != nil {
			corr = *c.CorrespondentID != nil
		}
		if c.DocumentTypeID != nil {
			typ = *c.DocumentTypeID != nil
		}
		if corr && typ {
			for _, t := range tags {
				if t.IsInbox && slices.Contains(d.Tags, t.ID) {
					c.RemoveTags = append(c.RemoveTags, t.ID)
				}
			}
			if len(c.RemoveTags) > 0 {
				res.Applied = append(res.Applied, "out of the Inbox")
			}
		}
	}
	if len(c.AddTags)+len(c.RemoveTags)+len(c.SetFields) == 0 && c.Title == nil && c.Created == nil && c.CorrespondentID == nil && c.DocumentTypeID == nil && c.StoragePathID == nil {
		return res, nil
	}
	var after []func(*sql.Tx, map[string][2]any) error
	if mode.Event {
		if ev := s.documentEvent(ctx, uid, "documents.updated", d, map[string]any{"actor": firstNonEmpty(actor, "ai")}); ev != nil {
			after = append(after, func(tx *sql.Tx, _ map[string][2]any) error { return ev(tx) })
		}
	}
	changed, err := s.store.UpdateDocument(ctx, uid, d.ID, c, after...)
	if err != nil {
		return res, err
	}
	if len(changed) > 0 {
		s.store.AddHistory(ctx, d.ID, uid, firstNonEmpty(actor, "ai"), "filled in by AI", map[string]any{"changes": changed, "model": usage.Model, "reason": res.Reason, "summary": res.Summary, "tokens": usage.PromptTokens + usage.CompletionTokens})
	}
	return res, nil
}

// aiFieldTypes are the kinds of field the AI may make.
var aiFieldTypes = []string{"string", "date", "monetary", "integer", "float", "boolean"}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

const fillPrompt = `You file one person's documents in their archive (Pocket Documents, like Paperless-ngx): you read the OCR text of a new document and choose its title, date, correspondent, type, storage path, tags and custom fields.

Documents are mostly from Bosnia and Herzegovina, Serbia, Montenegro or Croatia, in Serbian, Bosnian or Croatian (Latin or Cyrillic) or English, and often OCR'd from phone photos, so the text has errors. A fiscal receipt ("FISKALNI RAČUN", "ФИСКАЛНИ РАЧУН") names the shop that issued it at the top; the buyer's ID ("ID kupca", "VP:…") is not the shop.

File the way the person already files: judge every correspondent, type, storage path and tag by the person's recent documents and by how often it is used, not only by its name. A name can mislead; an entry with no documents and nothing similar among the examples is rarely right. Prefer the person's existing entries.

- title: short, in the same language, letters and style as the person's titles (e.g. "<who> <what> <month or number>"). Never just a date or a scanner's file name.
- created: the date on the document (issue date, receipt date, signing date) as YYYY-MM-DD, or null when it shows none.
- correspondent_id: who issued or sent it (the shop, company, office or person) from the list, or null.
- new_correspondent: only when nobody in the list fits and the issuer is clearly named: their short common name ("Konzum", "Elektroprivreda RS"), without legal form or address. Else null.
- document_type_id, storage_path_id: from the lists, or null when none fits.
- new_document_type: only when no type in the list fits and the kind of document is clear: a short name in the style and language of the person's types. Else null.
- tag_ids: tags from the list that clearly apply, or [].
- new_tags: at most 2 short tags the person would plausibly use again, only when none of theirs fits; usually []. Never a date, an amount or the correspondent's name.
- fields: values for the listed custom fields the document clearly states (dates YYYY-MM-DD, amounts as 12.50 without currency, booleans true/false, a select field one of its options); leave the rest out.
- new_fields: the key facts someone looks for later that no listed field holds: the total amount, invoice / contract / policy / document number, due date, valid-until or expiry date (IDs, cards, policies, warranties). Names short, in English and generic so they fit other documents too ("Amount", "Invoice number", "Due date", "Valid until", "Policy number", "Document number"); type one of string, date, monetary, integer, float, boolean; value as for fields. At most 4, only what the document clearly states, [] when nothing.
- summary: two short sentences in English: what the document is and its key facts (parties, amounts with currency, dates, deadlines, obligations).

Answer only JSON: {"title":"…","created":"YYYY-MM-DD" or null,"correspondent_id":<id> or null,"new_correspondent":"…" or null,"document_type_id":<id> or null,"new_document_type":"…" or null,"storage_path_id":<id> or null,"tag_ids":[<id>],"new_tags":["…"],"fields":[{"field_id":<id>,"value":"…"}],"new_fields":[{"name":"…","type":"…","value":"…"}],"summary":"…","reason":"one short sentence on why you filed it so"}.`

// aiText is the OCR text the AI reads: the start, and the end of a long one.
func aiText(t string) string {
	r := []rune(strings.TrimSpace(t))
	if len(r) <= 8000 {
		return string(r)
	}
	return string(r[:6500]) + "\n[…]\n" + string(r[len(r)-1500:])
}

func dash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

func fieldHint(f store.CustomField) string {
	ex := fieldExtra(f)
	switch {
	case f.DataType == "select" && len(ex.Options) > 0:
		return " (one of: " + strings.Join(ex.Options, ", ") + ")"
	case f.DataType == "monetary" && ex.Currency != "":
		return " (" + ex.Currency + ")"
	}
	return ""
}

// fieldValue checks an AI value against the field's type.
func fieldValue(f store.CustomField, raw any) (string, bool) {
	var v string
	switch x := raw.(type) {
	case string:
		v = strings.TrimSpace(x)
	case float64:
		v = strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		v = strconv.FormatBool(x)
	default:
		return "", false
	}
	if v == "" {
		return "", false
	}
	switch f.DataType {
	case "date":
		if _, err := time.Parse("2006-01-02", v); err != nil {
			return "", false
		}
	case "integer":
		if _, err := strconv.ParseInt(v, 10, 64); err != nil {
			return "", false
		}
	case "float", "monetary":
		v = plainNumber(v)
		n, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return "", false
		}
		if f.DataType == "monetary" {
			v = strconv.FormatFloat(n, 'f', 2, 64)
		}
	case "boolean":
		if v != "true" && v != "false" {
			return "", false
		}
	case "select":
		if !slices.Contains(fieldExtra(f).Options, v) {
			return "", false
		}
	case "documentlink":
		return "", false
	}
	if len([]rune(v)) > 500 {
		return "", false
	}
	return v, true
}

// plainNumber turns "1.500,00 KM", "€ 1,500.00" or "30,5" into 1500.00 / 30.5:
// the currency goes, and of "." and "," the last one is the decimal point.
func plainNumber(v string) string {
	v = strings.TrimFunc(strings.TrimSpace(v), func(r rune) bool { return !(r >= '0' && r <= '9') && r != '-' })
	v = strings.NewReplacer(" ", "", "\u00a0", "", "'", "").Replace(v)
	dot, comma := strings.LastIndex(v, "."), strings.LastIndex(v, ",")
	switch {
	case dot >= 0 && comma >= 0 && comma > dot:
		v = strings.ReplaceAll(v, ".", "")
		v = strings.Replace(v, ",", ".", 1)
	case dot >= 0 && comma >= 0:
		v = strings.ReplaceAll(v, ",", "")
	case comma >= 0 && strings.Count(v, ",") == 1:
		v = strings.Replace(v, ",", ".", 1)
	case comma >= 0:
		v = strings.ReplaceAll(v, ",", "")
	case strings.Count(v, ".") > 1:
		v = strings.ReplaceAll(v, ".", "")
	}
	return v
}

type fieldExtras struct {
	Options  []string `json:"options"`
	Currency string   `json:"currency"`
}

func fieldExtra(f store.CustomField) fieldExtras {
	var ex fieldExtras
	_ = json.Unmarshal(f.Extra, &ex)
	return ex
}

// FlexID reads an id the model wrote as a number or a string.
type FlexID int64

// UnmarshalJSON implements json.Unmarshaler.
func (f *FlexID) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		*f = 0
		return nil
	}
	*f = FlexID(n)
	return nil
}
