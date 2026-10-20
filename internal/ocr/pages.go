package ocr

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Page tools on PDFs through qpdf, and on images in Go.

func runQPDF(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "qpdf", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 3 {
			return nil // warnings only; the output was written
		}
		return fmt.Errorf("qpdf: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// pageSpec turns page numbers into a qpdf page range ("1,3-5").
func pageSpec(pages []int) string {
	sort.Ints(pages)
	var parts []string
	for i := 0; i < len(pages); {
		j := i
		for j+1 < len(pages) && pages[j+1] == pages[j]+1 {
			j++
		}
		if j > i {
			parts = append(parts, fmt.Sprintf("%d-%d", pages[i], pages[j]))
		} else {
			parts = append(parts, strconv.Itoa(pages[i]))
		}
		i = j + 1
	}
	return strings.Join(parts, ",")
}

// RotatePDF rotates the given pages (1-based; empty = all) by degrees (90, 180, 270 or negatives).
func RotatePDF(ctx context.Context, in, out string, pages []int, degrees int) error {
	degrees = ((degrees % 360) + 360) % 360
	if degrees == 0 {
		return copyFile(in, out)
	}
	spec := "1-z"
	if len(pages) > 0 {
		spec = pageSpec(pages)
	}
	return runQPDF(ctx, in, "--rotate=+"+strconv.Itoa(degrees)+":"+spec, out)
}

// DeletePages writes the PDF without the given pages.
func DeletePages(ctx context.Context, in, out string, pages []int, total int) error {
	drop := map[int]bool{}
	for _, p := range pages {
		drop[p] = true
	}
	var keep []int
	for i := 1; i <= total; i++ {
		if !drop[i] {
			keep = append(keep, i)
		}
	}
	if len(keep) == 0 {
		return errors.New("that would leave no pages")
	}
	return runQPDF(ctx, "--empty", "--pages", in, pageSpec(keep), "--", out)
}

// ExtractPages writes the given pages (in the given order) to a new PDF.
func ExtractPages(ctx context.Context, in, out string, pages []int) error {
	if len(pages) == 0 {
		return errors.New("no pages")
	}
	var parts []string
	for _, p := range pages {
		parts = append(parts, strconv.Itoa(p))
	}
	return runQPDF(ctx, "--empty", "--pages", in, strings.Join(parts, ","), "--", out)
}

// MergePDFs concatenates PDFs.
func MergePDFs(ctx context.Context, out string, ins ...string) error {
	if len(ins) == 0 {
		return errors.New("nothing to merge")
	}
	args := []string{"--empty", "--pages"}
	for _, in := range ins {
		args = append(args, in, "1-z")
	}
	args = append(args, "--", out)
	return runQPDF(ctx, args...)
}

// RotateImage rotates a PNG/JPEG by 90, 180 or 270 degrees clockwise and
// writes it back in the same format.
func RotateImage(in, out string, degrees int) error {
	degrees = ((degrees % 360) + 360) % 360
	f, err := os.Open(in)
	if err != nil {
		return err
	}
	img, format, err := image.Decode(f)
	f.Close()
	if err != nil {
		return err
	}
	b := img.Bounds()
	var dst *image.RGBA
	switch degrees {
	case 90, 270:
		dst = image.NewRGBA(image.Rect(0, 0, b.Dy(), b.Dx()))
	default:
		dst = image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := img.At(x, y)
			switch degrees {
			case 90:
				dst.Set(b.Max.Y-1-y, x-b.Min.X, c)
			case 180:
				dst.Set(b.Max.X-1-x, b.Max.Y-1-y, c)
			case 270:
				dst.Set(y-b.Min.Y, b.Max.X-1-x, c)
			default:
				dst.Set(x-b.Min.X, y-b.Min.Y, c)
			}
		}
	}
	_ = os.MkdirAll(filepath.Dir(out), 0o755)
	o, err := os.Create(out)
	if err != nil {
		return err
	}
	defer o.Close()
	if format == "png" {
		return png.Encode(o, dst)
	}
	return jpeg.Encode(o, dst, &jpeg.Options{Quality: 92})
}

// ---- barcodes ----

// Barcode is one code found on a page.
type Barcode struct {
	Page  int    `json:"page"`
	Type  string `json:"type"`
	Value string `json:"value"`
}

// ReadBarcodes renders a PDF's pages (or takes an image) and runs zbarimg on each.
// maxPages caps the work on long scans (0 = all).
func ReadBarcodes(ctx context.Context, tools *Tools, src, tmpDir string, maxPages int) ([]Barcode, error) {
	if !tools.ZBar {
		return nil, errors.New("zbarimg is not installed")
	}
	work, err := os.MkdirTemp(tmpDir, "barcodes-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)
	var images []string
	if strings.EqualFold(filepath.Ext(src), ".pdf") {
		if !tools.PDFToPPM {
			return nil, errors.New("pdftoppm is not installed")
		}
		args := []string{"-r", "150", "-png"}
		if maxPages > 0 {
			args = append(args, "-l", strconv.Itoa(maxPages))
		}
		args = append(args, src, filepath.Join(work, "p"))
		if out, err := exec.CommandContext(ctx, "pdftoppm", args...).CombinedOutput(); err != nil {
			return nil, fmt.Errorf("pdftoppm: %v %s", err, strings.TrimSpace(string(out)))
		}
		entries, _ := os.ReadDir(work)
		for _, e := range entries {
			images = append(images, filepath.Join(work, e.Name()))
		}
		sort.Strings(images)
	} else {
		images = []string{src}
	}
	var out []Barcode
	for i, img := range images {
		res, err := exec.CommandContext(ctx, "zbarimg", "-q", "--raw", "-Sdisable", "-Scode128.enable", "-Scode39.enable", "-Sqrcode.enable", "-Sean13.enable", "-Si25.enable", "-Sdatabar.enable", "-Scodabar.enable", img).Output()
		if err != nil {
			continue // exit 4 = nothing found
		}
		for _, line := range strings.Split(strings.TrimSpace(string(res)), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				out = append(out, Barcode{Page: i + 1, Value: line})
			}
		}
	}
	return out, nil
}
