package ocr

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Tesseract language packs can be added at runtime: they are downloaded into
// DATA_DIR/tessdata, the system packs are linked in beside them, and
// TESSDATA_PREFIX points Tesseract at that folder.

// Language is one Tesseract pack.
type Language struct {
	Code      string `json:"code"`
	Name      string `json:"name"`
	Installed bool   `json:"installed"`
	Source    string `json:"source,omitempty"` // system | downloaded
}

// Known lists the packs available from tessdata_best, with names.
var Known = map[string]string{
	"afr": "Afrikaans", "ara": "Arabic", "aze": "Azerbaijani", "bel": "Belarusian", "ben": "Bengali", "bos": "Bosnian", "bul": "Bulgarian", "cat": "Catalan", "ces": "Czech",
	"chi_sim": "Chinese (simplified)", "chi_tra": "Chinese (traditional)", "cym": "Welsh", "dan": "Danish", "deu": "German", "ell": "Greek", "eng": "English", "est": "Estonian",
	"eus": "Basque", "fas": "Persian", "fin": "Finnish", "fra": "French", "gle": "Irish", "glg": "Galician", "heb": "Hebrew", "hin": "Hindi", "hrv": "Croatian", "hun": "Hungarian",
	"hye": "Armenian", "ind": "Indonesian", "isl": "Icelandic", "ita": "Italian", "jpn": "Japanese", "kat": "Georgian", "kaz": "Kazakh", "kor": "Korean", "lat": "Latin",
	"lav": "Latvian", "lit": "Lithuanian", "mkd": "Macedonian", "msa": "Malay", "nld": "Dutch", "nor": "Norwegian", "pol": "Polish", "por": "Portuguese", "ron": "Romanian",
	"rus": "Russian", "slk": "Slovak", "slv": "Slovenian", "spa": "Spanish", "sqi": "Albanian", "srp": "Serbian (Cyrillic)", "srp_latn": "Serbian (Latin)", "swe": "Swedish",
	"tha": "Thai", "tur": "Turkish", "ukr": "Ukrainian", "uzb": "Uzbek", "uzb_cyrl": "Uzbek (Cyrillic)", "vie": "Vietnamese", "osd": "Orientation & script detection",
}

var langCode = regexp.MustCompile(`^[a-z]{3}(_[a-z]+)?$`)

var systemTessdata = "/usr/share/tessdata"

// PrepareTessdata links the system packs (and Tesseract's configs) into dir
// and points Tesseract at it. Returns the directory in use ("" when the system folder is used as is).
func PrepareTessdata(dir string) string {
	if dir == "" {
		return ""
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	sys := systemTessdata
	if v := os.Getenv("TESSDATA_PREFIX"); v != "" && v != dir {
		sys = v
	}
	// Everything is linked, not only the language files: Tesseract also reads
	// configs/ (the hocr and txt output configs OCRmyPDF asks for), tessconfigs/
	// and pdf.ttf from the same folder, and fails without a word when they are missing.
	if entries, err := os.ReadDir(sys); err == nil {
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".") {
				continue
			}
			dst := filepath.Join(dir, e.Name())
			if _, err := os.Lstat(dst); err == nil {
				continue
			}
			_ = os.Symlink(filepath.Join(sys, e.Name()), dst)
		}
	}
	_ = os.Setenv("TESSDATA_PREFIX", dir)
	return dir
}

// Languages lists what is installed in dir (or the system folder) and what could be.
func Languages(dir string) []Language {
	installed := map[string]string{}
	look := dir
	if look == "" {
		look = systemTessdata
	}
	if entries, err := os.ReadDir(look); err == nil {
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".traineddata") {
				continue
			}
			code := strings.TrimSuffix(e.Name(), ".traineddata")
			src := "downloaded"
			if e.Type()&os.ModeSymlink != 0 {
				src = "system"
			}
			installed[code] = src
		}
	}
	var out []Language
	for code, name := range Known {
		l := Language{Code: code, Name: name}
		if src, ok := installed[code]; ok {
			l.Installed, l.Source = true, src
		}
		out = append(out, l)
	}
	for code, src := range installed {
		if _, known := Known[code]; !known {
			out = append(out, Language{Code: code, Name: code, Installed: true, Source: src})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Installed != out[j].Installed {
			return out[i].Installed
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// InstallLanguage downloads a pack from tessdata_best into dir.
func InstallLanguage(ctx context.Context, dir, code string) error {
	code = strings.ToLower(strings.TrimSpace(code))
	if dir == "" || !langCode.MatchString(code) {
		return errors.New("not a language code")
	}
	url := "https://github.com/tesseract-ocr/tessdata_best/raw/main/" + code + ".traineddata"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	res, err := (&http.Client{Timeout: 5 * time.Minute}).Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("no such language pack (%s answered HTTP %d)", url, res.StatusCode)
	}
	tmp, err := os.CreateTemp(dir, ".dl-*")
	if err != nil {
		return err
	}
	n, err := io.Copy(tmp, io.LimitReader(res.Body, 200<<20))
	tmp.Close()
	if err != nil || n < 100<<10 {
		os.Remove(tmp.Name())
		return fmt.Errorf("download failed")
	}
	dst := filepath.Join(dir, code+".traineddata")
	_ = os.Remove(dst)
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return err
	}
	ResetCache()
	return nil
}

// RemoveLanguage deletes a downloaded pack (system ones stay).
func RemoveLanguage(dir, code string) error {
	if dir == "" || !langCode.MatchString(code) {
		return errors.New("not a language code")
	}
	p := filepath.Join(dir, code+".traineddata")
	fi, err := os.Lstat(p)
	if err != nil {
		return errors.New("not installed")
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return errors.New("that pack came with the image; it cannot be removed")
	}
	ResetCache()
	return os.Remove(p)
}
