package server

import (
	"archive/zip"
	"bytes"
	"mime/multipart"
	"net/url"
	"testing"
)

type mp struct {
	body  *bytes.Buffer
	ctype string
}

func multipartText(t *testing.T, name, text string) mp {
	return multipartFile(t, name, []byte(text))
}

func multipartFile(t *testing.T, name string, data []byte) mp {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("document", name)
	_, _ = fw.Write(data)
	_ = mw.Close()
	return mp{&buf, mw.FormDataContentType()}
}

func makeZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for n, c := range files {
		w, _ := zw.Create(n)
		_, _ = w.Write([]byte(c))
	}
	_ = zw.Close()
	return buf.Bytes()
}

func urlq(s string) string { return url.QueryEscape(s) }
