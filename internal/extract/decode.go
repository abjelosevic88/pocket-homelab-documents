package extract

import (
	"encoding/base64"
	"io"
	"mime/quotedprintable"
	"strings"
)

func decodeBody(enc string, r io.Reader) io.Reader {
	switch strings.ToLower(strings.TrimSpace(enc)) {
	case "base64":
		return base64.NewDecoder(base64.StdEncoding, &spaceStripper{r})
	case "quoted-printable":
		return quotedprintable.NewReader(r)
	}
	return r
}

// spaceStripper drops the line breaks of a base64 body.
type spaceStripper struct{ r io.Reader }

func (s *spaceStripper) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	j := 0
	for i := 0; i < n; i++ {
		if p[i] == '\n' || p[i] == '\r' || p[i] == ' ' || p[i] == '\t' {
			continue
		}
		p[j] = p[i]
		j++
	}
	return j, err
}
