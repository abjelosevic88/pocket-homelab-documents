package server

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-documents/internal/ocr"
	"github.com/abjelosevic88/pocket-homelab-documents/internal/store"
)

// Barcodes on incoming scans: an ASN label ("ASN00042") sets the archive
// serial number; a separator sheet (a barcode reading PATCHT, or whatever
// Settings say) splits a batch scan into one document per section.

// barcodes reads the codes of a new document when the box has that on.
// It returns true when the document was split into others (and is gone).
func (s *Server) barcodes(ctx context.Context, d *store.Document, tools *ocr.Tools, logf func(string, ...any)) (split bool) {
	box, _ := s.store.GetSettings(ctx, 0)
	if !box.BarcodesEnabled || !tools.ZBar {
		return false
	}
	ext := strings.ToLower(d.OriginalExt)
	if ext != "pdf" && !strings.HasPrefix(d.Mime, "image/") {
		return false
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	codes, err := ocr.ReadBarcodes(cctx, tools, s.originalPath(d), s.cfg.TmpDir, box.BarcodeMaxPages)
	if err != nil {
		logf("barcodes: %v", err)
		return false
	}
	if len(codes) == 0 {
		return false
	}
	for _, c := range codes {
		logf("barcode on page %d: %s", c.Page, c.Value)
	}
	// ASN
	if d.ASN == nil && box.BarcodeASNPrefix != "" {
		re := regexp.MustCompile(`^` + regexp.QuoteMeta(box.BarcodeASNPrefix) + `0*(\d+)$`)
		for _, c := range codes {
			if m := re.FindStringSubmatch(strings.TrimSpace(c.Value)); m != nil {
				if n, err := strconv.ParseInt(m[1], 10, 64); err == nil && n > 0 {
					p := &n
					if _, err := s.store.UpdateDocument(ctx, d.UserID, d.ID, store.DocChanges{ASN: &p}); err == nil {
						d.ASN = &n
						logf("archive serial number %d from the barcode", n)
						s.store.AddHistory(ctx, d.ID, d.UserID, "barcode", "asn", map[string]any{"asn": n})
					} else {
						logf("archive serial number %d from the barcode is already taken", n)
					}
				}
				break
			}
		}
	}
	// separators (PDFs only)
	if ext != "pdf" || d.PageCount == 0 || box.BarcodeSeparator == "" {
		return false
	}
	sep := map[int]bool{}
	for _, c := range codes {
		if strings.EqualFold(strings.TrimSpace(c.Value), box.BarcodeSeparator) {
			sep[c.Page] = true
		}
	}
	if len(sep) == 0 {
		return false
	}
	// sections between separator pages
	var sections [][]int
	var cur []int
	for p := 1; p <= d.PageCount; p++ {
		if sep[p] {
			if len(cur) > 0 {
				sections = append(sections, cur)
			}
			cur = nil
			if box.BarcodeKeepSep {
				cur = append(cur, p)
			}
			continue
		}
		cur = append(cur, p)
	}
	if len(cur) > 0 {
		sections = append(sections, cur)
	}
	if len(sections) <= 1 && !box.BarcodeKeepSep {
		// only separators at the edges: just drop them
		if len(sections) == 1 && len(sections[0]) < d.PageCount {
			tmp := filepath.Join(s.cfg.TmpDir, fmt.Sprintf("nosep-%d.pdf", d.ID))
			if err := ocr.ExtractPages(cctx, s.originalPath(d), tmp, sections[0]); err == nil {
				_ = s.swapFileQuiet(ctx, d, tmp, "separator pages removed")
				logf("separator pages removed")
			}
		}
		return false
	}
	logf("separator sheets found: splitting into %d documents", len(sections))
	src := s.originalPath(d)
	made := 0
	for i, pages := range sections {
		tmp := filepath.Join(s.cfg.TmpDir, fmt.Sprintf("split-%d-%d.pdf", d.ID, i+1))
		if err := ocr.ExtractPages(cctx, src, tmp, pages); err != nil {
			logf("part %d: %v", i+1, err)
			continue
		}
		title := fmt.Sprintf("%s (%d)", d.Title, i+1)
		nd, err := s.ingest(ctx, d.UserID, incoming{Path: tmp, Name: safeFileName(title) + ".pdf", Title: title, Created: d.Created, Tags: d.Tags, Correspondent: d.CorrespondentID, DocumentType: d.DocumentTypeID, StoragePath: d.StoragePathID,
			Source: d.Source, Actor: "separator sheet", AllowDup: true})
		if err != nil {
			logf("part %d: %v", i+1, err)
			continue
		}
		s.store.AddHistory(ctx, nd.ID, d.UserID, "barcode", "split", map[string]any{"from": d.ID, "pages": pages})
		made++
	}
	if made == 0 {
		return false
	}
	// the batch itself is done with: to the trash, where it can be restored if the split went wrong
	_ = s.store.SoftDeleteDocument(ctx, d.UserID, d.ID)
	s.store.AddHistory(ctx, d.ID, d.UserID, "barcode", "split", map[string]any{"parts": made})
	logf("batch moved to the trash after splitting into %d documents", made)
	return true
}

// swapFileQuiet replaces the original without queuing OCR (the caller is the OCR job).
func (s *Server) swapFileQuiet(ctx context.Context, d *store.Document, newPath, reason string) error {
	sum, size, err := checksumOf(newPath)
	if err != nil {
		return err
	}
	if err := s.keepVersion(ctx, d, reason, "barcode"); err != nil {
		return err
	}
	if err := moveFile(newPath, s.originalPath(d)); err != nil {
		return err
	}
	d.Checksum, d.Size = sum, size
	return s.store.ReplaceFile(ctx, d.ID, d.OriginalName, d.OriginalExt, d.Mime, sum, size)
}
