// Package ocr turns an uploaded file into a searchable document: a PDF/A
// twin with a text layer (ocrmypdf + tesseract), the plain text, a
// thumbnail and page images (poppler), the page count, and a date read off
// the paper. Office files are rendered to PDF by Gotenberg when there is
// one; otherwise their text is read natively and they keep no PDF twin.
package ocr

import (
	"context"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Tools says which helpers are installed.
type Tools struct {
	OCRmyPDF  bool              `json:"ocrmypdf"`
	Tesseract bool              `json:"tesseract"`
	PDFToText bool              `json:"pdftotext"`
	PDFToPPM  bool              `json:"pdftoppm"`
	PDFInfo   bool              `json:"pdfinfo"`
	QPDF      bool              `json:"qpdf"`
	Unpaper   bool              `json:"unpaper"`
	Languages []string          `json:"languages"` // tesseract language codes installed
	Gotenberg bool              `json:"gotenberg"`
	Versions  map[string]string `json:"versions"`
	checkedAt time.Time
}

var (
	toolsMu     sync.Mutex
	toolsCached *Tools
)

// Detect finds the helpers on PATH (cached for a few minutes).
func Detect(ctx context.Context, gotenbergURL string) *Tools {
	toolsMu.Lock()
	defer toolsMu.Unlock()
	if toolsCached != nil && time.Since(toolsCached.checkedAt) < 5*time.Minute {
		return toolsCached
	}
	t := &Tools{Versions: map[string]string{}, checkedAt: time.Now(), Languages: []string{}}
	has := func(name string) bool {
		_, err := exec.LookPath(name)
		return err == nil
	}
	t.OCRmyPDF, t.Tesseract, t.PDFToText, t.PDFToPPM, t.PDFInfo, t.QPDF, t.Unpaper = has("ocrmypdf"), has("tesseract"), has("pdftotext"), has("pdftoppm"), has("pdfinfo"), has("qpdf"), has("unpaper")
	ver := func(name string, args ...string) string {
		c, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		out, _ := exec.CommandContext(c, name, args...).CombinedOutput()
		line := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
		if m := regexp.MustCompile(`\d+(\.\d+)+`).FindString(line); m != "" {
			return m
		}
		return line
	}
	if t.OCRmyPDF {
		t.Versions["ocrmypdf"] = ver("ocrmypdf", "--version")
	}
	if t.Tesseract {
		t.Versions["tesseract"] = ver("tesseract", "--version")
		c, cancel := context.WithTimeout(ctx, 20*time.Second)
		out, _ := exec.CommandContext(c, "tesseract", "--list-langs").CombinedOutput()
		cancel()
		for _, l := range strings.Split(string(out), "\n") {
			l = strings.TrimSpace(l)
			if l == "" || strings.Contains(l, " ") || l == "osd" {
				continue
			}
			t.Languages = append(t.Languages, l)
		}
	}
	if t.PDFToText {
		t.Versions["poppler"] = ver("pdftotext", "-v")
	}
	t.Gotenberg = gotenbergURL != ""
	toolsCached = t
	return t
}

// CanOCR reports whether scans can be OCR'd at all.
func (t *Tools) CanOCR() bool { return t.OCRmyPDF && t.Tesseract }

// HasLanguage reports whether every language in a tesseract spec (eng+deu) is installed.
func (t *Tools) HasLanguage(spec string) (missing []string) {
	if len(t.Languages) == 0 {
		return nil // unknown: let tesseract decide
	}
	have := map[string]bool{}
	for _, l := range t.Languages {
		have[l] = true
	}
	for _, l := range strings.Split(spec, "+") {
		l = strings.TrimSpace(l)
		if l != "" && !have[l] {
			missing = append(missing, l)
		}
	}
	return missing
}
