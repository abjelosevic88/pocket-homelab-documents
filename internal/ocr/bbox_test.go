package ocr

import "testing"

func TestParseBBox(t *testing.T) {
	in := `<!DOCTYPE html><html xmlns="http://www.w3.org/1999/xhtml"><head><title></title><meta name="Producer" content="x"/></head><body><doc>
<page width="595.276000" height="841.890000"><flow><block xMin="1" yMin="1" xMax="2" yMax="2">
<line xMin="50" yMin="60" xMax="200" yMax="72"><word xMin="50.000000" yMin="60.000000" xMax="98.500000" yMax="72.000000">Račun</word><word xMin="102" yMin="60" xMax="130" yMax="72">br.</word></line>
<line xMin="50" yMin="80" xMax="90" yMax="92"><word xMin="50" yMin="80" xMax="90" yMax="92">&amp;12</word></line>
</block></flow></page></doc></body></html>`
	p, err := parseBBox([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if p.Width != 595.276 || len(p.Lines) != 2 || len(p.Lines[0]) != 2 || p.Lines[0][0].T != "Račun" || p.Lines[0][0].W != 48.5 || p.Lines[1][0].T != "&12" {
		t.Fatalf("%+v", p)
	}
}
