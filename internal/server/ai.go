package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
// finds replaces what is there; tags are only added.
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
	res, err := s.aiFill(ctx, d, aiFillMode{Title: true, Date: true, Replace: true}, actorName(r))
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	if len(res.Applied) > 0 {
		d, _ = s.store.GetDocument(ctx, uid, id, false)
		_ = s.store.IndexDocument(ctx, d.ID)
		if err := s.placeInFolder(ctx, d); err != nil {
			s.log.Warn("documents folder", "doc", d.ID, "err", err)
		}
		s.classifiers.Invalidate(uid)
	}
	writeJSON(w, http.StatusOK, res)
}

// aiFillMode says what the AI may change. Without Replace it only fills what
// was empty when the document came in (before), so an uploader's or a mail
// rule's choice stays; matching guesses made since may be replaced.
type aiFillMode struct {
	Title   bool // the title was made of the file name
	Date    bool // no date was given
	Replace bool
	Before  *store.Document
}

type aiFillResult struct {
	Applied []string `json:"applied"`
	Reason  string   `json:"reason"`
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
	Reason string `json:"reason"`
}

func (s *Server) aiFill(ctx context.Context, d *store.Document, mode aiFillMode, actor string) (aiFillResult, error) {
	var res aiFillResult
	client, st, detail := s.aiFor(ctx, d.UserID)
	if client == nil {
		return res, fmt.Errorf("AI is not available: %s", detail)
	}
	uid := d.UserID
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
			fmt.Fprintf(&b, "\nThe matching rules guessed: %s (they can be wrong).\n", strings.Join(g, ", "))
		}
	}
	fmt.Fprintf(&b, "\nThe document:\nFile name: %s\nCurrent title: %s\nPages: %d\nText (OCR):\n<<<\n%s\n>>>\n", d.OriginalName, d.Title, d.PageCount, aiText(d.Content))

	var out aiAnswer
	actx, cancel := context.WithTimeout(ctx, 120*time.Second)
	usage, err := client.JSON(actx, fillPrompt, b.String(), &out)
	cancel()
	res.Model, res.Reason = usage.Model, strings.TrimSpace(out.Reason)
	if err != nil {
		return res, err
	}

	// what may change
	before := mode.Before
	if before == nil {
		before = d
	}
	free := func(was *int64) bool { return mode.Replace || was == nil }
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
	pick := func(id *FlexID, valid map[int64]string, was *int64, cur *int64, into ***int64, label string) {
		if id == nil || !free(was) {
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
	if free(before.CorrespondentID) && (out.CorrespondentID == nil || names.Correspondents[int64(*out.CorrespondentID)] == "") && out.NewCorrespondent != nil {
		if name := strings.TrimSpace(*out.NewCorrespondent); name != "" && len([]rune(name)) <= 80 {
			if n, err := s.store.FindCorrespondent(ctx, uid, name); err == nil {
				id := FlexID(n.ID)
				out.CorrespondentID = &id
			} else {
				n := &store.Named{UserID: uid, Name: name, Matchable: store.Matchable{MatchingAlgorithm: 6, IsInsensitive: true}}
				if err := s.store.CreateCorrespondent(ctx, n); err == nil {
					names.Correspondents[n.ID] = n.Name
					id := FlexID(n.ID)
					out.CorrespondentID = &id
					res.Applied = append(res.Applied, "new correspondent")
				}
			}
		}
	}
	pick(out.CorrespondentID, names.Correspondents, before.CorrespondentID, d.CorrespondentID, &c.CorrespondentID, "correspondent")
	pick(out.DocumentTypeID, names.Types, before.DocumentTypeID, d.DocumentTypeID, &c.DocumentTypeID, "type")
	pick(out.StoragePathID, names.Paths, before.StoragePathID, d.StoragePathID, &c.StoragePathID, "path")
	for _, t := range out.TagIDs {
		if id := int64(t); validTag[id] && !slices.Contains(d.Tags, id) && !slices.Contains(c.AddTags, id) {
			c.AddTags = append(c.AddTags, id)
			res.Applied = append(res.Applied, "tag "+names.Tags[id])
		}
	}
	has := map[int64]bool{}
	for _, f := range d.Fields {
		has[f.FieldID] = strings.TrimSpace(f.Value) != ""
	}
	for _, fv := range out.Fields {
		f, ok := names.Fields[int64(fv.FieldID)]
		if !ok || (has[f.ID] && !mode.Replace) {
			continue
		}
		if v, ok := fieldValue(f, fv.Value); ok {
			c.SetFields = append(c.SetFields, store.FieldValue{FieldID: f.ID, Value: v})
			res.Applied = append(res.Applied, f.Name+" "+v)
		}
	}
	if len(res.Applied) == 0 {
		return res, nil
	}
	changed, err := s.store.UpdateDocument(ctx, uid, d.ID, c)
	if err != nil {
		return res, err
	}
	if len(changed) > 0 {
		s.store.AddHistory(ctx, d.ID, uid, firstNonEmpty(actor, "ai"), "filled in by AI", map[string]any{"changes": changed, "model": usage.Model, "reason": res.Reason, "tokens": usage.PromptTokens + usage.CompletionTokens})
	}
	return res, nil
}

const fillPrompt = `You file one person's documents in their archive (Pocket Documents, like Paperless-ngx): you read the OCR text of a new document and choose its title, date, correspondent, type, storage path, tags and custom fields.

Documents are mostly from Bosnia and Herzegovina, Serbia, Montenegro or Croatia, in Serbian, Bosnian or Croatian (Latin or Cyrillic) or English, and often OCR'd from phone photos, so the text has errors. A fiscal receipt ("FISKALNI RAČUN", "ФИСКАЛНИ РАЧУН") names the shop that issued it at the top; the buyer's ID ("ID kupca", "VP:…") is not the shop.

File the way the person already files: judge every correspondent, type, storage path and tag by the person's recent documents and by how often it is used, not only by its name. A name can mislead; an entry with no documents and nothing similar among the examples is rarely right. Prefer the person's existing entries.

- title: short, in the same language, letters and style as the person's titles (e.g. "<who> <what> <month or number>"). Never just a date or a scanner's file name.
- created: the date on the document (issue date, receipt date, signing date) as YYYY-MM-DD, or null when it shows none.
- correspondent_id: who issued or sent it (the shop, company, office or person) from the list, or null.
- new_correspondent: only when nobody in the list fits and the issuer is clearly named: their short common name ("Konzum", "Elektroprivreda RS"), without legal form or address. Else null.
- document_type_id, storage_path_id: from the lists, or null when none fits.
- tag_ids: tags from the list that clearly apply, or [].
- fields: values for the listed custom fields the document clearly states (dates YYYY-MM-DD, amounts as 12.50 without currency, booleans true/false, a select field one of its options); leave the rest out.

Answer only JSON: {"title":"…","created":"YYYY-MM-DD" or null,"correspondent_id":<id> or null,"new_correspondent":"…" or null,"document_type_id":<id> or null,"storage_path_id":<id> or null,"tag_ids":[<id>],"fields":[{"field_id":<id>,"value":"…"}],"reason":"one short sentence on what the document is"}.`

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
		v = strings.ReplaceAll(strings.TrimSpace(strings.TrimLeft(v, "ABCDEFGHIJKLMNOPQRSTUVWXYZ€$ ")), ",", ".")
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
