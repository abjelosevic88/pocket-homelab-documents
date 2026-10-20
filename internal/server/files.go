package server

import (
	"context"
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

	"github.com/abjelosevic88/pocket-homelab-documents/internal/ocr"
	"github.com/abjelosevic88/pocket-homelab-documents/internal/store"
)

// Where a document's files live. Ids keep the layout flat and renames free;
// the mirror folder is the human-readable view.

func (s *Server) originalPath(d *store.Document) string {
	ext := d.OriginalExt
	if ext == "" {
		ext = "bin"
	}
	return filepath.Join(s.cfg.OriginalsDir, fmt.Sprintf("%d.%s", d.ID, ext))
}

func (s *Server) archivePath(id int64) string {
	return filepath.Join(s.cfg.ArchiveDir, fmt.Sprintf("%d.pdf", id))
}

func (s *Server) thumbPath(id int64) string {
	return filepath.Join(s.cfg.ThumbsDir, fmt.Sprintf("%d.jpg", id))
}

func (s *Server) pagePath(id int64, n int) string {
	return filepath.Join(s.cfg.PagesDir, strconv.FormatInt(id, 10), fmt.Sprintf("%d.jpg", n))
}

// bestFile is the archive PDF when there is one, else the original.
func (s *Server) bestFile(d *store.Document) (path, name, ctype string) {
	if d.HasArchive {
		if _, err := os.Stat(s.archivePath(d.ID)); err == nil {
			return s.archivePath(d.ID), safeFileName(d.Title) + ".pdf", "application/pdf"
		}
	}
	ctype = d.Mime
	if ctype == "" {
		ctype = mime.TypeByExtension("." + d.OriginalExt)
	}
	name = safeFileName(d.Title)
	if d.OriginalExt != "" {
		name += "." + d.OriginalExt
	}
	return s.originalPath(d), name, firstNonEmpty(ctype, "application/octet-stream")
}

var unsafeName = regexp.MustCompile(`[\x00-\x1f/\\:*?"<>|]+`)

// safeFileName keeps a title usable as a file name.
func safeFileName(t string) string {
	t = unsafeName.ReplaceAllString(strings.TrimSpace(t), " ")
	t = strings.Join(strings.Fields(t), " ")
	if len(t) > 120 {
		t = t[:120]
	}
	if t == "" || t == "." || t == ".." {
		t = "document"
	}
	return t
}

// removeFiles deletes everything stored for a document.
func (s *Server) removeFiles(d *store.Document) {
	_ = os.Remove(s.originalPath(d))
	_ = os.Remove(s.archivePath(d.ID))
	_ = os.Remove(s.thumbPath(d.ID))
	_ = os.RemoveAll(filepath.Join(s.cfg.PagesDir, strconv.FormatInt(d.ID, 10)))
}

// purgeDocument deletes a document for good: row, files and its copy in the folder.
func (s *Server) purgeDocument(ctx context.Context, d *store.Document) error {
	s.unplace(ctx, d)
	if err := s.store.PurgeDocument(ctx, d.UserID, d.ID); err != nil {
		return err
	}
	s.removeFiles(d)
	s.removeVersions(d.ID)
	return nil
}

// serveFile writes a file with the right headers; inline or as a download.
func serveFile(w http.ResponseWriter, r *http.Request, path, name, ctype string, download bool) {
	f, err := os.Open(path)
	if err != nil {
		writeErr(w, http.StatusNotFound, "file missing on disk")
		return
	}
	defer f.Close()
	fi, _ := f.Stat()
	disp := "inline"
	if download {
		disp = "attachment"
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`%s; filename="%s"; filename*=UTF-8''%s`, disp, asciiName(name), urlEscape(name)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if strings.HasPrefix(ctype, "image/") || ctype == "application/pdf" {
		w.Header().Set("Cache-Control", "private, max-age=3600")
	}
	http.ServeContent(w, r, name, fi.ModTime(), f)
}

func asciiName(n string) string {
	var b strings.Builder
	for _, r := range n {
		if r < 128 && r != '"' && r != '\\' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}

func urlEscape(n string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for _, c := range []byte(n) {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-' || c == '_' {
			b.WriteByte(c)
		} else {
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		}
	}
	return b.String()
}

// pageImage renders (and caches) page n of a document as JPEG.
func (s *Server) pageImage(ctx context.Context, d *store.Document, n int) (string, error) {
	if n < 1 || (d.PageCount > 0 && n > d.PageCount) {
		return "", store.ErrNotFound
	}
	p := s.pagePath(d.ID, n)
	if _, err := os.Stat(p); err == nil {
		return p, nil
	}
	src := ""
	if d.HasArchive {
		src = s.archivePath(d.ID)
	} else if strings.EqualFold(d.OriginalExt, "pdf") {
		src = s.originalPath(d)
	}
	if src == "" {
		return "", store.ErrNotFound
	}
	c, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := ocr.RenderPage(c, src, n, 110, p, 1400); err != nil {
		return "", err
	}
	return p, nil
}

// saveUpload writes an upload to a temp file and returns its path, checksum and size.
func (s *Server) saveUpload(r io.Reader, ext string) (path, sum string, size int64, err error) {
	_ = os.MkdirAll(s.cfg.TmpDir, 0o755)
	f, err := os.CreateTemp(s.cfg.TmpDir, "upload-*."+ext)
	if err != nil {
		return "", "", 0, err
	}
	path = f.Name()
	n, err := io.Copy(f, io.LimitReader(r, s.cfg.MaxUploadBytes+1))
	f.Close()
	if err != nil {
		os.Remove(path)
		return "", "", 0, err
	}
	if n > s.cfg.MaxUploadBytes {
		os.Remove(path)
		return "", "", 0, fmt.Errorf("the file is larger than %d MB", s.cfg.MaxUploadBytes>>20)
	}
	if n == 0 {
		os.Remove(path)
		return "", "", 0, fmt.Errorf("the file is empty")
	}
	sum, size, err = ocr.Checksum(path)
	if err != nil {
		os.Remove(path)
		return "", "", 0, err
	}
	return path, sum, size, nil
}

// ---- storage path templates ----

var tmplVar = regexp.MustCompile(`\{([a-z_]+)\}`)

// renderPath fills a storage path template for a document.
func renderPath(tmpl string, d *store.Document, names store.NameMaps) string {
	created, _ := time.Parse("2006-01-02", d.Created)
	tags := []string{}
	for _, t := range d.Tags {
		if n, ok := names.Tags[t]; ok {
			tags = append(tags, n)
		}
	}
	val := func(k string) string {
		switch k {
		case "title":
			return d.Title
		case "created":
			return d.Created
		case "created_year":
			return created.Format("2006")
		case "created_month":
			return created.Format("01")
		case "created_month_name":
			return created.Format("January")
		case "created_day":
			return created.Format("02")
		case "added":
			return strings.SplitN(d.AddedAt, "T", 2)[0]
		case "added_year":
			return strings.SplitN(d.AddedAt, "-", 2)[0]
		case "correspondent":
			if d.CorrespondentID != nil {
				return firstNonEmpty(names.Correspondents[*d.CorrespondentID], "none")
			}
			return "none"
		case "document_type":
			if d.DocumentTypeID != nil {
				return firstNonEmpty(names.Types[*d.DocumentTypeID], "none")
			}
			return "none"
		case "tag_list":
			if len(tags) == 0 {
				return "none"
			}
			return strings.Join(tags, ",")
		case "tag":
			if len(tags) == 0 {
				return "none"
			}
			return tags[0]
		case "asn":
			if d.ASN != nil {
				return strconv.FormatInt(*d.ASN, 10)
			}
			return "none"
		case "original_name":
			return strings.TrimSuffix(d.OriginalName, "."+d.OriginalExt)
		case "id":
			return strconv.FormatInt(d.ID, 10)
		}
		return "{" + k + "}"
	}
	out := tmplVar.ReplaceAllStringFunc(tmpl, func(m string) string { return safeFileName(val(m[1 : len(m)-1])) })
	parts := []string{}
	for _, p := range strings.Split(filepath.ToSlash(out), "/") {
		p = strings.TrimSpace(p)
		if p == "" || p == "." || p == ".." {
			continue
		}
		parts = append(parts, p)
	}
	if len(parts) == 0 {
		parts = []string{safeFileName(d.Title)}
	}
	return filepath.Join(parts...)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
