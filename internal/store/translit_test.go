package store

import "testing"

func TestTranslit(t *testing.T) {
	if got := CyrToLat("Рачун бр. 12 Љубљана џеп"); got != "Račun br. 12 Ljubljana džep" {
		t.Errorf("cyr→lat: %q", got)
	}
	if got := LatToCyr("Račun Ljubljana"); got != "рачун љубљана" {
		t.Errorf("lat→cyr: %q", got)
	}
	q := FTSQuery("račun")
	if q != `("račun"*) OR ("рачун"*)` {
		t.Errorf("query in both alphabets: %q", q)
	}
	if q := FTSQuery("invoice xyz"); q != `"invoice"* "xyz"*` {
		t.Errorf("non-Serbian query untouched: %q", q)
	}
}
