package search

import "testing"

func TestParse(t *testing.T) {
	p := Parse(`invoice tag:tax -tag:paid from:"IKEA Shop" type:receipt created:2024 asn:12..20 is:inbox has:notes "exact phrase" -draft field:total=99`)
	if p.Text != `invoice "exact phrase" -draft` {
		t.Errorf("text: %q", p.Text)
	}
	if len(p.Tags) != 1 || p.Tags[0] != "tax" || len(p.NotTags) != 1 || p.NotTags[0] != "paid" {
		t.Errorf("tags: %v %v", p.Tags, p.NotTags)
	}
	if len(p.Correspondents) != 1 || p.Correspondents[0] != "IKEA Shop" {
		t.Errorf("correspondents: %v", p.Correspondents)
	}
	if p.CreatedFrom != "2024-01-01" || p.CreatedTo != "2024-12-31" {
		t.Errorf("created: %s %s", p.CreatedFrom, p.CreatedTo)
	}
	if p.ASNFrom == nil || *p.ASNFrom != 12 || p.ASNTo == nil || *p.ASNTo != 20 {
		t.Errorf("asn range: %v %v", p.ASNFrom, p.ASNTo)
	}
	if !p.Inbox || p.HasNotes == nil || !*p.HasNotes {
		t.Errorf("flags: %+v", p)
	}
	if p.Fields["total"] != "99" {
		t.Errorf("fields: %v", p.Fields)
	}
	if q := Parse("meeting at 10:30 https://example.com"); q.Text != "meeting at 10:30 https://example.com" {
		t.Errorf("urls and times stay text: %q", q.Text)
	}
}

func TestDateRange(t *testing.T) {
	for _, c := range []struct{ in, from, to string }{
		{"2024", "2024-01-01", "2024-12-31"},
		{"2024-02", "2024-02-01", "2024-02-29"},
		{"2024-02-10", "2024-02-10", "2024-02-10"},
		{"10.02.2024", "2024-02-10", "2024-02-10"},
		{"2023..2024", "2023-01-01", "2024-12-31"},
		{">2024-01-31", "2024-02-01", ""},
		{"<2024-01", "", "2023-12-31"},
	} {
		f, to := DateRange(c.in)
		if f != c.from || to != c.to {
			t.Errorf("%s: got %s..%s want %s..%s", c.in, f, to, c.from, c.to)
		}
	}
}
