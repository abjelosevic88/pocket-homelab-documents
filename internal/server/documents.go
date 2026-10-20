package server

import (
	"errors"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-documents/internal/match"
	"github.com/abjelosevic88/pocket-homelab-documents/internal/ocr"
	"github.com/abjelosevic88/pocket-homelab-documents/internal/search"
	"github.com/abjelosevic88/pocket-homelab-documents/internal/store"
)

// ---- listing & search ----

// listFilter builds the store filter from the query string: the search box
// (q, with its field syntax) plus explicit parameters the filter sidebar sends.
func (s *Server) listFilter(r *http.Request) (store.DocFilter, search.Parsed) {
	ctx, uid := r.Context(), me(r)
	q := r.URL.Query()
	p := search.Parse(q.Get("q"))
	f := store.DocFilter{UserID: uid, Query: p.Text, FTS: store.FTSQuery(p.Text)}
	names := s.store.Names(ctx, uid)
	resolve := func(m map[int64]string, vals []string) []int64 {
		var out []int64
		for _, v := range vals {
			found := false
			for id, name := range m {
				if strings.EqualFold(name, v) {
					out = append(out, id)
					found = true
					break
				}
			}
			if !found {
				out = append(out, -1) // nothing has this name: match nothing
			}
		}
		return out
	}
	ints := func(key string) []int64 {
		var out []int64
		for _, raw := range q[key] {
			for _, v := range strings.Split(raw, ",") {
				if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
					out = append(out, n)
				}
			}
		}
		return out
	}
	f.Tags = append(resolve(names.Tags, p.Tags), ints("tags")...)
	f.Tags = append(f.Tags, ints("tags__all")...)
	f.AnyTags = ints("tags__any")
	f.NotTags = append(resolve(names.Tags, p.NotTags), ints("tags__none")...)
	f.Correspondents = append(resolve(names.Correspondents, p.Correspondents), ints("correspondent")...)
	f.Types = append(resolve(names.Types, p.Types), ints("document_type")...)
	f.Paths = append(resolve(names.Paths, p.Paths), ints("storage_path")...)
	f.NoCorrespondent = p.NoCorrespondent || q.Get("correspondent") == "none"
	f.NoType = p.NoType || q.Get("document_type") == "none"
	f.NoPath = q.Get("storage_path") == "none"
	f.ASN, f.ASNFrom, f.ASNTo, f.HasASN = p.ASN, p.ASNFrom, p.ASNTo, p.HasASN
	if v := q.Get("asn"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			f.ASN = &n
		}
	}
	f.CreatedFrom, f.CreatedTo = firstNonEmpty(q.Get("created_from"), p.CreatedFrom), firstNonEmpty(q.Get("created_to"), p.CreatedTo)
	if v := q.Get("created"); v != "" {
		f.CreatedFrom, f.CreatedTo = search.DateRange(v)
	}
	f.AddedFrom, f.AddedTo = firstNonEmpty(q.Get("added_from"), p.AddedFrom), firstNonEmpty(q.Get("added_to"), p.AddedTo)
	if v := q.Get("added"); v != "" {
		f.AddedFrom, f.AddedTo = search.DateRange(v)
	}
	f.Inbox = p.Inbox || queryBool(r, "inbox")
	f.Untagged = p.Untagged || queryBool(r, "untagged")
	f.Favorite = p.Favorite
	if v := q.Get("favorite"); v != "" {
		b := v == "1" || v == "true"
		f.Favorite = &b
	}
	f.HasNotes = p.HasNotes
	f.Status = firstNonEmpty(q.Get("status"), p.Status)
	f.Mime = firstNonEmpty(q.Get("mime"), p.Mime)
	f.TitleContains = firstNonEmpty(q.Get("title"), p.Title)
	f.Trash = queryBool(r, "trash")
	f.SharedWithMe = queryBool(r, "shared")
	f.IDs = ints("ids")
	f.Fields = map[int64]string{}
	for name, v := range p.Fields {
		for id, cf := range names.Fields {
			if strings.EqualFold(cf.Name, name) {
				f.Fields[id] = v
			}
		}
	}
	for _, raw := range q["field"] { // field=<id>:<value> or field=<id>
		idStr, v, _ := strings.Cut(raw, ":")
		if id, err := strconv.ParseInt(idStr, 10, 64); err == nil {
			f.Fields[id] = v
		}
	}
	f.NoField = ints("field__none")
	f.Sort = q.Get("sort")
	if f.Sort == "" {
		if st, err := s.store.GetSettings(ctx, uid); err == nil {
			f.Sort = st.DefaultSort
		}
	}
	f.Limit = clampInt(queryInt(r, "limit"), 0, 500)
	if f.Limit == 0 {
		f.Limit = clampInt(queryInt(r, "page_size"), 0, 500)
	}
	if f.Limit == 0 {
		f.Limit = 50
	}
	f.Offset = queryInt(r, "offset")
	if pg := queryInt(r, "page"); pg > 1 && f.Offset == 0 {
		f.Offset = (pg - 1) * f.Limit
	}
	f.WithContent = queryBool(r, "content")
	f.NoFacets = queryBool(r, "no_facets")
	return f, p
}

func (s *Server) handleListDocuments(w http.ResponseWriter, r *http.Request) {
	f, p := s.listFilter(r)
	docs, total, facets, err := s.store.ListDocuments(r.Context(), f)
	if err != nil {
		s.fail(w, err, "list documents")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": docs, "count": total, "facets": facets, "offset": f.Offset, "limit": f.Limit, "query": p, "all": nil})
}

func (s *Server) handleNextASN(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]int64{"next_asn": s.store.NextASN(r.Context(), me(r))})
}

// ---- one document ----

func (s *Server) handleGetDocument(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), me(r)
	d, _, err := s.docAccess(r, idParam(r, "id"), true)
	if err != nil {
		s.fail(w, err, "document")
		return
	}
	prev, next := int64(0), int64(0)
	if d.UserID == uid {
		prev, next = s.store.Neighbours(ctx, uid, d.ID)
	}
	out := map[string]any{"document": d, "prev": prev, "next": next}
	if d.Status == "processing" || d.Status == "pending" {
		if jid, ok := s.processing.Load(d.ID); ok {
			if j, err := s.store.GetJob(ctx, jid.(int64)); err == nil {
				out["job"] = j
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

type documentIn struct {
	Title           *string             `json:"title"`
	Content         *string             `json:"content"`
	Correspondent   *int64              `json:"correspondent"`
	DocumentType    *int64              `json:"document_type"`
	StoragePath     *int64              `json:"storage_path"`
	ASN             *int64              `json:"asn"`
	Created         *string             `json:"created"`
	Favorite        *bool               `json:"favorite"`
	Tags            *[]int64            `json:"tags"`
	CustomFields    *[]store.FieldValue `json:"custom_fields"`
	RemoveInboxTags bool                `json:"remove_inbox_tags"`
}

func (s *Server) changesFrom(keys map[string]bool, in documentIn) store.DocChanges {
	c := store.DocChanges{Title: in.Title, Content: in.Content, Favorite: in.Favorite, Tags: in.Tags, Fields: in.CustomFields}
	if keys["correspondent"] {
		c.CorrespondentID = &in.Correspondent
	}
	if keys["document_type"] {
		c.DocumentTypeID = &in.DocumentType
	}
	if keys["storage_path"] {
		c.StoragePathID = &in.StoragePath
	}
	if keys["asn"] {
		c.ASN = &in.ASN
	}
	if in.Created != nil {
		v := normalizeDate(*in.Created)
		c.Created = &v
	}
	if in.Title != nil {
		t := strings.TrimSpace(*in.Title)
		if t == "" {
			t = "Untitled"
		}
		c.Title = &t
	}
	return c
}

// normalizeDate accepts YYYY-MM-DD, an RFC 3339 stamp, or dd.mm.yyyy.
func normalizeDate(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 10 {
		if _, err := time.Parse("2006-01-02", v[:10]); err == nil {
			return v[:10]
		}
	}
	if t, err := time.Parse("02.01.2006", v); err == nil {
		return t.Format("2006-01-02")
	}
	if t, err := time.Parse("2.1.2006", v); err == nil {
		return t.Format("2006-01-02")
	}
	return store.Today()
}

func (s *Server) handleUpdateDocument(w http.ResponseWriter, r *http.Request) {
	ctx, uid, id := r.Context(), me(r), idParam(r, "id")
	if d := s.docForEdit(w, r, false); d == nil {
		return
	} else if d.UserID != uid {
		// an editor of a shared document changes the owner's document
		uid = d.UserID
	}
	var in documentIn
	keys, err := decodeKeys(r, &in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	c := s.changesFrom(keys, in)
	if in.RemoveInboxTags {
		c.RemoveTags = s.store.InboxTagIDs(ctx, uid)
	}
	if err := s.applyChanges(ctx, uid, id, c, actorName(r), "updated"); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeErr(w, http.StatusConflict, "that archive serial number is already in use")
			return
		}
		s.fail(w, err, "update document")
		return
	}
	d, err := s.store.GetDocument(ctx, uid, id, true)
	if err != nil {
		s.fail(w, err, "document")
		return
	}
	if d.UserID != me(r) {
		d.Access = s.store.SharedLevel(ctx, d.ID, me(r))
	}
	writeJSON(w, http.StatusOK, d)
}

// applyChanges saves, records history, re-mirrors, retrains and publishes.
func (s *Server) applyChanges(ctx contextT, uid, id int64, c store.DocChanges, actor, action string) error {
	changed, err := s.store.UpdateDocument(ctx, uid, id, c)
	if err != nil {
		return err
	}
	if len(changed) == 0 {
		return nil
	}
	s.store.AddHistory(ctx, id, uid, actor, action, changed)
	s.classifiers.Invalidate(uid)
	if d, err := s.store.GetDocument(ctx, uid, id, false); err == nil {
		if _, ok := changed["content"]; !ok {
			_ = s.placeInFolder(ctx, d)
		}
		fields := make([]string, 0, len(changed))
		for k := range changed {
			fields = append(fields, k)
		}
		sort.Strings(fields)
		s.publish(uid, "documents.updated", "document:"+strconv.FormatInt(id, 10), map[string]any{"id": id, "title": d.Title, "fields": fields, "actor": actor})
		if action == "updated" {
			s.runRules(ctx, d, "updated", actor)
		}
	}
	return nil
}

func (s *Server) handleDeleteDocument(w http.ResponseWriter, r *http.Request) {
	ctx, uid, id := r.Context(), me(r), idParam(r, "id")
	d, err := s.store.GetDocument(ctx, uid, id, false)
	if err != nil {
		s.fail(w, err, "document")
		return
	}
	if err := s.store.SoftDeleteDocument(ctx, uid, id); err != nil {
		s.fail(w, err, "delete document")
		return
	}
	s.unplace(ctx, d)
	s.store.AddHistory(ctx, id, uid, actorName(r), "deleted", nil)
	s.classifiers.Invalidate(uid)
	s.publish(uid, "documents.deleted", "document:"+strconv.FormatInt(id, 10), map[string]any{"id": id, "title": d.Title})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRestoreDocument(w http.ResponseWriter, r *http.Request) {
	ctx, uid, id := r.Context(), me(r), idParam(r, "id")
	if err := s.store.RestoreDocument(ctx, uid, id); err != nil {
		s.fail(w, err, "restore document")
		return
	}
	s.store.AddHistory(ctx, id, uid, actorName(r), "restored", nil)
	if d, err := s.store.GetDocument(ctx, uid, id, false); err == nil {
		_ = s.placeInFolder(ctx, d)
		writeJSON(w, http.StatusOK, d)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePurgeDocument(w http.ResponseWriter, r *http.Request) {
	ctx, uid, id := r.Context(), me(r), idParam(r, "id")
	d, err := s.store.GetDocument(ctx, uid, id, false)
	if err != nil {
		s.fail(w, err, "document")
		return
	}
	if d.DeletedAt == nil && !queryBool(r, "force") {
		writeErr(w, http.StatusBadRequest, "move it to the trash first (or add ?force=1)")
		return
	}
	if err := s.purgeDocument(ctx, d); err != nil {
		s.fail(w, err, "purge document")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- files ----

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	d := s.docForFile(w, r)
	if d == nil {
		return
	}
	if queryBool(r, "original") || !d.HasArchive {
		name := safeFileName(d.Title)
		if d.OriginalExt != "" {
			name += "." + d.OriginalExt
		}
		serveFile(w, r, s.originalPath(d), name, firstNonEmpty(d.Mime, "application/octet-stream"), !queryBool(r, "inline"))
		return
	}
	path, name, ctype := s.bestFile(d)
	serveFile(w, r, path, name, ctype, !queryBool(r, "inline"))
}

func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request) {
	d := s.docForFile(w, r)
	if d == nil {
		return
	}
	path, name, ctype := s.bestFile(d)
	serveFile(w, r, path, name, ctype, false)
}

func (s *Server) handleThumb(w http.ResponseWriter, r *http.Request) {
	d := s.docForFile(w, r)
	if d == nil {
		return
	}
	s.serveThumb(w, r, d)
}

func (s *Server) serveThumb(w http.ResponseWriter, r *http.Request, d *store.Document) {
	p := s.thumbPath(d.ID)
	if _, err := os.Stat(p); err != nil {
		// no thumbnail yet: a page render may still work
		if p2, err := s.pageImage(r.Context(), d, 1); err == nil {
			p = p2
		} else {
			w.Header().Set("Cache-Control", "no-cache")
			writeErr(w, http.StatusNotFound, "no thumbnail")
			return
		}
	}
	serveFile(w, r, p, "thumb.jpg", "image/jpeg", false)
}

func (s *Server) handlePageImage(w http.ResponseWriter, r *http.Request) {
	d := s.docForFile(w, r)
	if d == nil {
		return
	}
	n, _ := strconv.Atoi(strings.TrimSuffix(r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:], ".jpg"))
	p, err := s.pageImage(r.Context(), d, n)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "no such page")
			return
		}
		s.fail(w, err, "render page")
		return
	}
	serveFile(w, r, p, "page.jpg", "image/jpeg", false)
}

func (s *Server) handleText(w http.ResponseWriter, r *http.Request) {
	d, _, err := s.docAccess(r, idParam(r, "id"), true)
	if err != nil {
		s.fail(w, err, "document")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(d.Content))
}

func (s *Server) handleReprocess(w http.ResponseWriter, r *http.Request) {
	ctx, uid, id := r.Context(), me(r), idParam(r, "id")
	d, err := s.store.GetDocument(ctx, uid, id, false)
	if err != nil {
		s.fail(w, err, "document")
		return
	}
	var in struct {
		Mode     string `json:"mode"` // skip | redo | force | "" = settings
		Force    bool   `json:"force_text"`
		Language string `json:"language"` // tesseract codes joined with +; "" = the box default; saved on the document
	}
	_ = decode(r, &in)
	if lang := strings.Trim(strings.ToLower(strings.ReplaceAll(in.Language, " ", "")), "+"); lang != d.Language && (lang != "" || in.Language != "") {
		_, _ = s.store.UpdateDocument(ctx, uid, id, store.DocChanges{Language: &lang})
	}
	j, err := s.enqueueProcess(ctx, d, "reprocess", map[string]any{"mode": in.Mode, "force_text": in.Force, "actor": actorName(r)})
	if err != nil {
		s.fail(w, err, "queue")
		return
	}
	writeJSON(w, http.StatusAccepted, j)
}

// ---- suggestions ----

// Suggestions are what the matchers and the classifier would assign, plus
// dates found in the text; the UI shows them as chips to tap.
type suggestions struct {
	Tags           []int64            `json:"tags"`
	Correspondents []int64            `json:"correspondents"`
	DocumentTypes  []int64            `json:"document_types"`
	StoragePaths   []int64            `json:"storage_paths"`
	Dates          []string           `json:"dates"`
	Confidence     map[string]float64 `json:"confidence"`
	TrainedOn      int                `json:"trained_on"`
}

func (s *Server) suggestFor(ctx contextT, uid int64, d *store.Document) suggestions {
	out := suggestions{Tags: []int64{}, Correspondents: []int64{}, DocumentTypes: []int64{}, StoragePaths: []int64{}, Dates: []string{}, Confidence: map[string]float64{}}
	text := d.Title + "\n" + d.OriginalName + "\n" + d.Content
	tags, _ := s.store.ListTags(ctx, uid)
	corr, _ := s.store.ListCorrespondents(ctx, uid)
	types, _ := s.store.ListDocumentTypes(ctx, uid)
	paths, _ := s.store.ListStoragePaths(ctx, uid)
	seen := map[string]bool{}
	add := func(list *[]int64, key string, id int64, conf float64) {
		k := key + ":" + strconv.FormatInt(id, 10)
		if seen[k] {
			return
		}
		seen[k] = true
		*list = append(*list, id)
		if conf > 0 {
			out.Confidence[k] = conf
		}
	}
	for _, t := range tags {
		if match.Matches(t.Matchable, text) {
			add(&out.Tags, "tag", t.ID, 1)
		}
	}
	for _, c := range corr {
		if match.Matches(c.Matchable, text) {
			add(&out.Correspondents, "correspondent", c.ID, 1)
		}
	}
	for _, t := range types {
		if match.Matches(t.Matchable, text) {
			add(&out.DocumentTypes, "document_type", t.ID, 1)
		}
	}
	for _, p := range paths {
		if match.Matches(p.Matchable, text) {
			add(&out.StoragePaths, "storage_path", p.ID, 1)
		}
	}
	// the learned part, only for items set to "auto"
	auto := func(m store.Matchable) bool { return m.MatchingAlgorithm == store.MatchAuto }
	cl, err := s.classifiers.Get(uid, func() ([]store.TrainingDoc, error) { return s.store.TrainingDocs(ctx, uid) })
	if err == nil && cl != nil {
		sg := cl.Suggest(text, 0.6)
		out.TrainedOn = sg.TrainedOn
		for _, p := range sg.Tags {
			for _, t := range tags {
				if t.ID == p.ID && auto(t.Matchable) {
					add(&out.Tags, "tag", t.ID, p.Confidence)
				}
			}
		}
		if sg.Correspondent != nil {
			for _, c := range corr {
				if c.ID == sg.Correspondent.ID && auto(c.Matchable) {
					add(&out.Correspondents, "correspondent", c.ID, sg.Correspondent.Confidence)
				}
			}
		}
		if sg.DocumentType != nil {
			for _, t := range types {
				if t.ID == sg.DocumentType.ID && auto(t.Matchable) {
					add(&out.DocumentTypes, "document_type", t.ID, sg.DocumentType.Confidence)
				}
			}
		}
		if sg.StoragePath != nil {
			for _, p := range paths {
				if p.ID == sg.StoragePath.ID && auto(p.Matchable) {
					add(&out.StoragePaths, "storage_path", p.ID, sg.StoragePath.Confidence)
				}
			}
		}
	}
	for i, dt := range ocr.FindDates(d.Content, true) {
		if i >= 8 {
			break
		}
		out.Dates = append(out.Dates, dt.Format("2006-01-02"))
	}
	return out
}

func (s *Server) handleSuggestions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d, _, err := s.docAccess(r, idParam(r, "id"), true)
	if err != nil {
		s.fail(w, err, "document")
		return
	}
	writeJSON(w, http.StatusOK, s.suggestFor(ctx, d.UserID, d))
}

// handleApplySuggestions assigns everything suggested that is not set yet.
func (s *Server) handleApplySuggestions(w http.ResponseWriter, r *http.Request) {
	ctx, uid, id := r.Context(), me(r), idParam(r, "id")
	d, err := s.store.GetDocument(ctx, uid, id, true)
	if err != nil {
		s.fail(w, err, "document")
		return
	}
	sg := s.suggestFor(ctx, uid, d)
	c := store.DocChanges{AddTags: sg.Tags}
	if d.CorrespondentID == nil && len(sg.Correspondents) > 0 {
		v := sg.Correspondents[0]
		p := &v
		c.CorrespondentID = &p
	}
	if d.DocumentTypeID == nil && len(sg.DocumentTypes) > 0 {
		v := sg.DocumentTypes[0]
		p := &v
		c.DocumentTypeID = &p
	}
	if d.StoragePathID == nil && len(sg.StoragePaths) > 0 {
		v := sg.StoragePaths[0]
		p := &v
		c.StoragePathID = &p
	}
	if err := s.applyChanges(ctx, uid, id, c, actorName(r), "suggestions"); err != nil {
		s.fail(w, err, "apply suggestions")
		return
	}
	d, _ = s.store.GetDocument(ctx, uid, id, true)
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) handleSimilar(w http.ResponseWriter, r *http.Request) {
	limit := clampInt(queryInt(r, "limit"), 0, 50)
	if limit == 0 {
		limit = 10
	}
	docs, err := s.store.Similar(r.Context(), me(r), idParam(r, "id"), limit)
	if err != nil {
		s.fail(w, err, "similar")
		return
	}
	writeJSON(w, http.StatusOK, docs)
}

// ---- notes & history ----

func (s *Server) handleListNotes(w http.ResponseWriter, r *http.Request) {
	d := s.docForFile(w, r)
	if d == nil {
		return
	}
	notes, err := s.store.ListNotes(r.Context(), d.ID)
	if err != nil {
		s.fail(w, err, "notes")
		return
	}
	writeJSON(w, http.StatusOK, notes)
}

func (s *Server) handleAddNote(w http.ResponseWriter, r *http.Request) {
	d := s.docForEdit(w, r, false)
	if d == nil {
		return
	}
	var in struct {
		Text string `json:"text"`
		Note string `json:"note"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	n := &store.Note{DocumentID: d.ID, UserID: me(r), Text: firstNonEmpty(in.Text, in.Note)}
	if err := s.store.AddNote(r.Context(), n); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.store.AddHistory(r.Context(), d.ID, me(r), actorName(r), "note", map[string]any{"text": n.Text})
	writeJSON(w, http.StatusCreated, n)
}

func (s *Server) handleDeleteNote(w http.ResponseWriter, r *http.Request) {
	d := s.docForEdit(w, r, false)
	if d == nil {
		return
	}
	if err := s.store.DeleteNote(r.Context(), d.ID, idParam(r, "nid")); err != nil {
		s.fail(w, err, "delete note")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	d := s.docForFile(w, r)
	if d == nil {
		return
	}
	h, err := s.store.ListHistory(r.Context(), d.ID, 300)
	if err != nil {
		s.fail(w, err, "history")
		return
	}
	writeJSON(w, http.StatusOK, h)
}

func (s *Server) handleDocumentJobs(w http.ResponseWriter, r *http.Request) {
	d := s.docForFile(w, r)
	if d == nil {
		return
	}
	jobs, err := s.store.ListJobs(r.Context(), me(r), "", 500)
	if err != nil {
		s.fail(w, err, "jobs")
		return
	}
	out := []store.Job{}
	for _, j := range jobs {
		if j.DocumentID != nil && *j.DocumentID == d.ID {
			if full, err := s.store.GetJob(r.Context(), j.ID); err == nil {
				out = append(out, *full)
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- bulk ----

type bulkIn struct {
	IDs           []int64            `json:"ids"`
	Op            string             `json:"op"`
	Tag           *int64             `json:"tag"`
	AddTags       []int64            `json:"add_tags"`
	RemoveTags    []int64            `json:"remove_tags"`
	Correspondent *int64             `json:"correspondent"`
	DocumentType  *int64             `json:"document_type"`
	StoragePath   *int64             `json:"storage_path"`
	Favorite      *bool              `json:"favorite"`
	Fields        []store.FieldValue `json:"custom_fields"`
	RemoveFields  []int64            `json:"remove_fields"`
	Mode          string             `json:"mode"`
}

func (s *Server) handleBulk(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), me(r)
	var in bulkIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if len(in.IDs) == 0 {
		writeErr(w, http.StatusBadRequest, "no documents chosen")
		return
	}
	if len(in.IDs) > 2000 {
		writeErr(w, http.StatusBadRequest, "too many at once")
		return
	}
	actor := actorName(r)
	done, failed := 0, 0
	for _, id := range in.IDs {
		var err error
		switch in.Op {
		case "add_tag", "remove_tag", "modify_tags", "set_correspondent", "set_document_type", "set_storage_path", "set_favorite", "set_fields":
			c := store.DocChanges{}
			switch in.Op {
			case "add_tag":
				if in.Tag != nil {
					c.AddTags = []int64{*in.Tag}
				}
				c.AddTags = append(c.AddTags, in.AddTags...)
			case "remove_tag":
				if in.Tag != nil {
					c.RemoveTags = []int64{*in.Tag}
				}
				c.RemoveTags = append(c.RemoveTags, in.RemoveTags...)
			case "modify_tags":
				c.AddTags, c.RemoveTags = in.AddTags, in.RemoveTags
			case "set_correspondent":
				c.CorrespondentID = &in.Correspondent
			case "set_document_type":
				c.DocumentTypeID = &in.DocumentType
			case "set_storage_path":
				c.StoragePathID = &in.StoragePath
			case "set_favorite":
				c.Favorite = in.Favorite
			case "set_fields":
				c.SetFields, c.RemoveFields = in.Fields, in.RemoveFields
			}
			err = s.applyChanges(ctx, uid, id, c, actor, "bulk")
		case "delete":
			var d *store.Document
			if d, err = s.store.GetDocument(ctx, uid, id, false); err == nil {
				if err = s.store.SoftDeleteDocument(ctx, uid, id); err == nil {
					s.unplace(ctx, d)
					s.store.AddHistory(ctx, id, uid, actor, "deleted", nil)
					s.publish(uid, "documents.deleted", "document:"+strconv.FormatInt(id, 10), map[string]any{"id": id, "title": d.Title})
				}
			}
		case "restore":
			if err = s.store.RestoreDocument(ctx, uid, id); err == nil {
				s.store.AddHistory(ctx, id, uid, actor, "restored", nil)
				if d, e := s.store.GetDocument(ctx, uid, id, false); e == nil {
					_ = s.placeInFolder(ctx, d)
				}
			}
		case "purge":
			var d *store.Document
			if d, err = s.store.GetDocument(ctx, uid, id, false); err == nil {
				err = s.purgeDocument(ctx, d)
			}
		case "reprocess":
			var d *store.Document
			if d, err = s.store.GetDocument(ctx, uid, id, false); err == nil {
				_, err = s.enqueueProcess(ctx, d, "reprocess", map[string]any{"mode": in.Mode, "actor": actor})
			}
		case "remove_inbox":
			err = s.applyChanges(ctx, uid, id, store.DocChanges{RemoveTags: s.store.InboxTagIDs(ctx, uid)}, actor, "bulk")
		default:
			writeErr(w, http.StatusBadRequest, "unknown op "+in.Op)
			return
		}
		if err != nil {
			failed++
			s.log.Warn("bulk", "op", in.Op, "id", id, "err", err)
		} else {
			done++
		}
	}
	s.classifiers.Invalidate(uid)
	writeJSON(w, http.StatusOK, map[string]int{"done": done, "failed": failed})
}
