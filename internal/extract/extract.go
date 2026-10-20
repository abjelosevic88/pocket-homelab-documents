// Package extract reads the text of files that are not scans: office
// documents (docx, xlsx, pptx, odt, ods, odp), plain text, Markdown, HTML,
// CSV and e-mails. No external tool is needed; OCR is for the rest.
package extract

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Kinds of file by extension. "pdf" and "image" go to OCR; "office" becomes
// a PDF through Gotenberg when there is one; "text" is read as is.
func Kind(ext string) string {
	switch strings.ToLower(strings.TrimPrefix(ext, ".")) {
	case "pdf":
		return "pdf"
	case "png", "jpg", "jpeg", "tif", "tiff", "webp", "gif", "bmp", "heic", "heif", "jp2":
		return "image"
	case "docx", "xlsx", "pptx", "odt", "ods", "odp", "doc", "xls", "ppt", "rtf":
		return "office"
	case "txt", "md", "markdown", "csv", "tsv", "html", "htm", "json", "xml", "log", "eml":
		return "text"
	}
	return ""
}

// Mime guesses a content type from the extension.
func Mime(ext string) string {
	ext = strings.ToLower(strings.TrimPrefix(ext, "."))
	switch ext {
	case "md", "markdown":
		return "text/markdown"
	case "eml":
		return "message/rfc822"
	case "heic":
		return "image/heic"
	case "jp2":
		return "image/jp2"
	}
	if m := mime.TypeByExtension("." + ext); m != "" {
		return strings.Split(m, ";")[0]
	}
	return "application/octet-stream"
}

// CanRead reports whether Text can read the file.
func CanRead(ext string) bool {
	k := Kind(ext)
	if k == "text" {
		return true
	}
	switch strings.ToLower(strings.TrimPrefix(ext, ".")) {
	case "docx", "xlsx", "pptx", "odt", "ods", "odp":
		return true
	}
	return false
}

// Text reads the text of a file by its extension.
func Text(path string) (string, error) {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
	switch ext {
	case "docx":
		return zipXMLText(path, []string{"word/document.xml", "word/footnotes.xml", "word/endnotes.xml"}, docxBreaks)
	case "pptx":
		return pptxText(path)
	case "xlsx":
		return xlsxText(path)
	case "odt", "ods", "odp":
		return zipXMLText(path, []string{"content.xml"}, odfBreaks)
	case "html", "htm":
		b, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		return HTMLText(string(b)), nil
	case "eml":
		return emlText(path)
	case "txt", "md", "markdown", "csv", "tsv", "json", "xml", "log":
		b, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		return strings.ToValidUTF8(string(b), ""), nil
	}
	return "", errors.New("no text reader for ." + ext)
}

var (
	docxBreaks = regexp.MustCompile(`</w:p>|<w:br[^>]*/>|<w:tab[^>]*/>|</w:tr>`)
	odfBreaks  = regexp.MustCompile(`</text:p>|</text:h>|<text:line-break[^>]*/>|<text:tab[^>]*/>|</table:table-row>`)
	tagRe      = regexp.MustCompile(`<[^>]+>`)
	multiSpace = regexp.MustCompile(` {2,}`)
)

func zipXMLText(path string, members []string, breaks *regexp.Regexp) (string, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return "", err
	}
	defer zr.Close()
	var out []string
	for _, f := range zr.File {
		for _, m := range members {
			if f.Name == m {
				rc, err := f.Open()
				if err != nil {
					continue
				}
				b, _ := io.ReadAll(io.LimitReader(rc, 64<<20))
				rc.Close()
				out = append(out, xmlToText(string(b), breaks))
			}
		}
	}
	if len(out) == 0 {
		return "", errors.New("no text in the document")
	}
	return strings.Join(out, "\n\n"), nil
}

func xmlToText(s string, breaks *regexp.Regexp) string {
	s = breaks.ReplaceAllString(s, "\n")
	s = tagRe.ReplaceAllString(s, "")
	s = unescape(s)
	return tidy(s)
}

func unescape(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, nil)
	r := strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&", "&quot;", `"`, "&apos;", "'", "&#39;", "'", "&nbsp;", " ", "&#160;", " ")
	return r.Replace(s)
}

// tidy collapses runs of blank lines and trailing spaces.
func tidy(s string) string {
	lines := strings.Split(s, "\n")
	var out []string
	blank := 0
	for _, l := range lines {
		l = strings.TrimSpace(multiSpace.ReplaceAllString(l, " "))
		if l == "" {
			blank++
			if blank > 1 {
				continue
			}
			out = append(out, "")
			continue
		}
		blank = 0
		out = append(out, l)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func pptxText(path string) (string, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return "", err
	}
	defer zr.Close()
	type slide struct {
		n    int
		text string
	}
	var slides []slide
	re := regexp.MustCompile(`^ppt/slides/slide(\d+)\.xml$`)
	for _, f := range zr.File {
		m := re.FindStringSubmatch(f.Name)
		if m == nil {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		b, _ := io.ReadAll(io.LimitReader(rc, 16<<20))
		rc.Close()
		n := 0
		for _, c := range m[1] {
			n = n*10 + int(c-'0')
		}
		slides = append(slides, slide{n, xmlToText(strings.ReplaceAll(string(b), "</a:p>", "</a:p>\n"), regexp.MustCompile(`</a:p>`))})
	}
	sort.Slice(slides, func(i, j int) bool { return slides[i].n < slides[j].n })
	var out []string
	for _, s := range slides {
		out = append(out, s.text)
	}
	if len(out) == 0 {
		return "", errors.New("no slides")
	}
	return strings.Join(out, "\n\n"), nil
}

func xlsxText(path string) (string, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return "", err
	}
	defer zr.Close()
	read := func(name string) []byte {
		for _, f := range zr.File {
			if f.Name == name {
				rc, err := f.Open()
				if err != nil {
					return nil
				}
				defer rc.Close()
				b, _ := io.ReadAll(io.LimitReader(rc, 64<<20))
				return b
			}
		}
		return nil
	}
	// shared strings
	var shared []string
	if b := read("xl/sharedStrings.xml"); b != nil {
		type si struct {
			T []string `xml:"t"`
			R []struct {
				T string `xml:"t"`
			} `xml:"r"`
		}
		var sst struct {
			SI []si `xml:"si"`
		}
		if xml.Unmarshal(b, &sst) == nil {
			for _, s := range sst.SI {
				txt := strings.Join(s.T, "")
				for _, r := range s.R {
					txt += r.T
				}
				shared = append(shared, txt)
			}
		}
	}
	var out []string
	for _, f := range zr.File {
		if !strings.HasPrefix(f.Name, "xl/worksheets/sheet") || !strings.HasSuffix(f.Name, ".xml") {
			continue
		}
		b := read(f.Name)
		var ws struct {
			Rows []struct {
				Cells []struct {
					T  string `xml:"t,attr"`
					V  string `xml:"v"`
					IS struct {
						T string `xml:"t"`
					} `xml:"is"`
				} `xml:"c"`
			} `xml:"sheetData>row"`
		}
		if xml.Unmarshal(b, &ws) != nil {
			continue
		}
		for _, r := range ws.Rows {
			var cells []string
			for _, c := range r.Cells {
				v := c.V
				switch c.T {
				case "s":
					idx := 0
					for _, ch := range c.V {
						if ch < '0' || ch > '9' {
							idx = -1
							break
						}
						idx = idx*10 + int(ch-'0')
					}
					if idx >= 0 && idx < len(shared) {
						v = shared[idx]
					}
				case "inlineStr":
					v = c.IS.T
				}
				if strings.TrimSpace(v) != "" {
					cells = append(cells, v)
				}
			}
			if len(cells) > 0 {
				out = append(out, strings.Join(cells, "\t"))
			}
		}
		out = append(out, "")
	}
	if len(out) == 0 {
		return "", errors.New("no cells")
	}
	return tidy(strings.Join(out, "\n")), nil
}

var (
	scriptRe = regexp.MustCompile(`(?is)<(script|style|head)[^>]*>.*?</(script|style|head)>`)
	blockRe  = regexp.MustCompile(`(?i)</(p|div|h[1-6]|li|tr|br|section|article|header|footer|blockquote|pre)>|<br\s*/?>|<hr\s*/?>`)
)

// HTMLText strips markup from HTML.
func HTMLText(s string) string {
	s = scriptRe.ReplaceAllString(s, "")
	s = blockRe.ReplaceAllString(s, "\n")
	s = tagRe.ReplaceAllString(s, " ")
	return tidy(unescape(s))
}

func emlText(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	msg, err := mail.ReadMessage(f)
	if err != nil {
		return "", err
	}
	var out []string
	for _, h := range []string{"From", "To", "Cc", "Date", "Subject"} {
		if v := msg.Header.Get(h); v != "" {
			if dec, err := new(mime.WordDecoder).DecodeHeader(v); err == nil {
				v = dec
			}
			out = append(out, h+": "+v)
		}
	}
	out = append(out, "")
	body := partText(msg.Header.Get("Content-Type"), msg.Header.Get("Content-Transfer-Encoding"), msg.Body)
	out = append(out, body)
	return tidy(strings.Join(out, "\n")), nil
}

// partText walks a MIME body and returns the text parts (plain preferred, HTML stripped).
func partText(ctype, enc string, r io.Reader) string {
	mt, params, err := mime.ParseMediaType(ctype)
	if err != nil {
		mt = "text/plain"
	}
	if strings.HasPrefix(mt, "multipart/") {
		mr := multipart.NewReader(r, params["boundary"])
		var plain, html []string
		for {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			pt, _, _ := mime.ParseMediaType(p.Header.Get("Content-Type"))
			txt := partText(p.Header.Get("Content-Type"), p.Header.Get("Content-Transfer-Encoding"), p)
			switch {
			case pt == "text/plain":
				plain = append(plain, txt)
			case pt == "text/html":
				html = append(html, txt)
			case strings.HasPrefix(pt, "multipart/"):
				plain = append(plain, txt)
			}
		}
		if len(plain) > 0 {
			return strings.Join(plain, "\n")
		}
		return strings.Join(html, "\n")
	}
	b, _ := io.ReadAll(io.LimitReader(decodeBody(enc, r), 16<<20))
	if mt == "text/html" {
		return HTMLText(string(b))
	}
	if strings.HasPrefix(mt, "text/") {
		return string(b)
	}
	return ""
}
