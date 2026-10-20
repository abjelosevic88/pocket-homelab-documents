package mail

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	netmail "net/mail"
	"net/textproto"
	"path"
	"regexp"
	"strings"
	"time"
)

// Address is a name and e-mail address.
type Address struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

// String is "Name <addr>" or just the address.
func (a Address) String() string {
	if a.Name != "" && a.Address != "" {
		return a.Name + " <" + a.Address + ">"
	}
	if a.Address != "" {
		return a.Address
	}
	return a.Name
}

// Attachment is one file inside a message.
type Attachment struct {
	Name string
	Mime string
	Data []byte
}

// Message is a parsed e-mail.
type Message struct {
	UID         uint32
	MessageID   string
	From        Address
	To          []Address
	Subject     string
	Date        time.Time
	Text        string // the readable body (plain text, or HTML stripped)
	Attachments []Attachment
	Raw         []byte
}

// maxAttachment caps one attachment at 100 MB.
const maxAttachment = 100 << 20

var wordDecoder = &mime.WordDecoder{}

func decodeHeader(v string) string {
	if d, err := wordDecoder.DecodeHeader(v); err == nil {
		return d
	}
	return v
}

// Parse reads an RFC 822 message: headers, the text body and attachments.
func Parse(raw []byte) (*Message, error) {
	msg, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	m := &Message{Raw: raw}
	m.Subject = strings.TrimSpace(decodeHeader(msg.Header.Get("Subject")))
	m.MessageID = strings.TrimSpace(msg.Header.Get("Message-Id"))
	if m.MessageID == "" {
		m.MessageID = strings.TrimSpace(msg.Header.Get("Message-ID"))
	}
	if t, err := msg.Header.Date(); err == nil {
		m.Date = t
	} else if v := msg.Header.Get("Date"); v != "" {
		for _, layout := range []string{time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822, "Mon, 2 Jan 2006 15:04:05 -0700 (MST)", "2 Jan 2006 15:04:05 -0700"} {
			if t, err := time.Parse(layout, strings.TrimSpace(v)); err == nil {
				m.Date = t
				break
			}
		}
	}
	m.From = parseAddresses(msg.Header.Get("From"), 1)
	if m.From.Address == "" && m.From.Name == "" {
		m.From = parseAddresses(msg.Header.Get("Sender"), 1)
	}
	m.To = parseAddressList(msg.Header.Get("To"))
	m.To = append(m.To, parseAddressList(msg.Header.Get("Cc"))...)

	var plain, html []string
	walkPart(textproto.MIMEHeader(msg.Header), msg.Body, 0, func(ctype string, params map[string]string, name string, disposition string, data []byte) {
		switch {
		case name == "" && strings.HasPrefix(ctype, "text/plain") && disposition != "attachment":
			plain = append(plain, string(decodeCharset(data, params["charset"])))
		case name == "" && strings.HasPrefix(ctype, "text/html") && disposition != "attachment":
			html = append(html, string(decodeCharset(data, params["charset"])))
		case name != "":
			if len(data) > maxAttachment {
				return
			}
			m.Attachments = append(m.Attachments, Attachment{Name: name, Mime: ctype, Data: data})
		}
	})
	if len(plain) > 0 {
		m.Text = strings.TrimSpace(strings.Join(plain, "\n\n"))
	} else if len(html) > 0 {
		m.Text = strings.TrimSpace(stripHTML(strings.Join(html, "\n")))
	}
	return m, nil
}

func parseAddresses(v string, _ int) Address {
	l := parseAddressList(v)
	if len(l) == 0 {
		return Address{}
	}
	return l[0]
}

func parseAddressList(v string) []Address {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	list, err := (&netmail.AddressParser{WordDecoder: wordDecoder}).ParseList(v)
	if err != nil {
		// a bare, slightly broken header: keep what we can
		v = decodeHeader(v)
		if i := strings.Index(v, "<"); i >= 0 {
			if j := strings.Index(v[i:], ">"); j > 0 {
				return []Address{{Name: strings.Trim(strings.TrimSpace(v[:i]), `"`), Address: v[i+1 : i+j]}}
			}
		}
		return []Address{{Address: v}}
	}
	out := make([]Address, 0, len(list))
	for _, a := range list {
		out = append(out, Address{Name: a.Name, Address: a.Address})
	}
	return out
}

// walkPart descends a MIME tree, calling fn with each leaf's decoded bytes.
func walkPart(h textproto.MIMEHeader, body io.Reader, depth int, fn func(ctype string, params map[string]string, name, disposition string, data []byte)) {
	if depth > 10 {
		return
	}
	ctype, params, err := mime.ParseMediaType(h.Get("Content-Type"))
	if err != nil {
		ctype = "text/plain"
		params = map[string]string{}
	}
	ctype = strings.ToLower(ctype)
	if strings.HasPrefix(ctype, "multipart/") && params["boundary"] != "" {
		mr := multipart.NewReader(body, params["boundary"])
		for {
			p, err := mr.NextPart()
			if err != nil {
				return
			}
			walkPart(p.Header, p, depth+1, fn)
		}
	}
	if ctype == "message/rfc822" {
		// an attached mail: treat it as one .eml attachment
		data, err := io.ReadAll(io.LimitReader(body, maxAttachment+1))
		if err != nil {
			return
		}
		name := partName(h, params)
		if name == "" {
			name = "message.eml"
		}
		fn(ctype, params, name, "attachment", data)
		return
	}
	data, err := io.ReadAll(io.LimitReader(decodeTransfer(body, h.Get("Content-Transfer-Encoding")), maxAttachment+1))
	if err != nil && len(data) == 0 {
		return
	}
	disp, _, _ := mime.ParseMediaType(h.Get("Content-Disposition"))
	disp = strings.ToLower(disp)
	name := partName(h, params)
	if name == "" && disp == "attachment" {
		name = "attachment" + extFor(ctype)
	}
	fn(ctype, params, name, disp, data)
}

// partName is the file name of a part: Content-Disposition filename, else
// Content-Type name, decoded.
func partName(h textproto.MIMEHeader, ctParams map[string]string) string {
	if _, dp, err := mime.ParseMediaType(h.Get("Content-Disposition")); err == nil {
		if n := dp["filename"]; n != "" {
			return cleanName(decodeHeader(n))
		}
	}
	if n := ctParams["name"]; n != "" {
		return cleanName(decodeHeader(n))
	}
	return ""
}

func cleanName(n string) string {
	n = strings.TrimSpace(n)
	n = path.Base(strings.ReplaceAll(n, "\\", "/"))
	if n == "." || n == "/" {
		return ""
	}
	return n
}

func extFor(ctype string) string {
	switch ctype {
	case "application/pdf":
		return ".pdf"
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "text/plain":
		return ".txt"
	}
	if exts, _ := mime.ExtensionsByType(ctype); len(exts) > 0 {
		return exts[0]
	}
	return ""
}

func decodeTransfer(r io.Reader, enc string) io.Reader {
	switch strings.ToLower(strings.TrimSpace(enc)) {
	case "base64":
		return base64.NewDecoder(base64.StdEncoding, &b64Cleaner{r: r})
	case "quoted-printable":
		return quotedprintable.NewReader(r)
	}
	return r
}

// b64Cleaner drops whitespace so line-wrapped base64 decodes.
type b64Cleaner struct {
	r io.Reader
}

func (c *b64Cleaner) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	j := 0
	for i := 0; i < n; i++ {
		switch p[i] {
		case '\r', '\n', ' ', '\t':
			continue
		}
		p[j] = p[i]
		j++
	}
	return j, err
}

// decodeCharset turns common single-byte charsets into UTF-8; others pass through.
func decodeCharset(b []byte, charset string) []byte {
	switch strings.ToLower(charset) {
	case "", "utf-8", "utf8", "us-ascii", "ascii":
		return bytes.ToValidUTF8(b, []byte(""))
	case "iso-8859-1", "latin1", "windows-1252", "cp1252":
		var sb strings.Builder
		sb.Grow(len(b))
		for _, c := range b {
			if c < 0x80 || c >= 0xa0 || strings.ToLower(charset) == "iso-8859-1" {
				sb.WriteRune(rune(c))
			} else if r, ok := cp1252[c]; ok {
				sb.WriteRune(r)
			}
		}
		return []byte(sb.String())
	}
	return bytes.ToValidUTF8(b, []byte(""))
}

var cp1252 = map[byte]rune{0x80: '€', 0x82: '‚', 0x84: '„', 0x85: '…', 0x91: '‘', 0x92: '’', 0x93: '“', 0x94: '”', 0x95: '•', 0x96: '–', 0x97: '—', 0x99: '™', 0x8a: 'Š', 0x9a: 'š', 0x8e: 'Ž', 0x9e: 'ž', 0x8c: 'Œ', 0x9c: 'œ', 0x9f: 'Ÿ'}

var (
	scriptRe = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(script|style)>`)
	tagRe    = regexp.MustCompile(`<[^>]+>`)
	spaceRe  = regexp.MustCompile(`[ \t]{2,}`)
	linesRe  = regexp.MustCompile(`\n{3,}`)
)

func stripHTML(s string) string {
	s = scriptRe.ReplaceAllString(s, "")
	s = regexp.MustCompile(`(?i)<br\s*/?>|</p>|</div>|</tr>|</li>|</h[1-6]>`).ReplaceAllString(s, "\n")
	s = tagRe.ReplaceAllString(s, "")
	s = strings.NewReplacer("&nbsp;", " ", "&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'").Replace(s)
	s = spaceRe.ReplaceAllString(s, " ")
	s = linesRe.ReplaceAllString(s, "\n\n")
	return s
}
