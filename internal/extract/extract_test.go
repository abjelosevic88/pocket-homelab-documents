package extract

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func writeZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, body := range files {
		w, _ := zw.Create(name)
		_, _ = w.Write([]byte(body))
	}
	_ = zw.Close()
	_ = f.Close()
}

func TestDocx(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.docx")
	writeZip(t, p, map[string]string{"word/document.xml": `<w:document><w:body><w:p><w:r><w:t>Hello</w:t></w:r><w:r><w:t xml:space="preserve"> world</w:t></w:r></w:p><w:p><w:r><w:t>Second &amp; last</w:t></w:r></w:p></w:body></w:document>`})
	got, err := Text(p)
	if err != nil || got != "Hello world\nSecond & last" {
		t.Fatalf("%q %v", got, err)
	}
}

func TestXlsx(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.xlsx")
	writeZip(t, p, map[string]string{
		"xl/sharedStrings.xml":     `<sst><si><t>Item</t></si><si><r><t>Pri</t></r><r><t>ce</t></r></si></sst>`,
		"xl/worksheets/sheet1.xml": `<worksheet><sheetData><row><c t="s"><v>0</v></c><c t="s"><v>1</v></c></row><row><c><v>42</v></c><c t="inlineStr"><is><t>x</t></is></c></row></sheetData></worksheet>`,
	})
	got, err := Text(p)
	if err != nil || got != "Item\tPrice\n42\tx" {
		t.Fatalf("%q %v", got, err)
	}
}

func TestEmlAndHTML(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.eml")
	_ = os.WriteFile(p, []byte("From: a@b.c\r\nSubject: Hi there\r\nContent-Type: text/html\r\n\r\n<html><body><p>Line <b>one</b></p><p>Two</p></body></html>\r\n"), 0o644)
	got, err := Text(p)
	if err != nil || got != "From: a@b.c\nSubject: Hi there\n\nLine one\nTwo" {
		t.Fatalf("%q %v", got, err)
	}
	if Kind("PDF") != "pdf" || Kind("jpeg") != "image" || Kind("docx") != "office" || Kind("exe") != "" {
		t.Fatal("kinds")
	}
}
