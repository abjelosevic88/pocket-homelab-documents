package server

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-documents/internal/store"
)

// ---- export: one zip with everything of a person ----

type exportManifest struct {
	App            string              `json:"app"`
	Version        string              `json:"version"`
	ExportedAt     string              `json:"exported_at"`
	Tags           []store.Tag         `json:"tags"`
	Correspondents []store.Named       `json:"correspondents"`
	DocumentTypes  []store.Named       `json:"document_types"`
	StoragePaths   []store.Named       `json:"storage_paths"`
	CustomFields   []store.CustomField `json:"custom_fields"`
	SavedViews     []store.SavedView   `json:"saved_views"`
	Rules          []store.Rule        `json:"rules"`
	Documents      []exportDoc         `json:"documents"`
}

type exportDoc struct {
	store.Document
	Content  string       `json:"content"`
	Notes    []store.Note `json:"notes"`
	Original string       `json:"original_file"`
	Archive  string       `json:"archive_file,omitempty"`
}

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), me(r)
	withArchive := !queryBool(r, "no_archive")
	m := exportManifest{App: "pocket-documents", Version: Version, ExportedAt: store.Now()}
	m.Tags, _ = s.store.ListTags(ctx, uid)
	m.Correspondents, _ = s.store.ListCorrespondents(ctx, uid)
	m.DocumentTypes, _ = s.store.ListDocumentTypes(ctx, uid)
	m.StoragePaths, _ = s.store.ListStoragePaths(ctx, uid)
	m.CustomFields, _ = s.store.ListCustomFields(ctx, uid)
	m.SavedViews, _ = s.store.ListSavedViews(ctx, uid)
	m.Rules, _ = s.store.ListRules(ctx, uid)
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="pocket-documents-export-`+time.Now().Format("20060102")+`.zip"`)
	zw := zip.NewWriter(w)
	add := func(name, path string) bool {
		f, err := os.Open(path)
		if err != nil {
			return false
		}
		defer f.Close()
		fi, _ := f.Stat()
		h := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: fi.ModTime()}
		if strings.HasSuffix(name, ".pdf") || strings.HasSuffix(name, ".jpg") || strings.HasSuffix(name, ".png") {
			h.Method = zip.Store
		}
		zf, err := zw.CreateHeader(h)
		if err != nil {
			return false
		}
		_, err = io.Copy(zf, f)
		return err == nil
	}
	for _, id := range s.store.DocumentIDs(ctx, uid, false) {
		d, err := s.store.GetDocument(ctx, uid, id, true)
		if err != nil {
			continue
		}
		ed := exportDoc{Document: *d, Content: d.Content}
		ed.Document.Content = ""
		ed.Notes, _ = s.store.ListNotes(ctx, id)
		ed.Original = fmt.Sprintf("originals/%d.%s", d.ID, firstNonEmpty(d.OriginalExt, "bin"))
		if !add(ed.Original, s.originalPath(d)) {
			ed.Original = ""
		}
		if withArchive && d.HasArchive {
			ed.Archive = fmt.Sprintf("archive/%d.pdf", d.ID)
			if !add(ed.Archive, s.archivePath(d.ID)) {
				ed.Archive = ""
			}
		}
		m.Documents = append(m.Documents, ed)
	}
	mf, _ := zw.Create("manifest.json")
	enc := json.NewEncoder(mf)
	enc.SetIndent("", " ")
	_ = enc.Encode(m)
	_ = zw.Close()
}

// ---- import: our own zip, or a Paperless-ngx document_exporter folder zipped ----

func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), me(r)
	mr, err := r.MultipartReader()
	if err != nil {
		writeErr(w, http.StatusBadRequest, "send the zip as multipart/form-data")
		return
	}
	var part *multipart.Part
	for {
		p, err := mr.NextPart()
		if err != nil {
			writeErr(w, http.StatusBadRequest, "no file in the request")
			return
		}
		if p.FileName() != "" {
			part = p
			break
		}
	}
	tmp, err := os.CreateTemp(s.cfg.TmpDir, "import-*.zip")
	if err != nil {
		s.fail(w, err, "import")
		return
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, io.LimitReader(part, 20<<30)); err != nil {
		tmp.Close()
		s.fail(w, err, "import")
		return
	}
	tmp.Close()
	res, err := s.importZip(ctx, uid, tmp.Name(), actorName(r))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

type importResult struct {
	Documents int      `json:"documents"`
	Skipped   int      `json:"skipped"`
	Tags      int      `json:"tags"`
	Errors    []string `json:"errors"`
}

// importZip reads manifest.json: ours, or Paperless' Django fixture.
func (s *Server) importZip(ctx context.Context, uid int64, path, actor string) (*importResult, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, errors.New("not a zip file")
	}
	defer zr.Close()
	files := map[string]*zip.File{}
	var manifest *zip.File
	for _, f := range zr.File {
		name := strings.TrimPrefix(f.Name, "./")
		files[name] = f
		if filepath.Base(name) == "manifest.json" && (manifest == nil || len(name) < len(manifest.Name)) {
			manifest = f
		}
	}
	if manifest == nil {
		return nil, errors.New("no manifest.json in the zip")
	}
	prefix := strings.TrimSuffix(strings.TrimPrefix(manifest.Name, "./"), "manifest.json")
	rc, err := manifest.Open()
	if err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(rc, 512<<20))
	rc.Close()
	if err != nil {
		return nil, err
	}
	extractTo := func(name string) (string, error) {
		f, ok := files[prefix+name]
		if !ok {
			f, ok = files[name]
		}
		if !ok {
			return "", fmt.Errorf("%s not in the zip", name)
		}
		rc, err := f.Open()
		if err != nil {
			return "", err
		}
		defer rc.Close()
		out, err := os.CreateTemp(s.cfg.TmpDir, "imp-*"+filepath.Ext(name))
		if err != nil {
			return "", err
		}
		_, err = io.Copy(out, rc)
		out.Close()
		if err != nil {
			os.Remove(out.Name())
			return "", err
		}
		return out.Name(), nil
	}
	if strings.HasPrefix(strings.TrimSpace(string(raw)), "[") {
		return s.importPaperlessFixture(ctx, uid, raw, extractTo, actor)
	}
	var m exportManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, errors.New("manifest.json is not readable")
	}
	res := &importResult{Errors: []string{}}
	tagMap := map[int64]int64{}
	for _, t := range m.Tags {
		if ex, err := s.store.FindTag(ctx, uid, t.Name); err == nil {
			tagMap[t.ID] = ex.ID
			continue
		}
		nt := store.Tag{UserID: uid, Name: t.Name, Color: validColor(t.Color), Matchable: t.Matchable, IsInbox: t.IsInbox}
		if err := s.store.CreateTag(ctx, &nt); err == nil {
			tagMap[t.ID] = nt.ID
			res.Tags++
		}
	}
	named := func(list []store.Named, find func(context.Context, int64, string) (*store.Named, error), create func(context.Context, *store.Named) error) map[int64]int64 {
		out := map[int64]int64{}
		for _, n := range list {
			if ex, err := find(ctx, uid, n.Name); err == nil {
				out[n.ID] = ex.ID
				continue
			}
			nn := store.Named{UserID: uid, Name: n.Name, Path: n.Path, Matchable: n.Matchable}
			if err := create(ctx, &nn); err == nil {
				out[n.ID] = nn.ID
			}
		}
		return out
	}
	corrMap := named(m.Correspondents, s.store.FindCorrespondent, s.store.CreateCorrespondent)
	typeMap := named(m.DocumentTypes, s.store.FindDocumentType, s.store.CreateDocumentType)
	pathMap := named(m.StoragePaths, s.store.FindStoragePath, s.store.CreateStoragePath)
	fieldMap := map[int64]int64{}
	for _, f := range m.CustomFields {
		if ex, err := s.store.FindCustomField(ctx, uid, f.Name); err == nil {
			fieldMap[f.ID] = ex.ID
			continue
		}
		nf := store.CustomField{UserID: uid, Name: f.Name, DataType: f.DataType, Extra: f.Extra, Position: f.Position}
		if err := s.store.CreateCustomField(ctx, &nf); err == nil {
			fieldMap[f.ID] = nf.ID
		}
	}
	for _, v := range m.SavedViews {
		nv := store.SavedView{UserID: uid, Name: v.Name, Query: v.Query, OnDashboard: v.OnDashboard, InSidebar: v.InSidebar, Position: v.Position}
		_ = s.store.CreateSavedView(ctx, &nv)
	}
	for _, rl := range m.Rules {
		nr := store.Rule{UserID: uid, Name: rl.Name, Enabled: rl.Enabled, Trigger: rl.Trigger, Position: rl.Position, Filters: rl.Filters, Actions: rl.Actions}
		nr.Actions.AddTags = mapIDs(nr.Actions.AddTags, tagMap)
		nr.Actions.RemoveTags = mapIDs(nr.Actions.RemoveTags, tagMap)
		nr.Filters.Tags = mapIDs(nr.Filters.Tags, tagMap)
		nr.Actions.Correspondent = mapID(nr.Actions.Correspondent, corrMap)
		nr.Actions.DocumentType = mapID(nr.Actions.DocumentType, typeMap)
		nr.Actions.StoragePath = mapID(nr.Actions.StoragePath, pathMap)
		nr.Filters.Correspondent = mapID(nr.Filters.Correspondent, corrMap)
		nr.Filters.DocumentType = mapID(nr.Filters.DocumentType, typeMap)
		_ = s.store.CreateRule(ctx, &nr)
	}
	for _, ed := range m.Documents {
		if ed.Original == "" {
			res.Errors = append(res.Errors, ed.Title+": no file in the export")
			continue
		}
		tmp, err := extractTo(ed.Original)
		if err != nil {
			res.Errors = append(res.Errors, ed.Title+": "+err.Error())
			continue
		}
		in := incoming{Path: tmp, Name: firstNonEmpty(ed.OriginalName, filepath.Base(ed.Original)), Title: ed.Title, Created: ed.Created, ASN: ed.ASN, Tags: mapIDs(ed.Tags, tagMap),
			Correspondent: mapID(ed.CorrespondentID, corrMap), DocumentType: mapID(ed.DocumentTypeID, typeMap), StoragePath: mapID(ed.StoragePathID, pathMap),
			Source: "import", Actor: actor, SkipMatching: true, Content: ed.Content, AddedAt: ed.AddedAt}
		for _, fv := range ed.Fields {
			if nid, ok := fieldMap[fv.FieldID]; ok {
				in.Fields = append(in.Fields, store.FieldValue{FieldID: nid, Value: fv.Value})
			}
		}
		d, err := s.ingest(ctx, uid, in)
		if err != nil {
			var dup *ErrDuplicate
			if errors.As(err, &dup) {
				res.Skipped++
				continue
			}
			res.Errors = append(res.Errors, ed.Title+": "+err.Error())
			continue
		}
		for _, n := range ed.Notes {
			_ = s.store.AddNote(ctx, &store.Note{DocumentID: d.ID, UserID: uid, Text: n.Text})
		}
		if ed.Favorite {
			t := true
			_, _ = s.store.UpdateDocument(ctx, uid, d.ID, store.DocChanges{Favorite: &t})
		}
		res.Documents++
	}
	s.classifiers.Invalidate(uid)
	return res, nil
}

func mapIDs(ids []int64, m map[int64]int64) []int64 {
	out := []int64{}
	for _, id := range ids {
		if n, ok := m[id]; ok {
			out = append(out, n)
		}
	}
	return out
}

func mapID(id *int64, m map[int64]int64) *int64 {
	if id == nil {
		return nil
	}
	if n, ok := m[*id]; ok {
		return &n
	}
	return nil
}

// importPaperlessFixture reads the manifest the Paperless-ngx document_exporter writes.
func (s *Server) importPaperlessFixture(ctx context.Context, uid int64, raw []byte, extractTo func(string) (string, error), actor string) (*importResult, error) {
	var rows []struct {
		Model  string          `json:"model"`
		PK     int64           `json:"pk"`
		Fields json.RawMessage `json:"fields"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, errors.New("manifest.json is not a Paperless export")
	}
	res := &importResult{Errors: []string{}}
	tagMap, corrMap, typeMap, pathMap, fieldMap := map[int64]int64{}, map[int64]int64{}, map[int64]int64{}, map[int64]int64{}, map[int64]int64{}
	type matchFields struct {
		Name          string          `json:"name"`
		Color         string          `json:"color"`
		Match         string          `json:"match"`
		Algorithm     int             `json:"matching_algorithm"`
		IsInsensitive bool            `json:"is_insensitive"`
		IsInbox       bool            `json:"is_inbox_tag"`
		Path          string          `json:"path"`
		DataType      string          `json:"data_type"`
		Extra         json.RawMessage `json:"extra_data"`
	}
	// taxonomy first
	for _, r := range rows {
		var f matchFields
		_ = json.Unmarshal(r.Fields, &f)
		m := store.Matchable{Match: f.Match, MatchingAlgorithm: f.Algorithm, IsInsensitive: f.IsInsensitive}
		switch r.Model {
		case "documents.tag":
			if ex, err := s.store.FindTag(ctx, uid, f.Name); err == nil {
				tagMap[r.PK] = ex.ID
			} else {
				t := store.Tag{UserID: uid, Name: f.Name, Color: validColor(f.Color), Matchable: m, IsInbox: f.IsInbox}
				if s.store.CreateTag(ctx, &t) == nil {
					tagMap[r.PK] = t.ID
					res.Tags++
				}
			}
		case "documents.correspondent":
			if ex, err := s.store.FindCorrespondent(ctx, uid, f.Name); err == nil {
				corrMap[r.PK] = ex.ID
			} else {
				n := store.Named{UserID: uid, Name: f.Name, Matchable: m}
				if s.store.CreateCorrespondent(ctx, &n) == nil {
					corrMap[r.PK] = n.ID
				}
			}
		case "documents.documenttype":
			if ex, err := s.store.FindDocumentType(ctx, uid, f.Name); err == nil {
				typeMap[r.PK] = ex.ID
			} else {
				n := store.Named{UserID: uid, Name: f.Name, Matchable: m}
				if s.store.CreateDocumentType(ctx, &n) == nil {
					typeMap[r.PK] = n.ID
				}
			}
		case "documents.storagepath":
			if ex, err := s.store.FindStoragePath(ctx, uid, f.Name); err == nil {
				pathMap[r.PK] = ex.ID
			} else {
				n := store.Named{UserID: uid, Name: f.Name, Path: paperlessPathTemplate(f.Path), Matchable: m}
				if s.store.CreateStoragePath(ctx, &n) == nil {
					pathMap[r.PK] = n.ID
				}
			}
		case "documents.customfield":
			dt := paperlessFieldType(f.DataType)
			if ex, err := s.store.FindCustomField(ctx, uid, f.Name); err == nil {
				fieldMap[r.PK] = ex.ID
			} else {
				cf := store.CustomField{UserID: uid, Name: f.Name, DataType: dt, Extra: paperlessFieldExtra(f.Extra)}
				if s.store.CreateCustomField(ctx, &cf) == nil {
					fieldMap[r.PK] = cf.ID
				}
			}
		}
	}
	// field instances and notes by document pk
	fields := map[int64][]store.FieldValue{}
	notes := map[int64][]string{}
	for _, r := range rows {
		switch r.Model {
		case "documents.customfieldinstance":
			var f struct {
				Document int64           `json:"document"`
				Field    int64           `json:"field"`
				Text     *string         `json:"value_text"`
				Bool     *bool           `json:"value_bool"`
				URL      *string         `json:"value_url"`
				Date     *string         `json:"value_date"`
				Int      *int64          `json:"value_int"`
				Float    *float64        `json:"value_float"`
				Money    *string         `json:"value_monetary"`
				Select   *string         `json:"value_select"`
				DocIDs   json.RawMessage `json:"value_document_ids"`
			}
			_ = json.Unmarshal(r.Fields, &f)
			nid, ok := fieldMap[f.Field]
			if !ok {
				continue
			}
			v := ""
			switch {
			case f.Text != nil:
				v = *f.Text
			case f.Bool != nil:
				v = strconv.FormatBool(*f.Bool)
			case f.URL != nil:
				v = *f.URL
			case f.Date != nil:
				v = *f.Date
			case f.Int != nil:
				v = strconv.FormatInt(*f.Int, 10)
			case f.Float != nil:
				v = trimFloat(*f.Float)
			case f.Money != nil:
				v = *f.Money
			case f.Select != nil:
				v = *f.Select
			}
			if v != "" {
				fields[f.Document] = append(fields[f.Document], store.FieldValue{FieldID: nid, Value: v})
			}
		case "documents.note":
			var f struct {
				Document int64  `json:"document"`
				Note     string `json:"note"`
			}
			_ = json.Unmarshal(r.Fields, &f)
			notes[f.Document] = append(notes[f.Document], f.Note)
		}
	}
	for _, r := range rows {
		if r.Model != "documents.document" {
			continue
		}
		var f struct {
			Title         string  `json:"title"`
			Content       string  `json:"content"`
			Created       string  `json:"created"`
			ASN           *int64  `json:"archive_serial_number"`
			Correspondent *int64  `json:"correspondent"`
			DocumentType  *int64  `json:"document_type"`
			StoragePath   *int64  `json:"storage_path"`
			Tags          []int64 `json:"tags"`
			Filename      string  `json:"original_filename"`
			DeletedAt     *string `json:"deleted_at"`
			Added         string  `json:"added"`
			ExportedFile  string  `json:"__exported_file_name__"`
		}
		_ = json.Unmarshal(r.Fields, &f)
		if f.DeletedAt != nil {
			continue
		}
		if f.ExportedFile == "" {
			res.Errors = append(res.Errors, f.Title+": no file in the export")
			continue
		}
		tmp, err := extractTo(f.ExportedFile)
		if err != nil {
			res.Errors = append(res.Errors, f.Title+": "+err.Error())
			continue
		}
		in := incoming{Path: tmp, Name: firstNonEmpty(f.Filename, filepath.Base(f.ExportedFile)), Title: f.Title, Created: normalizeDate(f.Created), ASN: f.ASN, Tags: mapIDs(f.Tags, tagMap),
			Correspondent: mapID(f.Correspondent, corrMap), DocumentType: mapID(f.DocumentType, typeMap), StoragePath: mapID(f.StoragePath, pathMap), Fields: fields[r.PK],
			Source: "paperless", Actor: actor, SkipMatching: true, Content: f.Content, ExternalID: fmt.Sprintf("paperless:%d", r.PK), AddedAt: f.Added}
		d, err := s.ingest(ctx, uid, in)
		if err != nil {
			var dup *ErrDuplicate
			if errors.As(err, &dup) {
				res.Skipped++
				continue
			}
			res.Errors = append(res.Errors, f.Title+": "+err.Error())
			continue
		}
		for _, n := range notes[r.PK] {
			_ = s.store.AddNote(ctx, &store.Note{DocumentID: d.ID, UserID: uid, Text: n})
		}
		res.Documents++
	}
	s.classifiers.Invalidate(uid)
	return res, nil
}

// paperlessPathTemplate turns Paperless' {created_year}/{correspondent}/{title} (also the
// Jinja form {{ created_year }}) into ours.
func paperlessPathTemplate(p string) string {
	p = strings.ReplaceAll(strings.ReplaceAll(p, "{{ ", "{"), " }}", "}")
	p = strings.ReplaceAll(strings.ReplaceAll(p, "{{", "{"), "}}", "}")
	p = strings.NewReplacer("{correspondent}", "{correspondent}", "{document_type}", "{document_type}", "{created_year_short}", "{created_year}", "{added_year}", "{added_year}", "{tag_list}", "{tag_list}", "{doc_pk}", "{id}").Replace(p)
	if strings.TrimSpace(p) == "" {
		return "{created_year}/{title}"
	}
	return p
}

func paperlessFieldType(t string) string {
	for _, x := range store.FieldTypes {
		if x == t {
			return t
		}
	}
	return "string"
}

func paperlessFieldExtra(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || !json.Valid(raw) {
		return json.RawMessage("{}")
	}
	var ex struct {
		Options []any  `json:"select_options"`
		Cur     string `json:"default_currency"`
	}
	_ = json.Unmarshal(raw, &ex)
	out := map[string]any{}
	if len(ex.Options) > 0 {
		var opts []string
		for _, o := range ex.Options {
			switch v := o.(type) {
			case string:
				opts = append(opts, v)
			case map[string]any:
				if l, ok := v["label"].(string); ok {
					opts = append(opts, l)
				}
			}
		}
		out["options"] = opts
	}
	if ex.Cur != "" {
		out["currency"] = ex.Cur
	}
	b, _ := json.Marshal(out)
	return b
}

// ---- import straight from a running Paperless-ngx over its API ----

type paperlessClient struct {
	base, token string
	http        *http.Client
}

func (c *paperlessClient) get(ctx context.Context, path string, q url.Values, v any) error {
	u := c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Accept", "application/json; version=9")
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == 401 || res.StatusCode == 403 {
		return errors.New("Paperless rejected the token")
	}
	if res.StatusCode != 200 {
		return fmt.Errorf("Paperless answered HTTP %d for %s", res.StatusCode, path)
	}
	return json.NewDecoder(res.Body).Decode(v)
}

// all pages of a Paperless list endpoint
func (c *paperlessClient) list(ctx context.Context, path string, into func(json.RawMessage)) error {
	next := path + "?page_size=100"
	for next != "" {
		var page struct {
			Next    *string           `json:"next"`
			Results []json.RawMessage `json:"results"`
		}
		u := next
		if strings.HasPrefix(u, "http") {
			// keep the path+query, use our base (Paperless may say its own host name)
			if pu, err := url.Parse(u); err == nil {
				u = pu.RequestURI()
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+u, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Token "+c.token)
		req.Header.Set("Accept", "application/json; version=9")
		res, err := c.http.Do(req)
		if err != nil {
			return err
		}
		if res.StatusCode != 200 {
			res.Body.Close()
			return fmt.Errorf("Paperless answered HTTP %d for %s", res.StatusCode, u)
		}
		err = json.NewDecoder(res.Body).Decode(&page)
		res.Body.Close()
		if err != nil {
			return err
		}
		for _, r := range page.Results {
			into(r)
		}
		next = ""
		if page.Next != nil {
			next = *page.Next
		}
	}
	return nil
}

func (c *paperlessClient) download(ctx context.Context, id int64, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/api/documents/%d/download/?original=true", c.base, id), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Token "+c.token)
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("download answered HTTP %d", res.StatusCode)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, res.Body)
	f.Close()
	return err
}

func (s *Server) paperlessFrom(r *http.Request) (*paperlessClient, error) {
	var in struct {
		URL   string `json:"url"`
		Token string `json:"token"`
	}
	if err := decode(r, &in); err != nil {
		return nil, errors.New("invalid json")
	}
	in.URL = strings.TrimRight(strings.TrimSpace(in.URL), "/")
	if in.URL == "" || strings.TrimSpace(in.Token) == "" {
		return nil, errors.New("the Paperless URL and an API token are needed")
	}
	return &paperlessClient{base: in.URL, token: strings.TrimSpace(in.Token), http: &http.Client{Timeout: 10 * time.Minute}}, nil
}

func (s *Server) handleTestPaperless(w http.ResponseWriter, r *http.Request) {
	c, err := s.paperlessFrom(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var out struct {
		Count int `json:"count"`
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if err := c.get(ctx, "/api/documents/", url.Values{"page_size": {"1"}}, &out); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "documents": out.Count})
}

func (s *Server) handleImportPaperless(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL   string `json:"url"`
		Token string `json:"token"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	in.URL = strings.TrimRight(strings.TrimSpace(in.URL), "/")
	if in.URL == "" || strings.TrimSpace(in.Token) == "" {
		writeErr(w, http.StatusBadRequest, "the Paperless URL and an API token are needed")
		return
	}
	j, err := s.store.EnqueueJob(r.Context(), me(r), nil, "import_paperless", map[string]any{"url": in.URL, "token": strings.TrimSpace(in.Token), "actor": actorName(r)})
	if err != nil {
		s.fail(w, err, "queue")
		return
	}
	s.nudge()
	writeJSON(w, http.StatusAccepted, j)
}

// paperlessImportJob pulls everything of the token's Paperless user.
func (s *Server) paperlessImportJob(ctx context.Context, j *store.Job) error {
	var p struct {
		URL, Token, Actor string
	}
	_ = json.Unmarshal(j.Params, &p)
	uid := j.UserID
	c := &paperlessClient{base: p.URL, token: p.Token, http: &http.Client{Timeout: 10 * time.Minute}}
	logf := func(f string, a ...any) { s.store.JobProgress(ctx, j.ID, "", fmt.Sprintf(f, a...)) }
	prog := func(f string, a ...any) { s.store.JobProgress(ctx, j.ID, fmt.Sprintf(f, a...), "") }
	tagMap, corrMap, typeMap, pathMap, fieldMap := map[int64]int64{}, map[int64]int64{}, map[int64]int64{}, map[int64]int64{}, map[int64]int64{}
	type item struct {
		ID            int64           `json:"id"`
		Name          string          `json:"name"`
		Color         string          `json:"color"`
		Match         string          `json:"match"`
		Algorithm     int             `json:"matching_algorithm"`
		IsInsensitive bool            `json:"is_insensitive"`
		IsInbox       bool            `json:"is_inbox_tag"`
		Path          string          `json:"path"`
		DataType      string          `json:"data_type"`
		Extra         json.RawMessage `json:"extra_data"`
	}
	prog("reading tags, correspondents, types…")
	err := c.list(ctx, "/api/tags/", func(raw json.RawMessage) {
		var it item
		_ = json.Unmarshal(raw, &it)
		if ex, err := s.store.FindTag(ctx, uid, it.Name); err == nil {
			tagMap[it.ID] = ex.ID
			return
		}
		t := store.Tag{UserID: uid, Name: it.Name, Color: validColor(it.Color), Matchable: store.Matchable{Match: it.Match, MatchingAlgorithm: it.Algorithm, IsInsensitive: it.IsInsensitive}, IsInbox: it.IsInbox}
		if s.store.CreateTag(ctx, &t) == nil {
			tagMap[it.ID] = t.ID
		}
	})
	if err != nil {
		return err
	}
	namedList := func(path string, find func(context.Context, int64, string) (*store.Named, error), create func(context.Context, *store.Named) error, into map[int64]int64, isPath bool) error {
		return c.list(ctx, path, func(raw json.RawMessage) {
			var it item
			_ = json.Unmarshal(raw, &it)
			if ex, err := find(ctx, uid, it.Name); err == nil {
				into[it.ID] = ex.ID
				return
			}
			n := store.Named{UserID: uid, Name: it.Name, Matchable: store.Matchable{Match: it.Match, MatchingAlgorithm: it.Algorithm, IsInsensitive: it.IsInsensitive}}
			if isPath {
				n.Path = paperlessPathTemplate(it.Path)
			}
			if create(ctx, &n) == nil {
				into[it.ID] = n.ID
			}
		})
	}
	if err := namedList("/api/correspondents/", s.store.FindCorrespondent, s.store.CreateCorrespondent, corrMap, false); err != nil {
		return err
	}
	if err := namedList("/api/document_types/", s.store.FindDocumentType, s.store.CreateDocumentType, typeMap, false); err != nil {
		return err
	}
	if err := namedList("/api/storage_paths/", s.store.FindStoragePath, s.store.CreateStoragePath, pathMap, true); err != nil {
		return err
	}
	_ = c.list(ctx, "/api/custom_fields/", func(raw json.RawMessage) {
		var it item
		_ = json.Unmarshal(raw, &it)
		if ex, err := s.store.FindCustomField(ctx, uid, it.Name); err == nil {
			fieldMap[it.ID] = ex.ID
			return
		}
		cf := store.CustomField{UserID: uid, Name: it.Name, DataType: paperlessFieldType(it.DataType), Extra: paperlessFieldExtra(it.Extra)}
		if s.store.CreateCustomField(ctx, &cf) == nil {
			fieldMap[it.ID] = cf.ID
		}
	})
	logf("taxonomy: %d tags, %d correspondents, %d types, %d paths, %d fields", len(tagMap), len(corrMap), len(typeMap), len(pathMap), len(fieldMap))
	// documents
	var docs []json.RawMessage
	if err := c.list(ctx, "/api/documents/", func(raw json.RawMessage) { docs = append(docs, raw) }); err != nil {
		return err
	}
	logf("%d documents to import", len(docs))
	imported, skipped, failed := 0, 0, 0
	for i, raw := range docs {
		var pd struct {
			ID            int64   `json:"id"`
			Title         string  `json:"title"`
			Content       string  `json:"content"`
			Created       string  `json:"created"`
			ASN           *int64  `json:"archive_serial_number"`
			Correspondent *int64  `json:"correspondent"`
			DocumentType  *int64  `json:"document_type"`
			StoragePath   *int64  `json:"storage_path"`
			Tags          []int64 `json:"tags"`
			Filename      string  `json:"original_file_name"`
			Added         string  `json:"added"`
			Notes         []struct {
				Note string `json:"note"`
			} `json:"notes"`
			Fields []struct {
				Field int64 `json:"field"`
				Value any   `json:"value"`
			} `json:"custom_fields"`
		}
		_ = json.Unmarshal(raw, &pd)
		prog("%d of %d: %s", i+1, len(docs), pd.Title)
		tmp := filepath.Join(s.cfg.TmpDir, fmt.Sprintf("paperless-%d%s", pd.ID, filepath.Ext(firstNonEmpty(pd.Filename, ".pdf"))))
		if err := c.download(ctx, pd.ID, tmp); err != nil {
			failed++
			logf("#%d %s: download failed: %v", pd.ID, pd.Title, err)
			continue
		}
		in := incoming{Path: tmp, Name: firstNonEmpty(pd.Filename, fmt.Sprintf("paperless-%d.pdf", pd.ID)), Title: pd.Title, Created: normalizeDate(pd.Created), ASN: pd.ASN, Tags: mapIDs(pd.Tags, tagMap),
			Correspondent: mapID(pd.Correspondent, corrMap), DocumentType: mapID(pd.DocumentType, typeMap), StoragePath: mapID(pd.StoragePath, pathMap),
			Source: "paperless", Actor: firstNonEmpty(p.Actor, "paperless import"), SkipMatching: true, Content: pd.Content, ExternalID: fmt.Sprintf("paperless:%d", pd.ID), AddedAt: pd.Added}
		for _, f := range pd.Fields {
			if nid, ok := fieldMap[f.Field]; ok && f.Value != nil {
				in.Fields = append(in.Fields, store.FieldValue{FieldID: nid, Value: fmt.Sprint(f.Value)})
			}
		}
		d, err := s.ingest(ctx, uid, in)
		if err != nil {
			var dup *ErrDuplicate
			if errors.As(err, &dup) {
				skipped++
				continue
			}
			failed++
			logf("#%d %s: %v", pd.ID, pd.Title, err)
			continue
		}
		for _, n := range pd.Notes {
			if strings.TrimSpace(n.Note) != "" {
				_ = s.store.AddNote(ctx, &store.Note{DocumentID: d.ID, UserID: uid, Text: n.Note})
			}
		}
		imported++
	}
	logf("done: %d imported, %d already here, %d failed", imported, skipped, failed)
	s.classifiers.Invalidate(uid)
	if failed > 0 && imported == 0 {
		return fmt.Errorf("%d documents failed", failed)
	}
	return nil
}
