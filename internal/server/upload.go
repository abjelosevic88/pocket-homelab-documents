package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-documents/internal/extract"
	"github.com/abjelosevic88/pocket-homelab-documents/internal/ocr"
	"github.com/abjelosevic88/pocket-homelab-documents/internal/store"
)

// incoming describes a file about to become a document.
type incoming struct {
	OperationKey  string
	OperationSlot int
	Path          string // where the bytes are now (moved into place)
	Name          string // original file name
	Title         string
	Created       string
	ASN           *int64
	Tags          []int64
	Correspondent *int64
	DocumentType  *int64
	StoragePath   *int64
	Fields        []store.FieldValue
	Source        string
	Actor         string
	SubDirs       []string // consume sub-folders, become tags when the setting says so
	AllowDup      bool
	SkipMatching  bool
	Content       string // text already known (imports)
	ExternalID    string // where it came from elsewhere ("paperless:12", "invoicing:invoice:7")
	AddedAt       string // RFC 3339; imports keep when the document first came in
}

// ErrDuplicate is returned when the same file is already in.
type ErrDuplicate struct{ Existing *store.Document }

func (e *ErrDuplicate) Error() string {
	return fmt.Sprintf("this file is already in your documents as #%d “%s”", e.Existing.ID, e.Existing.Title)
}

// ingest makes a document of a file and queues its processing.
func (s *Server) ingest(ctx contextT, uid int64, in incoming) (*store.Document, error) {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(in.Name), "."))
	if ext == "" {
		ext = strings.ToLower(strings.TrimPrefix(filepath.Ext(in.Path), "."))
	}
	if ext == "jpeg" {
		ext = "jpg"
	}
	if ext == "tiff" {
		ext = "tif"
	}
	if extract.Kind(ext) == "" {
		os.Remove(in.Path)
		return nil, fmt.Errorf("%w: files of type .%s are not supported", errInvalidUpload, ext)
	}
	sum, size, err := checksumOf(in.Path)
	if err != nil {
		return nil, err
	}
	fingerprint := in
	fingerprint.Path = ""
	fingerprint.Actor = ""
	fingerprint.OperationKey = ""
	fingerprint.OperationSlot = 0
	scope := fmt.Sprintf("%d:%s:upload:%d", uid, linkAppFrom(ctx), in.OperationSlot)
	key := in.OperationKey
	if in.ExternalID != "" {
		scope = fmt.Sprintf("%d:document-external-id", uid)
		key = in.ExternalID
	}
	op, err := core.NewOperation(scope, key, struct {
		Checksum string
		Input    incoming
	}{sum, fingerprint})
	if err != nil {
		os.Remove(in.Path)
		return nil, fmt.Errorf("%w: %s", errInvalidUpload, err)
	}
	var replayID int64
	if ok, err := op.Replay(ctx, s.store.DB, &replayID); err != nil {
		os.Remove(in.Path)
		return nil, err
	} else if ok {
		os.Remove(in.Path)
		d, err := s.store.GetDocument(ctx, uid, replayID, true)
		if err == nil && d.DeletedAt != nil {
			return nil, store.ErrNotFound
		}
		return d, err
	}
	st, _ := s.store.GetSettings(ctx, uid)
	fromName := false
	if in.Created == "" && st.DateFromName {
		in.Created = dateFromName(in.Name)
		fromName = in.Created != ""
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = titleFromName(in.Name, fromName)
	}
	d := &store.Document{UserID: uid, Title: title, Created: firstNonEmpty(in.Created, store.Today()), ASN: in.ASN, Tags: append([]int64{}, in.Tags...), Fields: in.Fields,
		CorrespondentID: in.Correspondent, DocumentTypeID: in.DocumentType, StoragePathID: in.StoragePath,
		OriginalName: in.Name, OriginalExt: ext, Mime: extract.Mime(ext), Checksum: sum, Size: size, Source: firstNonEmpty(in.Source, "web"), Content: in.Content, ExternalID: strings.TrimSpace(in.ExternalID), AddedAt: in.AddedAt}
	if st.ConsumeSubdirsAsTags {
		for _, sub := range in.SubDirs {
			if t, err := s.store.FindTag(ctx, uid, sub); err == nil {
				d.Tags = append(d.Tags, t.ID)
			} else {
				nt := &store.Tag{UserID: uid, Name: sub, Color: tagColors[len(sub)%len(tagColors)], Matchable: store.Matchable{MatchingAlgorithm: store.MatchNone, IsInsensitive: true}}
				if err := s.store.CreateTag(ctx, nt); err == nil {
					d.Tags = append(d.Tags, nt.ID)
				}
			}
		}
	}
	// a new document lands in the inbox; imports arrive already filed
	if !in.SkipMatching {
		d.Tags = append(d.Tags, s.store.InboxTagIDs(ctx, uid)...)
	}
	// Reserve the filename independently, then durably place the bytes before
	// making a document visible. Crashes may leave an unused file, never a row
	// with a missing original or a filename reused by another upload.
	d.ID, err = s.store.ReserveDocumentID(ctx)
	if err != nil {
		return nil, err
	}
	dst := s.originalPath(d)
	if err = moveFileDurable(in.Path, dst); err != nil {
		os.Remove(dst)
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			os.Remove(dst)
		}
	}()
	params, err := json.Marshal(map[string]any{"new": true, "detect_date": in.Created == "" && st.DateFromText, "skip_matching": in.SkipMatching, "actor": firstNonEmpty(in.Actor, in.Source)})
	if err != nil {
		return nil, err
	}
	event := s.documentEvent(ctx, uid, "documents.added", d, map[string]any{"file": in.Name, "source": d.Source})
	replayed, err := s.store.CreateDocumentOperation(ctx, d, op, in.AllowDup, d.ID, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO jobs(user_id,document_id,kind,params) VALUES(?,?,'process',?)`, uid, d.ID, string(params)); err != nil {
			return err
		}
		if event != nil {
			return event(tx)
		}
		return nil
	})
	if err != nil {
		var dup *store.DuplicateDocument
		if errors.As(err, &dup) {
			ex, e := s.store.GetDocument(ctx, uid, dup.ID, true)
			if e != nil {
				return nil, e
			}
			return nil, &ErrDuplicate{Existing: ex}
		}
		return nil, err
	}
	if replayed {
		return s.store.GetDocument(ctx, uid, d.ID, true)
	}
	keep = true
	s.store.AddHistory(ctx, d.ID, uid, firstNonEmpty(in.Actor, in.Source), "added", map[string]any{"file": in.Name, "size": size, "source": d.Source})
	s.nudge()

	return d, nil
}

var nameDates = []*regexp.Regexp{
	regexp.MustCompile(`(?:19|20)\d{2}[-_.](?:0[1-9]|1[0-2])[-_.](?:0[1-9]|[12]\d|3[01])`),
	regexp.MustCompile(`(?:0?[1-9]|[12]\d|3[01])\.(?:0?[1-9]|1[0-2])\.(?:19|20)\d{2}`),
	regexp.MustCompile(`(?:19|20)\d{2}(?:0[1-9]|1[0-2])(?:0[1-9]|[12]\d|3[01])`),
}

// titleFromName makes a title of a file name: separators become spaces and,
// when the date was read from the name, the date goes (it is the document date now).
func titleFromName(name string, dropDate bool) string {
	t := strings.TrimSuffix(name, filepath.Ext(name))
	if dropDate {
		for _, re := range nameDates {
			if loc := re.FindStringIndex(t); loc != nil {
				t = t[:loc[0]] + " " + t[loc[1]:]
				break
			}
		}
	}
	t = strings.Join(strings.Fields(strings.NewReplacer("_", " ").Replace(t)), " ")
	t = strings.Trim(t, " -.")
	if t == "" {
		return "Untitled"
	}
	return t
}

// dateFromName reads a date from a file name: 2026-03-14_invoice.pdf,
// 20260314-scan.pdf, "Rechnung 14.03.2026.pdf".
func dateFromName(name string) string {
	base := strings.NewReplacer("_", " ", "-", "-").Replace(strings.TrimSuffix(name, filepath.Ext(name)))
	if d := ocr.GuessCreated(base, true); d != "" {
		return d
	}
	if m := compactDate.FindStringSubmatch(base); m != nil {
		if t, err := time.Parse("20060102", m[1]); err == nil && t.Year() >= 1990 && t.Before(time.Now().AddDate(0, 0, 2)) {
			return t.Format("2006-01-02")
		}
	}
	return ""
}

var compactDate = regexp.MustCompile(`(?:^|[^0-9])((?:19|20)\d{2}(?:0[1-9]|1[0-2])(?:0[1-9]|[12]\d|3[01]))(?:[^0-9]|$)`)

func checksumOf(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	return hashReader(f)
}

var errInvalidUpload = errors.New("invalid upload")

func moveFileDurable(src, dst string) error {
	if err := moveFile(src, dst); err != nil {
		return err
	}
	f, err := os.Open(dst)
	if err != nil {
		return err
	}
	err = f.Sync()
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	dir, err := os.Open(filepath.Dir(dst))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func moveFile(src, dst string) error {
	_ = os.MkdirAll(filepath.Dir(dst), 0o755)
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	if err := copyFile(src, dst); err != nil {
		return err
	}
	return os.Remove(src)
}

// handleUpload takes one or more files (multipart "document", "file" or
// "files"), or a raw body with a file name in X-File-Name.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), me(r)
	ct := r.Header.Get("Content-Type")
	mt, _, _ := mime.ParseMediaType(ct)
	var results []any
	status := http.StatusCreated
	failureStatus := http.StatusBadRequest
	actor := actorName(r)
	ingestOne := func(name string, rd io.Reader, meta map[string]string) {
		ext := strings.TrimPrefix(filepath.Ext(name), ".")
		if ext == "" {
			ext = "bin"
		}
		tmp, _, _, err := s.saveUpload(rd, ext)
		if err != nil {
			results = append(results, map[string]any{"file": name, "error": err.Error()})
			return
		}
		in := incoming{OperationKey: r.Header.Get("Idempotency-Key"), OperationSlot: len(results), Path: tmp, Name: name, Title: meta["title"], Created: meta["created"], Source: firstNonEmpty(meta["source"], "web"), Actor: actor, AllowDup: meta["allow_duplicates"] == "1" || meta["allow_duplicates"] == "true", ExternalID: meta["external_id"]}
		if meta["created"] != "" {
			in.Created = normalizeDate(meta["created"])
		}
		if v := meta["archive_serial_number"]; v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				in.ASN = &n
			}
		}
		if v := meta["asn"]; v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				in.ASN = &n
			}
		}
		if v := meta["correspondent"]; v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				in.Correspondent = &n
			}
		}
		if v := meta["document_type"]; v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				in.DocumentType = &n
			}
		}
		if v := meta["storage_path"]; v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				in.StoragePath = &n
			}
		}
		for _, t := range strings.Split(meta["tags"], ",") {
			if n, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64); err == nil {
				in.Tags = append(in.Tags, n)
			}
		}
		d, err := s.ingest(ctx, uid, in)
		if err != nil {
			failureStatus = http.StatusInternalServerError
			switch {
			case errors.Is(err, errInvalidUpload):
				failureStatus = http.StatusBadRequest
			case errors.Is(err, core.ErrOperationConflict), errors.Is(err, store.ErrConflict):
				failureStatus = http.StatusConflict
			case errors.Is(err, store.ErrNotFound):
				failureStatus = http.StatusNotFound
			}

			var dup *ErrDuplicate
			if errors.As(err, &dup) {
				results = append(results, map[string]any{"file": name, "error": err.Error(), "duplicate_of": dup.Existing.ID})
				return
			}
			results = append(results, map[string]any{"file": name, "error": err.Error()})
			return
		}
		results = append(results, map[string]any{"file": name, "document": d})
	}
	switch {
	case mt == "multipart/form-data":
		mr, err := r.MultipartReader()
		if err != nil {
			writeErr(w, http.StatusBadRequest, "bad multipart body")
			return
		}
		meta := map[string]string{}
		for k := range r.URL.Query() { // query parameters count as form fields (scripts, Shortcuts)
			meta[k] = r.URL.Query().Get(k)
		}
		type pending struct {
			name string
			tmp  string
		}
		var files []pending
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				writeErr(w, http.StatusBadRequest, "bad multipart body")
				return
			}
			if part.FileName() == "" {
				b, _ := io.ReadAll(io.LimitReader(part, 64<<10))
				meta[part.FormName()] = strings.TrimSpace(string(b))
				if part.FormName() == "tags" && meta["tags"] != string(b) {
					meta["tags"] = strings.Trim(meta["tags"]+","+string(b), ",")
				}
				continue
			}
			ext := strings.TrimPrefix(filepath.Ext(part.FileName()), ".")
			tmp, _, _, err := s.saveUpload(part, firstNonEmpty(ext, "bin"))
			if err != nil {
				results = append(results, map[string]any{"file": part.FileName(), "error": err.Error()})
				continue
			}
			files = append(files, pending{filepath.Base(part.FileName()), tmp})
		}
		for _, f := range files {
			ingestOne(f.name, mustOpen(f.tmp), meta)
			os.Remove(f.tmp)
		}
	default:
		name := firstNonEmpty(r.Header.Get("X-File-Name"), r.URL.Query().Get("name"))
		if name == "" {
			if exts, _ := mime.ExtensionsByType(mt); len(exts) > 0 {
				name = "upload" + exts[0]
			} else {
				writeErr(w, http.StatusBadRequest, "send multipart/form-data with a \"document\" field, or a raw body with X-File-Name")
				return
			}
		}
		meta := map[string]string{}
		for k := range r.URL.Query() {
			meta[k] = r.URL.Query().Get(k)
		}
		ingestOne(filepath.Base(name), r.Body, meta)
	}
	if len(results) == 0 {
		writeErr(w, http.StatusBadRequest, "no file in the request")
		return
	}
	ok := 0
	for _, x := range results {
		if _, has := x.(map[string]any)["document"]; has {
			ok++
		}
	}
	if ok == 0 {
		status = failureStatus
		if len(results) == 1 {
			if dup, has := results[0].(map[string]any)["duplicate_of"]; has {
				writeJSON(w, http.StatusConflict, map[string]any{"error": results[0].(map[string]any)["error"], "duplicate_of": dup})
				return
			}
		}
	}
	if len(results) == 1 && ok == 1 {
		writeJSON(w, status, results[0].(map[string]any)["document"])
		return
	}
	writeJSON(w, status, map[string]any{"results": results, "added": ok})
}

type tmpReader struct {
	*os.File
}

func mustOpen(p string) io.Reader {
	f, err := os.Open(p)
	if err != nil {
		return strings.NewReader("")
	}
	return &closingReader{f}
}

// closingReader closes the file once it has been read to the end.
type closingReader struct{ f *os.File }

func (c *closingReader) Read(p []byte) (int, error) {
	n, err := c.f.Read(p)
	if err == io.EOF {
		c.f.Close()
	}
	return n, err
}
