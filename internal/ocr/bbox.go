package ocr

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"
)

// PageWords is where the words of one page are, for a selectable text layer
// over a page image. Coordinates are in PDF points from the top-left corner,
// the same frame pdftoppm renders the page image in.
type PageWords struct {
	Width  float64  `json:"width"`
	Height float64  `json:"height"`
	Lines  [][]Word `json:"lines"`
}

// Word is one word and its box.
type Word struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
	T string  `json:"t"`
}

type bboxDoc struct {
	Pages []struct {
		Width  float64 `xml:"width,attr"`
		Height float64 `xml:"height,attr"`
		Flows  []struct {
			Blocks []struct {
				Lines []struct {
					Words []struct {
						XMin float64 `xml:"xMin,attr"`
						YMin float64 `xml:"yMin,attr"`
						XMax float64 `xml:"xMax,attr"`
						YMax float64 `xml:"yMax,attr"`
						Text string  `xml:",chardata"`
					} `xml:"word"`
				} `xml:"line"`
			} `xml:"block"`
		} `xml:"flow"`
	} `xml:"body>doc>page"`
}

// PageText reads the word boxes of page n (1-based) with pdftotext -bbox-layout.
func PageText(ctx context.Context, pdf string, n int) (*PageWords, error) {
	cmd := exec.CommandContext(ctx, "pdftotext", "-f", strconv.Itoa(n), "-l", strconv.Itoa(n), "-bbox-layout", "-enc", "UTF-8", pdf, "-")
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("pdftotext: %v %s", err, strings.TrimSpace(stderr.String()))
	}
	return parseBBox(out.Bytes())
}

func parseBBox(b []byte) (*PageWords, error) {
	var doc bboxDoc
	dec := xml.NewDecoder(bytes.NewReader(b))
	dec.Strict = false
	dec.AutoClose = xml.HTMLAutoClose
	dec.Entity = xml.HTMLEntity
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	if len(doc.Pages) == 0 {
		return nil, fmt.Errorf("no page in the output")
	}
	p := doc.Pages[0]
	out := &PageWords{Width: p.Width, Height: p.Height, Lines: [][]Word{}}
	r := func(f float64) float64 { return math.Round(f*100) / 100 }
	for _, f := range p.Flows {
		for _, bl := range f.Blocks {
			for _, ln := range bl.Lines {
				var line []Word
				for _, w := range ln.Words {
					t := strings.TrimSpace(w.Text)
					if t == "" || w.XMax <= w.XMin || w.YMax <= w.YMin {
						continue
					}
					line = append(line, Word{X: r(w.XMin), Y: r(w.YMin), W: r(w.XMax - w.XMin), H: r(w.YMax - w.YMin), T: t})
				}
				if len(line) > 0 {
					out.Lines = append(out.Lines, line)
				}
			}
		}
	}
	return out, nil
}
