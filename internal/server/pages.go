package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-documents/internal/extract"
	"github.com/abjelosevic88/pocket-homelab-documents/internal/ocr"
	"github.com/abjelosevic88/pocket-homelab-documents/internal/store"
)

// Page tools: rotate, delete, split, merge, and replacing the file. Each
// change puts the current original aside as a version and runs OCR again.

func (s *Server) versionPath(id int64, n int, ext string) string {
	return filepath.Join(s.cfg.DataDir, "versions", strconv.FormatInt(id, 10), fmt.Sprintf("%d.%s", n, firstNonEmpty(ext, "bin")))
}

// keepVersion moves the document's current original aside and records it.
func (s *Server) keepVersion(ctx context.Context, d *store.Document, reason, actor string) error {
	v := &store.Version{DocumentID: d.ID, Ext: d.OriginalExt, Size: d.Size, Checksum: d.Checksum, Reason: reason, Actor: actor}
	if err := s.store.AddVersion(ctx, v); err != nil {
		return err
	}
	dst := s.versionPath(d.ID, v.N, d.OriginalExt)
	if err := moveFile(s.originalPath(d), dst); err != nil {
		return err
	}
	return nil
}

// swapFile makes a new file the document's original (the old one was put aside).
func (s *Server) swapFile(ctx context.Context, d *store.Document, newPath, name string, actor, reason string) error {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	if ext == "" {
		ext = strings.ToLower(strings.TrimPrefix(filepath.Ext(newPath), "."))
	}
	if ext == "jpeg" {
		ext = "jpg"
	}
	if extract.Kind(ext) == "" {
		return fmt.Errorf("files of type .%s are not supported", ext)
	}
	sum, size, err := checksumOf(newPath)
	if err != nil {
		return err
	}
	if err := s.keepVersion(ctx, d, reason, actor); err != nil {
		return err
	}
	d.OriginalExt = ext
	if err := moveFile(newPath, s.originalPath(d)); err != nil {
		return err
	}
	_ = os.Remove(s.archivePath(d.ID))
	_ = os.Remove(s.thumbPath(d.ID))
	_ = os.RemoveAll(filepath.Join(s.cfg.PagesDir, strconv.FormatInt(d.ID, 10)))
	if err := s.store.ReplaceFile(ctx, d.ID, name, ext, extract.Mime(ext), sum, size); err != nil {
		return err
	}
	s.store.AddHistory(ctx, d.ID, d.UserID, actor, reason, map[string]any{"file": name, "size": size})
	_, err = s.enqueueProcess(ctx, d, "reprocess", map[string]any{"actor": actor, "after": reason})
	return err
}

// pdfOf returns a PDF path for a document: the original when it is one, else
// the archive twin (images get one from OCR).
func (s *Server) pdfOf(d *store.Document) (string, error) {
	if strings.EqualFold(d.OriginalExt, "pdf") {
		return s.originalPath(d), nil
	}
	if d.HasArchive {
		if _, err := os.Stat(s.archivePath(d.ID)); err == nil {
			return s.archivePath(d.ID), nil
		}
	}
	return "", errors.New("this document has no PDF to work on yet (OCR makes one)")
}

type pagesIn struct {
	Op      string `json:"op"` // rotate | delete | split | reorder
	Pages   []int  `json:"pages"`
	Degrees int    `json:"degrees"`
	Title   string `json:"title"`  // split: the new document's title
	Remove  bool   `json:"remove"` // split: take the pages out of this document too
}

func (s *Server) handlePages(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), me(r)
	d, err := s.store.GetDocument(ctx, uid, idParam(r, "id"), false)
	if err != nil {
		s.fail(w, err, "document")
		return
	}
	var in pagesIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if _, busy := s.processing.Load(d.ID); busy || d.Status == "processing" {
		writeErr(w, http.StatusConflict, "OCR is still running on this document; try again in a moment")
		return
	}
	tools := ocr.Detect(ctx, s.cfg.GotenbergURL)
	actor := actorName(r)
	for _, p := range in.Pages {
		if p < 1 || (d.PageCount > 0 && p > d.PageCount) {
			writeErr(w, http.StatusBadRequest, fmt.Sprintf("no page %d", p))
			return
		}
	}
	cctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	tmp := filepath.Join(s.cfg.TmpDir, fmt.Sprintf("pages-%d-%d", d.ID, time.Now().UnixNano()))
	switch in.Op {
	case "rotate":
		if in.Degrees%90 != 0 || in.Degrees == 0 {
			writeErr(w, http.StatusBadRequest, "degrees must be 90, 180 or 270")
			return
		}
		if strings.EqualFold(d.OriginalExt, "pdf") {
			if !tools.QPDF {
				writeErr(w, http.StatusBadRequest, "qpdf is not installed")
				return
			}
			tmp += ".pdf"
			if err := ocr.RotatePDF(cctx, s.originalPath(d), tmp, in.Pages, in.Degrees); err != nil {
				s.fail(w, err, "rotate")
				return
			}
		} else if strings.HasPrefix(d.Mime, "image/") {
			tmp += "." + d.OriginalExt
			if err := ocr.RotateImage(s.originalPath(d), tmp, in.Degrees); err != nil {
				s.fail(w, err, "rotate")
				return
			}
		} else {
			writeErr(w, http.StatusBadRequest, "only PDFs and images can be rotated")
			return
		}
		if err := s.swapFile(ctx, d, tmp, d.OriginalName, actor, "rotated"); err != nil {
			s.fail(w, err, "rotate")
			return
		}
	case "delete":
		src, err := s.pdfOf(d)
		if err != nil || !tools.QPDF {
			writeErr(w, http.StatusBadRequest, firstNonEmpty(errString(err), "qpdf is not installed"))
			return
		}
		if len(in.Pages) == 0 {
			writeErr(w, http.StatusBadRequest, "which pages?")
			return
		}
		tmp += ".pdf"
		if err := ocr.DeletePages(cctx, src, tmp, in.Pages, d.PageCount); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		name := d.OriginalName
		if !strings.EqualFold(d.OriginalExt, "pdf") {
			name = strings.TrimSuffix(d.OriginalName, filepath.Ext(d.OriginalName)) + ".pdf"
		}
		if err := s.swapFile(ctx, d, tmp, name, actor, "pages removed"); err != nil {
			s.fail(w, err, "delete pages")
			return
		}
	case "split":
		src, err := s.pdfOf(d)
		if err != nil || !tools.QPDF {
			writeErr(w, http.StatusBadRequest, firstNonEmpty(errString(err), "qpdf is not installed"))
			return
		}
		if len(in.Pages) == 0 || (d.PageCount > 0 && len(in.Pages) >= d.PageCount && in.Remove) {
			writeErr(w, http.StatusBadRequest, "choose some, not all, of the pages")
			return
		}
		tmp += ".pdf"
		if err := ocr.ExtractPages(cctx, src, tmp, in.Pages); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		title := strings.TrimSpace(in.Title)
		if title == "" {
			title = d.Title + " (part)"
		}
		nd, err := s.ingest(ctx, uid, incoming{Path: tmp, Name: safeFileName(title) + ".pdf", Title: title, Created: d.Created, Tags: d.Tags, Correspondent: d.CorrespondentID, DocumentType: d.DocumentTypeID, StoragePath: d.StoragePathID,
			Source: d.Source, Actor: actor, AllowDup: true, SkipMatching: true})
		if err != nil {
			s.fail(w, err, "split")
			return
		}
		s.store.AddHistory(ctx, nd.ID, uid, actor, "split", map[string]any{"from": d.ID, "pages": in.Pages})
		s.store.AddHistory(ctx, d.ID, uid, actor, "split", map[string]any{"to": nd.ID, "pages": in.Pages})
		if in.Remove {
			tmp2 := filepath.Join(s.cfg.TmpDir, fmt.Sprintf("pages-%d-%d-rest.pdf", d.ID, time.Now().UnixNano()))
			if err := ocr.DeletePages(cctx, src, tmp2, in.Pages, d.PageCount); err == nil {
				name := d.OriginalName
				if !strings.EqualFold(d.OriginalExt, "pdf") {
					name = strings.TrimSuffix(d.OriginalName, filepath.Ext(d.OriginalName)) + ".pdf"
				}
				_ = s.swapFile(ctx, d, tmp2, name, actor, "pages removed")
			}
		}
		writeJSON(w, http.StatusCreated, nd)
		return
	default:
		writeErr(w, http.StatusBadRequest, "op must be rotate, delete or split")
		return
	}
	nd, _ := s.store.GetDocument(ctx, uid, d.ID, false)
	writeJSON(w, http.StatusOK, nd)
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// handleMerge joins several documents into a new one (their PDFs in the given order).
func (s *Server) handleMerge(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), me(r)
	var in struct {
		IDs             []int64 `json:"ids"`
		Title           string  `json:"title"`
		DeleteOriginals bool    `json:"delete_originals"`
	}
	if err := decode(r, &in); err != nil || len(in.IDs) < 2 {
		writeErr(w, http.StatusBadRequest, "two or more documents are needed")
		return
	}
	tools := ocr.Detect(ctx, s.cfg.GotenbergURL)
	if !tools.QPDF {
		writeErr(w, http.StatusBadRequest, "qpdf is not installed")
		return
	}
	var docs []*store.Document
	var pdfs []string
	for _, id := range in.IDs {
		d, err := s.store.GetDocument(ctx, uid, id, false)
		if err != nil {
			s.fail(w, err, "document")
			return
		}
		p, err := s.pdfOf(d)
		if err != nil {
			writeErr(w, http.StatusBadRequest, fmt.Sprintf("“%s”: %v", d.Title, err))
			return
		}
		docs = append(docs, d)
		pdfs = append(pdfs, p)
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	tmp := filepath.Join(s.cfg.TmpDir, fmt.Sprintf("merge-%d.pdf", time.Now().UnixNano()))
	if err := ocr.MergePDFs(cctx, tmp, pdfs...); err != nil {
		s.fail(w, err, "merge")
		return
	}
	first := docs[0]
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = first.Title + " (merged)"
	}
	tags := map[int64]bool{}
	var tagList []int64
	for _, d := range docs {
		for _, t := range d.Tags {
			if !tags[t] {
				tags[t] = true
				tagList = append(tagList, t)
			}
		}
	}
	actor := actorName(r)
	nd, err := s.ingest(ctx, uid, incoming{Path: tmp, Name: safeFileName(title) + ".pdf", Title: title, Created: first.Created, Tags: tagList, Correspondent: first.CorrespondentID, DocumentType: first.DocumentTypeID, StoragePath: first.StoragePathID,
		Source: "web", Actor: actor, AllowDup: true, SkipMatching: true})
	if err != nil {
		s.fail(w, err, "merge")
		return
	}
	s.store.AddHistory(ctx, nd.ID, uid, actor, "merged", map[string]any{"from": in.IDs})
	if in.DeleteOriginals {
		for _, d := range docs {
			if err := s.store.SoftDeleteDocument(ctx, uid, d.ID); err == nil {
				s.unplace(ctx, d)
				s.store.AddHistory(ctx, d.ID, uid, actor, "deleted", map[string]any{"merged_into": nd.ID})
			}
		}
	}
	writeJSON(w, http.StatusCreated, nd)
}

// handleReplaceFile takes a new file for an existing document (multipart "document").
func (s *Server) handleReplaceFile(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), me(r)
	d, err := s.store.GetDocument(ctx, uid, idParam(r, "id"), false)
	if err != nil {
		s.fail(w, err, "document")
		return
	}
	mr, err := r.MultipartReader()
	if err != nil {
		writeErr(w, http.StatusBadRequest, "send multipart/form-data with a \"document\" field")
		return
	}
	for {
		part, err := mr.NextPart()
		if err != nil {
			writeErr(w, http.StatusBadRequest, "no file in the request")
			return
		}
		if part.FileName() == "" {
			continue
		}
		ext := strings.TrimPrefix(filepath.Ext(part.FileName()), ".")
		tmp, _, _, err := s.saveUpload(part, firstNonEmpty(ext, "bin"))
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.swapFile(ctx, d, tmp, filepath.Base(part.FileName()), actorName(r), "file replaced"); err != nil {
			os.Remove(tmp)
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		nd, _ := s.store.GetDocument(ctx, uid, d.ID, false)
		writeJSON(w, http.StatusOK, nd)
		return
	}
}

func (s *Server) handleListVersions(w http.ResponseWriter, r *http.Request) {
	d := s.docForFile(w, r)
	if d == nil {
		return
	}
	v, err := s.store.ListVersions(r.Context(), d.ID)
	if err != nil {
		s.fail(w, err, "versions")
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleDownloadVersion(w http.ResponseWriter, r *http.Request) {
	d := s.docForFile(w, r)
	if d == nil {
		return
	}
	n, _ := strconv.Atoi(r.PathValue("n"))
	v, err := s.store.GetVersion(r.Context(), d.ID, n)
	if err != nil {
		s.fail(w, err, "version")
		return
	}
	serveFile(w, r, s.versionPath(d.ID, v.N, v.Ext), fmt.Sprintf("%s (v%d).%s", safeFileName(d.Title), v.N, v.Ext), extract.Mime(v.Ext), true)
}

// handleRestoreVersion makes an earlier file the current one again (the current one becomes a version).
func (s *Server) handleRestoreVersion(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), me(r)
	d, err := s.store.GetDocument(ctx, uid, idParam(r, "id"), false)
	if err != nil {
		s.fail(w, err, "document")
		return
	}
	n, _ := strconv.Atoi(r.PathValue("n"))
	v, err := s.store.GetVersion(ctx, d.ID, n)
	if err != nil {
		s.fail(w, err, "version")
		return
	}
	tmp := filepath.Join(s.cfg.TmpDir, fmt.Sprintf("restore-%d-%d.%s", d.ID, n, v.Ext))
	if err := copyFile(s.versionPath(d.ID, v.N, v.Ext), tmp); err != nil {
		s.fail(w, err, "restore")
		return
	}
	name := strings.TrimSuffix(d.OriginalName, filepath.Ext(d.OriginalName)) + "." + v.Ext
	if err := s.swapFile(ctx, d, tmp, name, actorName(r), fmt.Sprintf("version %d restored", n)); err != nil {
		s.fail(w, err, "restore")
		return
	}
	nd, _ := s.store.GetDocument(ctx, uid, d.ID, false)
	writeJSON(w, http.StatusOK, nd)
}

// removeVersions deletes a document's earlier files (purge).
func (s *Server) removeVersions(id int64) {
	_ = os.RemoveAll(filepath.Join(s.cfg.DataDir, "versions", strconv.FormatInt(id, 10)))
}
