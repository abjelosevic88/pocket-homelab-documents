package ocr

import (
	"os"
	"path/filepath"
	"testing"
)

// Tesseract needs configs/hocr next to the language files; without it OCRmyPDF
// gets no hOCR back and every scan fails.
func TestPrepareTessdataLinksConfigs(t *testing.T) {
	sys := t.TempDir()
	_ = os.MkdirAll(filepath.Join(sys, "configs"), 0o755)
	_ = os.WriteFile(filepath.Join(sys, "configs", "hocr"), []byte("tessedit_create_hocr 1"), 0o644)
	_ = os.WriteFile(filepath.Join(sys, "eng.traineddata"), []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(sys, "pdf.ttf"), []byte("x"), 0o644)
	old := systemTessdata
	systemTessdata = sys
	defer func() { systemTessdata = old }()
	t.Setenv("TESSDATA_PREFIX", "")
	dir := filepath.Join(t.TempDir(), "tessdata")
	if got := PrepareTessdata(dir); got != dir || os.Getenv("TESSDATA_PREFIX") != dir {
		t.Fatalf("prefix: %q %q", got, os.Getenv("TESSDATA_PREFIX"))
	}
	for _, p := range []string{"configs/hocr", "eng.traineddata", "pdf.ttf"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Errorf("%s not reachable: %v", p, err)
		}
	}
	langs := Languages(dir)
	found := false
	for _, l := range langs {
		if l.Code == "eng" && l.Installed && l.Source == "system" {
			found = true
		}
	}
	if !found {
		t.Errorf("eng not listed as installed: %+v", langs[:3])
	}
}
