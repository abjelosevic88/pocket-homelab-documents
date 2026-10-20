package server

import (
	"errors"
	"fmt"
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
		return nil, fmt.Errorf("files of type .%s are not supported", ext)
	}
	sum, size, err := checksumOf(in.Path)
	if err != nil {
		return nil, err
	}
	if !in.AllowDup {
		if ex, err := s.store.FindByChecksum(ctx, uid, sum); err == nil {
			os.Remove(in.Path)
			return nil, &ErrDuplicate{Existing: ex}
		}
	}
	st, _ := s.store.GetSettings(ctx, uid)
	if in.Created == "" && st.DateFromName {
		in.Created = dateFromName(in.Name)
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = strings.TrimSuffix(in.Name, filepath.Ext(in.Name))
		title = strings.TrimSpace(strings.NewReplacer("_", " ").Replace(title))
		if title == "" {
			title = "Untitled"
		}
	}
	d := &store.Document{UserID: uid, Title: title, Created: firstNonEmpty(in.Created, store.Today()), ASN: in.ASN, Tags: append([]int64{}, in.Tags...), Fields: in.Fields,
		CorrespondentID: in.Correspondent, DocumentTypeID: in.DocumentType, StoragePathID: in.StoragePath,
		OriginalName: in.Name, OriginalExt: ext, Mime: extract.Mime(ext), Checksum: sum, Size: size, Source: firstNonEmpty(in.Source, "web"), Content: in.Content}
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
	if err := s.store.CreateDocument(ctx, d); err != nil {
		if errors.Is(err, store.ErrConflict) {
			os.Remove(in.Path)
			return nil, errors.New("that archive serial number is already in use")
		}
		return nil, err
	}
	dst := s.originalPath(d)
	if err := moveFile(in.Path, dst); err != nil {
		_ = s.store.PurgeDocument(ctx, uid, d.ID)
		return nil, err
	}
	s.store.AddHistory(ctx, d.ID, uid, firstNonEmpty(in.Actor, in.Source), "added", map[string]any{"file": in.Name, "size": size, "source": d.Source})
	s.publish(uid, "documents.added", "document:"+strconv.FormatInt(d.ID, 10), map[string]any{"id": d.ID, "title": d.Title, "file": in.Name, "source": d.Source})
	params := map[string]any{"new": true, "detect_date": in.Created == "" && st.DateFromText, "skip_matching": in.SkipMatching, "actor": firstNonEmpty(in.Actor, in.Source)}
	if _, err := s.enqueueProcess(ctx, d, "process", params); err != nil {
		return d, err
	}
	return d, nil
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
		in := incoming{Path: tmp, Name: name, Title: meta["title"], Created: meta["created"], Source: firstNonEmpty(meta["source"], "web"), Actor: actor, AllowDup: meta["allow_duplicates"] == "1" || meta["allow_duplicates"] == "true"}
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
		status = http.StatusBadRequest
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
