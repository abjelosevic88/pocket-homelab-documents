package ocr

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"

	"github.com/abjelosevic88/pocket-homelab-documents/internal/extract"
)

// Options tune one run.
type Options struct {
	Language   string // eng+deu
	Mode       string // skip | redo | force
	Deskew     bool
	Rotate     bool
	Clean      bool
	MaxPages   int
	OutputType string // pdfa | pdf
	ImageDPI   int
	Threads    int
	Timeout    time.Duration
	Gotenberg  string
	TmpDir     string
	Log        func(string)
}

// Result is what came out.
type Result struct {
	Text        string
	ArchivePath string // "" when no PDF twin could be made
	ThumbPath   string
	PageCount   int
	Kind        string // pdf | image | office | text
	Warnings    []string
	Took        time.Duration
}

func (o Options) log(f string, a ...any) {
	if o.Log != nil {
		o.Log(fmt.Sprintf(f, a...))
	}
}

// Process makes the archive PDF, the text and the thumbnail of a file.
// archiveOut and thumbOut are where to write them (the thumbnail is JPEG).
func Process(ctx context.Context, tools *Tools, src, archiveOut, thumbOut string, o Options) (*Result, error) {
	start := time.Now()
	res := &Result{Kind: extract.Kind(filepath.Ext(src))}
	if o.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.Timeout)
		defer cancel()
	}
	if o.TmpDir == "" {
		o.TmpDir = os.TempDir()
	}
	_ = os.MkdirAll(o.TmpDir, 0o755)
	work, err := os.MkdirTemp(o.TmpDir, "ocr-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)

	var pdfIn string // a PDF to OCR / read, when there is one
	switch res.Kind {
	case "pdf":
		pdfIn = src
	case "image":
		pdfIn = src // ocrmypdf takes images directly
	case "office":
		if o.Gotenberg != "" {
			out := filepath.Join(work, "office.pdf")
			if err := gotenbergConvert(ctx, o.Gotenberg, src, out); err != nil {
				res.Warnings = append(res.Warnings, "Gotenberg could not convert the file: "+err.Error())
				o.log("gotenberg: %v", err)
			} else {
				pdfIn = out
				o.log("gotenberg: converted to PDF")
			}
		}
		if extract.CanRead(filepath.Ext(src)) {
			if txt, err := extract.Text(src); err == nil {
				res.Text = txt
				o.log("read %d characters natively", len(txt))
			} else {
				o.log("native read: %v", err)
			}
		}
	case "text":
		if txt, err := extract.Text(src); err == nil {
			res.Text = txt
			o.log("read %d characters", len(txt))
		} else {
			return nil, err
		}
		if o.Gotenberg != "" && isHTMLish(src) {
			out := filepath.Join(work, "page.pdf")
			if err := gotenbergHTML(ctx, o.Gotenberg, src, out); err == nil {
				pdfIn = out
			}
		}
	default:
		return nil, fmt.Errorf("unsupported file type %q", filepath.Ext(src))
	}

	if pdfIn != "" {
		archive, text, warn, err := ocrPDF(ctx, tools, pdfIn, work, o)
		if err != nil {
			return nil, err
		}
		res.Warnings = append(res.Warnings, warn...)
		if archive != "" {
			if err := moveFile(archive, archiveOut); err != nil {
				return nil, err
			}
			res.ArchivePath = archiveOut
		}
		if text != "" && (res.Text == "" || res.Kind != "office") {
			res.Text = text
		}
		pdfForPages := archiveOut
		if res.ArchivePath == "" {
			pdfForPages = pdfIn
		}
		if tools.PDFInfo {
			res.PageCount = PageCount(ctx, pdfForPages)
		}
		if tools.PDFToPPM && res.Kind != "image" || (res.Kind == "image" && res.ArchivePath != "" && tools.PDFToPPM) {
			if err := Thumbnail(ctx, pdfForPages, thumbOut); err == nil {
				res.ThumbPath = thumbOut
			} else {
				o.log("thumbnail: %v", err)
			}
		}
	}
	if res.Kind == "image" && res.ThumbPath == "" {
		if err := imageThumb(src, thumbOut); err == nil {
			res.ThumbPath = thumbOut
			if res.PageCount == 0 {
				res.PageCount = 1
			}
		}
	}
	if res.PageCount == 0 && res.Text != "" && pdfIn == "" {
		res.PageCount = 1
	}
	res.Text = cleanText(res.Text)
	res.Took = time.Since(start)
	return res, nil
}

func isHTMLish(p string) bool {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".html", ".htm", ".md", ".markdown", ".txt":
		return true
	}
	return false
}

// ocrPDF runs ocrmypdf (or just pdftotext when it is missing) and returns the
// archive path (inside work) and the text.
func ocrPDF(ctx context.Context, tools *Tools, in, work string, o Options) (archive, text string, warnings []string, err error) {
	if tools.CanOCR() {
		out := filepath.Join(work, "archive.pdf")
		args := []string{"--output-type", o.OutputType, "-l", o.Language, "--jobs", strconv.Itoa(max(1, o.Threads)), "--optimize", "1", "--skip-big", "250"}
		switch o.Mode {
		case "redo":
			args = append(args, "--redo-ocr")
		case "force":
			args = append(args, "--force-ocr")
		default:
			args = append(args, "--skip-text")
		}
		if o.Deskew && o.Mode != "redo" {
			args = append(args, "--deskew")
		}
		if o.Rotate {
			args = append(args, "--rotate-pages")
		}
		if o.Clean && tools.Unpaper && o.Mode != "redo" {
			args = append(args, "--clean")
		}
		if o.MaxPages > 0 {
			args = append(args, "--pages", "1-"+strconv.Itoa(o.MaxPages))
		}
		if extract.Kind(filepath.Ext(in)) == "image" {
			args = append(args, "--image-dpi", strconv.Itoa(max(72, o.ImageDPI)))
		}
		args = append(args, in, out)
		o.log("ocrmypdf %s", strings.Join(args, " "))
		cmd := exec.CommandContext(ctx, "ocrmypdf", args...)
		cmd.Dir = work
		cmd.Env = append(os.Environ(), "TMPDIR="+work, "HOME="+work)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		runErr := cmd.Run()
		for _, line := range tail(stderr.String(), 30) {
			o.log("  %s", line)
		}
		if runErr != nil {
			exit := -1
			var ee *exec.ExitError
			if errors.As(runErr, &ee) {
				exit = ee.ExitCode()
			}
			if strings.Contains(stderr.String(), "DigitalSignatureError") {
				warnings = append(warnings, "the PDF is digitally signed, so OCR left it untouched to keep the signature valid; its text was read as is")
				exit = -2
			}
			switch exit {
			case -2:
			case 6: // PriorOcrFoundError: the PDF already has text and skip-text was not enough
				warnings = append(warnings, "the PDF already had a text layer; it was kept as is")
			case 2, 10: // bad args / already done
				warnings = append(warnings, "ocrmypdf: "+strings.TrimSpace(lastLine(stderr.String())))
			default:
				if ctx.Err() != nil {
					return "", "", nil, fmt.Errorf("OCR timed out after %s", o.Timeout)
				}
				if exit == 1 && strings.Contains(stderr.String(), "DynamicPdfError") {
					warnings = append(warnings, "the PDF has dynamic XFA forms; its text was read without OCR")
				} else if extract.Kind(filepath.Ext(in)) == "pdf" {
					// A PDF OCRmyPDF cannot rewrite (broken structure, odd producers):
					// keep the original as it is and read whatever text it carries.
					warnings = append(warnings, fmt.Sprintf("OCRmyPDF could not process this PDF (exit %d: %s); the original is kept and its own text was read", exit, strings.TrimSpace(lastLine(stderr.String()))))
				} else {
					return "", "", nil, fmt.Errorf("ocrmypdf failed (exit %d): %s", exit, strings.TrimSpace(lastLine(stderr.String())))
				}
			}
		}
		if fi, statErr := os.Stat(out); statErr == nil && fi.Size() > 0 {
			archive = out
		} else if extract.Kind(filepath.Ext(in)) == "image" {
			// no PDF twin for an image means no text either
			return "", "", warnings, errors.New("ocrmypdf produced no PDF for the image")
		}
	} else {
		warnings = append(warnings, "ocrmypdf or tesseract is not installed: no OCR, only the text the PDF already carries")
		if extract.Kind(filepath.Ext(in)) == "pdf" {
			out := filepath.Join(work, "archive.pdf")
			if err := copyFile(in, out); err == nil {
				archive = out
			}
		}
	}
	textFrom := archive
	if textFrom == "" {
		textFrom = in
	}
	if tools.PDFToText && extract.Kind(filepath.Ext(textFrom)) == "pdf" {
		text, err = PDFText(ctx, textFrom)
		if err != nil {
			o.log("pdftotext: %v", err)
			err = nil
		}
	}
	return archive, text, warnings, nil
}

// PDFText reads the text layer of a PDF with pdftotext.
func PDFText(ctx context.Context, pdf string) (string, error) {
	cmd := exec.CommandContext(ctx, "pdftotext", "-layout", "-enc", "UTF-8", pdf, "-")
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%v: %s", err, strings.TrimSpace(stderr.String()))
	}
	return out.String(), nil
}

// PageCount asks pdfinfo how many pages a PDF has.
func PageCount(ctx context.Context, pdf string) int {
	out, err := exec.CommandContext(ctx, "pdfinfo", pdf).Output()
	if err != nil {
		return 0
	}
	m := regexp.MustCompile(`(?m)^Pages:\s+(\d+)`).FindSubmatch(out)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(string(m[1]))
	return n
}

// Thumbnail renders page 1 of a PDF as a JPEG about 600 px wide.
func Thumbnail(ctx context.Context, pdf, out string) error {
	return RenderPage(ctx, pdf, 1, 72, out, 600)
}

// RenderPage renders one page as JPEG at the given dpi, scaled to width px.
func RenderPage(ctx context.Context, pdf string, page, dpi int, out string, width int) error {
	_ = os.MkdirAll(filepath.Dir(out), 0o755)
	base := strings.TrimSuffix(out, ".jpg")
	args := []string{"-f", strconv.Itoa(page), "-l", strconv.Itoa(page), "-r", strconv.Itoa(dpi), "-jpeg", "-jpegopt", "quality=82", "-singlefile"}
	if width > 0 {
		args = append(args, "-scale-to-x", strconv.Itoa(width), "-scale-to-y", "-1")
	}
	args = append(args, pdf, base)
	cmd := exec.CommandContext(ctx, "pdftoppm", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pdftoppm: %v %s", err, strings.TrimSpace(stderr.String()))
	}
	if _, err := os.Stat(base + ".jpg"); err != nil {
		return errors.New("pdftoppm wrote nothing")
	}
	return nil
}

// imageThumb scales an image file down to a JPEG thumbnail (no poppler needed).
func imageThumb(src, out string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return err
	}
	b := img.Bounds()
	w := 600
	h := b.Dy() * w / max(1, b.Dx())
	if b.Dx() < w {
		w, h = b.Dx(), b.Dy()
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, max(1, h)))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)
	_ = os.MkdirAll(filepath.Dir(out), 0o755)
	o, err := os.Create(out)
	if err != nil {
		return err
	}
	defer o.Close()
	return jpeg.Encode(o, dst, &jpeg.Options{Quality: 82})
}

// ---- gotenberg ----

func gotenbergConvert(ctx context.Context, base, src, out string) error {
	return gotenbergPost(ctx, base+"/forms/libreoffice/convert", src, filepath.Base(src), out, nil)
}

func gotenbergHTML(ctx context.Context, base, src, out string) error {
	name := "index.html"
	body := src
	ext := strings.ToLower(filepath.Ext(src))
	if ext != ".html" && ext != ".htm" {
		// wrap plain text / markdown in <pre>
		b, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		tmp := filepath.Join(filepath.Dir(out), "index.html")
		esc := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(string(b))
		if err := os.WriteFile(tmp, []byte("<html><head><meta charset=\"utf-8\"><style>body{font:13px/1.45 sans-serif;margin:2cm;white-space:pre-wrap;word-wrap:break-word}</style></head><body>"+esc+"</body></html>"), 0o644); err != nil {
			return err
		}
		body = tmp
	}
	return gotenbergPost(ctx, base+"/forms/chromium/convert/html", body, name, out, map[string]string{"paperWidth": "8.27", "paperHeight": "11.7"})
}

func gotenbergPost(ctx context.Context, url, src, name, out string, fields map[string]string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("files", name)
	if err != nil {
		return err
	}
	if _, err := io.Copy(fw, f); err != nil {
		return err
	}
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	_ = mw.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	res, err := (&http.Client{Timeout: 5 * time.Minute}).Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return fmt.Errorf("HTTP %d: %s", res.StatusCode, strings.TrimSpace(string(b)))
	}
	o, err := os.Create(out)
	if err != nil {
		return err
	}
	defer o.Close()
	_, err = io.Copy(o, res.Body)
	return err
}

// ---- helpers ----

func tail(s string, n int) []string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

func lastLine(s string) string {
	l := tail(s, 1)
	if len(l) == 0 {
		return ""
	}
	return l[0]
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

func copyFile(src, dst string) error {
	_ = os.MkdirAll(filepath.Dir(dst), 0o755)
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

// Checksum returns the sha256 of a file.
func Checksum(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

var (
	ctrlRe   = regexp.MustCompile(`[\x00-\x08\x0b\x0c\x0e-\x1f]`)
	spacesRe = regexp.MustCompile(`[ \t]{2,}`)
	blankRe  = regexp.MustCompile(`\n{3,}`)
)

// cleanText drops control characters and squeezes whitespace while keeping
// the line layout pdftotext produced.
func cleanText(s string) string {
	s = strings.ToValidUTF8(s, "")
	s = ctrlRe.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "\f", "\n\n")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(spacesRe.ReplaceAllString(l, "  "), " \t")
	}
	s = strings.Join(lines, "\n")
	s = blankRe.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}
