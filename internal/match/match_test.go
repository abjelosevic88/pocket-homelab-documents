package match

import (
	"testing"

	"github.com/abjelosevic88/pocket-homelab-documents/internal/store"
)

func TestMatches(t *testing.T) {
	text := "Rechnung Nr. 2024-17 von der Stadtwerke München GmbH, Betrag 120,00 EUR"
	cases := []struct {
		m    store.Matchable
		want bool
	}{
		{store.Matchable{Match: "stadtwerke rechnung", MatchingAlgorithm: store.MatchAny, IsInsensitive: true}, true},
		{store.Matchable{Match: "stadtwerke telekom", MatchingAlgorithm: store.MatchAll, IsInsensitive: true}, false},
		{store.Matchable{Match: "Stadtwerke München", MatchingAlgorithm: store.MatchLiteral}, true},
		{store.Matchable{Match: "stadtwerke münchen", MatchingAlgorithm: store.MatchLiteral}, false},
		{store.Matchable{Match: `\d{4}-\d{2}`, MatchingAlgorithm: store.MatchRegex}, true},
		{store.Matchable{Match: "Stadtwerke Munchen", MatchingAlgorithm: store.MatchFuzzy, IsInsensitive: true}, true},
		{store.Matchable{Match: "Wasserwerke Hamburg", MatchingAlgorithm: store.MatchFuzzy, IsInsensitive: true}, false},
		{store.Matchable{Match: "werke", MatchingAlgorithm: store.MatchAny, IsInsensitive: true}, false}, // word boundaries
		{store.Matchable{Match: "x", MatchingAlgorithm: store.MatchAuto}, false},
	}
	for i, c := range cases {
		if got := Matches(c.m, text); got != c.want {
			t.Errorf("case %d (%q alg %d): got %v want %v", i, c.m.Match, c.m.MatchingAlgorithm, got, c.want)
		}
	}
}

func TestClassifier(t *testing.T) {
	var docs []store.TrainingDoc
	for i := 0; i < 4; i++ {
		docs = append(docs, store.TrainingDoc{ID: int64(i), Text: "Rechnung Stromverbrauch Zähler kWh Abschlag Energie Stadtwerke", CorrespondentID: 1, DocumentTypeID: 10, Tags: []int64{100}})
		docs = append(docs, store.TrainingDoc{ID: int64(10 + i), Text: "Mietvertrag Wohnung Kaution Vermieter Nebenkosten Quadratmeter", CorrespondentID: 2, DocumentTypeID: 11, Tags: []int64{101}})
	}
	c := Train(docs)
	s := c.Suggest("Ihre Rechnung über Stromverbrauch 2400 kWh, Abschlag Energie", 0.6)
	if s.Correspondent == nil || s.Correspondent.ID != 1 {
		t.Fatalf("correspondent: %+v", s.Correspondent)
	}
	if s.DocumentType == nil || s.DocumentType.ID != 10 {
		t.Fatalf("type: %+v", s.DocumentType)
	}
	if len(s.Tags) != 1 || s.Tags[0].ID != 100 {
		t.Fatalf("tags: %+v", s.Tags)
	}
	if s2 := c.Suggest("Vermieter Kaution Wohnung Nebenkosten", 0.6); s2.Correspondent == nil || s2.Correspondent.ID != 2 {
		t.Fatalf("second class: %+v", s2.Correspondent)
	}
	if empty := Train(nil).Suggest("anything", 0.6); empty.Correspondent != nil || len(empty.Tags) != 0 {
		t.Fatal("empty model suggests things")
	}
}
