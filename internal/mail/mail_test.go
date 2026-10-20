package mail

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

var sample = "From: =?UTF-8?Q?Elektroprivreda_BiH?= <racuni@epbih.ba>\r\n" +
	"To: Alice <alice@example.com>, bob@example.com\r\n" +
	"Cc: carol@example.com\r\n" +
	"Subject: =?utf-8?B?UmHEjXVuIHphIHN0cnVqdQ==?=\r\n" +
	"Date: Sat, 14 Mar 2026 10:15:00 +0100\r\n" +
	"Message-ID: <abc123@epbih.ba>\r\n" +
	"MIME-Version: 1.0\r\n" +
	"Content-Type: multipart/mixed; boundary=\"outer\"\r\n" +
	"\r\n" +
	"--outer\r\n" +
	"Content-Type: multipart/alternative; boundary=\"inner\"\r\n" +
	"\r\n" +
	"--inner\r\n" +
	"Content-Type: text/plain; charset=utf-8\r\n" +
	"Content-Transfer-Encoding: quoted-printable\r\n" +
	"\r\n" +
	"Po=C5=A1tovani, u prilogu je ra=C4=8Dun.\r\n" +
	"--inner\r\n" +
	"Content-Type: text/html; charset=utf-8\r\n" +
	"\r\n" +
	"<p>Poštovani, <b>u prilogu</b> je račun.</p>\r\n" +
	"--inner--\r\n" +
	"--outer\r\n" +
	"Content-Type: application/pdf; name=\"racun-2026-03.pdf\"\r\n" +
	"Content-Disposition: attachment; filename=\"racun-2026-03.pdf\"\r\n" +
	"Content-Transfer-Encoding: base64\r\n" +
	"\r\n" +
	"JVBERi0xLjQKJcOkw7zDtsOfCjEgMCBvYmoKPDwvVHlwZS9DYXRhbG9nPj4KZW5kb2JqCg==\r\n" +
	"--outer\r\n" +
	"Content-Type: image/png\r\n" +
	"Content-Disposition: inline; filename=\"logo.png\"\r\n" +
	"Content-Transfer-Encoding: base64\r\n" +
	"\r\n" +
	base64.StdEncoding.EncodeToString([]byte("pngpngpng")) + "\r\n" +
	"--outer--\r\n"

func TestParse(t *testing.T) {
	m, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if m.From.Name != "Elektroprivreda BiH" || m.From.Address != "racuni@epbih.ba" {
		t.Errorf("from: %+v", m.From)
	}
	if len(m.To) != 3 || m.To[0].Name != "Alice" || m.To[1].Address != "bob@example.com" || m.To[2].Address != "carol@example.com" {
		t.Errorf("to: %+v", m.To)
	}
	if m.Subject != "Račun za struju" {
		t.Errorf("subject: %q", m.Subject)
	}
	if m.MessageID != "<abc123@epbih.ba>" {
		t.Errorf("message id: %q", m.MessageID)
	}
	if m.Date.Format("2006-01-02") != "2026-03-14" {
		t.Errorf("date: %v", m.Date)
	}
	if !strings.Contains(m.Text, "Poštovani, u prilogu je račun.") {
		t.Errorf("text: %q", m.Text)
	}
	if len(m.Attachments) != 2 {
		t.Fatalf("attachments: %d", len(m.Attachments))
	}
	if m.Attachments[0].Name != "racun-2026-03.pdf" || m.Attachments[0].Mime != "application/pdf" || !strings.HasPrefix(string(m.Attachments[0].Data), "%PDF-1.4") {
		t.Errorf("pdf: %s %s %q", m.Attachments[0].Name, m.Attachments[0].Mime, m.Attachments[0].Data[:8])
	}
	if m.Attachments[1].Name != "logo.png" || string(m.Attachments[1].Data) != "pngpngpng" {
		t.Errorf("inline image: %+v", m.Attachments[1].Name)
	}
	if len(m.Raw) != len(sample) {
		t.Errorf("raw kept: %d", len(m.Raw))
	}
}

func TestParseHTMLOnly(t *testing.T) {
	m, err := Parse([]byte("From: a@b.c\r\nSubject: hi\r\nContent-Type: text/html\r\n\r\n<html><style>p{}</style><body><p>Hello&nbsp;<b>world</b></p><p>Bye</p></body></html>"))
	if err != nil {
		t.Fatal(err)
	}
	if m.Text != "Hello world\nBye" {
		t.Errorf("stripped html: %q", m.Text)
	}
	if len(m.Attachments) != 0 {
		t.Errorf("attachments from html-only mail: %d", len(m.Attachments))
	}
}

func TestFilter(t *testing.T) {
	m, _ := Parse([]byte(sample))
	now := time.Date(2026, 3, 20, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		f    Filter
		want bool
	}{
		{"empty matches", Filter{}, true},
		{"from name", Filter{From: "elektroprivreda"}, true},
		{"from address", Filter{From: "EPBIH.BA"}, true},
		{"from miss", Filter{From: "telekom"}, false},
		{"to cc", Filter{To: "carol@"}, true},
		{"to miss", Filter{To: "dave"}, false},
		{"subject", Filter{Subject: "račun"}, true},
		{"subject case", Filter{Subject: "RAČUN ZA"}, true},
		{"subject miss", Filter{Subject: "faktura"}, false},
		{"body", Filter{Body: "u prilogu"}, true},
		{"body miss", Filter{Body: "otkaz"}, false},
		{"attachment glob", Filter{Attachment: "*.pdf"}, true},
		{"attachment glob case", Filter{Attachment: "RACUN-*.PDF"}, true},
		{"attachment contains", Filter{Attachment: "racun"}, true},
		{"attachment miss", Filter{Attachment: "*.docx"}, false},
		{"attachment list", Filter{Attachment: "*.docx, *.pdf"}, true},
		{"age ok", Filter{MaxAgeDays: 30}, true},
		{"age too old", Filter{MaxAgeDays: 3}, false},
		{"all together", Filter{From: "epbih", Subject: "struju", Attachment: "*.pdf", MaxAgeDays: 10}, true},
	}
	for _, c := range cases {
		if got := c.f.Matches(m, now); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
	if (Filter{}).Matches(nil, now) {
		t.Error("nil message matches")
	}
}

func TestGlobMatch(t *testing.T) {
	for _, c := range []struct {
		p, n string
		want bool
	}{
		{"", "anything.pdf", true},
		{"*.pdf", "Invoice.PDF", true},
		{"*.pdf", "invoice.pdf.exe", false},
		{"invoice*", "Invoice-2026.pdf", true},
		{"[", "x", false},
		{"scan_?.jpg", "scan_1.jpg", true},
		{"scan_?.jpg", "scan_12.jpg", false},
	} {
		if got := GlobMatch(c.p, c.n); got != c.want {
			t.Errorf("GlobMatch(%q, %q) = %v", c.p, c.n, got)
		}
	}
}

func TestTooOldAndSince(t *testing.T) {
	now := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)
	if TooOld(time.Time{}, 5, now) {
		t.Error("zero date is too old")
	}
	if TooOld(now.AddDate(0, 0, -4), 5, now) {
		t.Error("4 days with max 5 is too old")
	}
	if !TooOld(now.AddDate(0, 0, -6), 5, now) {
		t.Error("6 days with max 5 is not too old")
	}
	if TooOld(now.AddDate(-1, 0, 0), 0, now) {
		t.Error("max 0 means any age")
	}
	if !SinceFor(0, now).IsZero() {
		t.Error("since for 0 days")
	}
	if SinceFor(7, now).Format("2006-01-02") != "2026-03-13" {
		t.Errorf("since for 7 days: %v", SinceFor(7, now))
	}
}

func TestDecodeTransferBase64Wrapped(t *testing.T) {
	raw := "From: a@b.c\r\nContent-Type: application/pdf; name=\"a.pdf\"\r\nContent-Transfer-Encoding: base64\r\n\r\n" +
		"SGVsbG8g\r\nd29ybGQ=\r\n"
	m, err := Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Attachments) != 1 || string(m.Attachments[0].Data) != "Hello world" {
		t.Fatalf("attachments: %+v", m.Attachments)
	}
}
