package zotero

import "testing"

func TestLabel(t *testing.T) {
	cases := []struct {
		name string
		item Item
		want string
	}{
		{"one author", Item{Creators: []string{"Smith"}, Year: "2020"}, "Smith 2020"},
		{"two authors", Item{Creators: []string{"Smith", "Doe"}, Year: "2020"}, "Smith & Doe 2020"},
		{"three authors", Item{Creators: []string{"Smith", "Doe", "Roe"}, Year: "2020"}, "Smith et al. 2020"},
		{"no year", Item{Creators: []string{"Smith"}}, "Smith"},
		{"no creator", Item{Title: "Attention is all you need really", Year: "2017"}, "Attention is all you 2017"},
		{"nothing but a key", Item{Key: "ABCD1234"}, "ABCD1234"},
	}
	for _, c := range cases {
		if got := c.item.Label(); got != c.want {
			t.Errorf("%s: Label() = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestCitation(t *testing.T) {
	it := Item{
		Creators: []string{"Smith", "Doe"},
		Year:     "2020",
		Title:    "On outlines",
		Journal:  "Journal of Notes",
		DOI:      "10.1000/xyz",
	}
	want := "Smith, Doe (2020). On outlines. Journal of Notes. https://doi.org/10.1000/xyz"
	if got := it.Citation(); got != want {
		t.Errorf("Citation() = %q, want %q", got, want)
	}
	// with no DOI the stored url stands in
	it.DOI, it.URL = "", "https://example.com/paper"
	if got := it.Citation(); got != "Smith, Doe (2020). On outlines. Journal of Notes. https://example.com/paper" {
		t.Errorf("Citation() url fallback = %q", got)
	}
	// an entry with nothing at all still says something
	if got := (Item{Key: "K1"}).Citation(); got != "K1" {
		t.Errorf("empty Citation() = %q, want the key", got)
	}
}

func TestRefURIs(t *testing.T) {
	personal := Ref{Key: "ABCD1234"}
	if got := personal.LocalURI(); got != "zotero://select/library/items/ABCD1234" {
		t.Errorf("personal LocalURI = %q", got)
	}
	if got, ok := personal.WebURL("erin"); !ok || got != "https://www.zotero.org/erin/items/ABCD1234/library" {
		t.Errorf("personal WebURL = %q, %v", got, ok)
	}
	if _, ok := personal.WebURL(""); ok {
		t.Error("personal WebURL with no username should decline")
	}

	group := Ref{Key: "ABCD1234", GroupID: "5566"}
	if got := group.LocalURI(); got != "zotero://select/groups/5566/items/ABCD1234" {
		t.Errorf("group LocalURI = %q", got)
	}
	// a group library is public-addressable with no account name at all
	if got, ok := group.WebURL(""); !ok || got != "https://www.zotero.org/groups/5566/items/ABCD1234/library" {
		t.Errorf("group WebURL = %q, %v", got, ok)
	}
}

func TestParseRefRoundTrip(t *testing.T) {
	for _, r := range []Ref{{Key: "ABCD1234"}, {Key: "ABCD1234", GroupID: "5566"}} {
		got, ok := ParseRef(r.LocalURI())
		if !ok || got != r {
			t.Errorf("ParseRef(%q) = %+v, %v; want %+v", r.LocalURI(), got, ok, r)
		}
	}
	for _, bad := range []string{
		"", "https://example.com", "zotero://open-pdf/library/items/ABCD1234",
		"lflow://node/abc", "zotero://select/library/items/",
	} {
		if _, ok := ParseRef(bad); ok {
			t.Errorf("ParseRef(%q) accepted a non-reference", bad)
		}
	}
}

func TestDOIURL(t *testing.T) {
	if got := DOIURL("10.1000/xyz"); got != "https://doi.org/10.1000/xyz" {
		t.Errorf("DOIURL bare = %q", got)
	}
	if got := DOIURL("doi:10.1000/xyz"); got != "https://doi.org/10.1000/xyz" {
		t.Errorf("DOIURL prefixed = %q", got)
	}
	if got := DOIURL("https://doi.org/10.1000/xyz"); got != "https://doi.org/10.1000/xyz" {
		t.Errorf("DOIURL resolved = %q", got)
	}
	if got := DOIURL("  "); got != "" {
		t.Errorf("DOIURL empty = %q", got)
	}
}

func TestYearOf(t *testing.T) {
	cases := map[string]string{
		"2020-05-13 2020-05-13": "2020",
		"0000-00-00 1998":       "1998",
		"":                      "",
		"n.d.":                  "",
		"in press":              "",
	}
	for in, want := range cases {
		if got := yearOf(in); got != want {
			t.Errorf("yearOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSearch(t *testing.T) {
	lib := &Library{Items: []Item{
		{Key: "A", Creators: []string{"Vaswani"}, Year: "2017", Title: "Attention is all you need", Journal: "NeurIPS"},
		{Key: "B", Creators: []string{"Smith"}, Year: "2020", Title: "Attention in outlines"},
		{Key: "C", Creators: []string{"Doe"}, Year: "1998", Title: "Something else entirely"},
	}}

	// every term must match — this is how you find a half-remembered paper
	got := lib.Search("attention 2017", 0)
	if len(got) != 1 || got[0].Key != "A" {
		t.Fatalf("Search(attention 2017) = %v", keys(got))
	}
	// a term matching two entries keeps both
	if got := lib.Search("attention", 0); len(got) != 2 {
		t.Fatalf("Search(attention) = %v, want 2 hits", keys(got))
	}
	// an entry whose author leads with the term outranks a mere substring hit
	if got := lib.Search("smith", 0); len(got) != 1 || got[0].Key != "B" {
		t.Fatalf("Search(smith) = %v", keys(got))
	}
	// an empty query is the whole library
	if got := lib.Search("", 0); len(got) != 3 {
		t.Fatalf("Search(empty) = %v, want everything", keys(got))
	}
	// the limit caps the result set
	if got := lib.Search("", 2); len(got) != 2 {
		t.Fatalf("Search limit = %v, want 2", keys(got))
	}
	// nothing matches: no hits, no panic
	if got := lib.Search("zzz", 0); len(got) != 0 {
		t.Fatalf("Search(zzz) = %v", keys(got))
	}
}

func TestSearchRanksLeadingMatchFirst(t *testing.T) {
	lib := &Library{Items: []Item{
		{Key: "mention", Creators: []string{"Doe"}, Title: "A note on smithing"},
		{Key: "author", Creators: []string{"Smith"}, Title: "Unrelated"},
	}}
	got := lib.Search("smith", 0)
	if len(got) != 2 || got[0].Key != "author" {
		t.Errorf("Search ranking = %v, want the author match first", keys(got))
	}
}

func TestByKey(t *testing.T) {
	lib := &Library{Items: []Item{{Key: "A", Title: "First"}}}
	if it, ok := lib.ByKey("A"); !ok || it.Title != "First" {
		t.Errorf("ByKey(A) = %+v, %v", it, ok)
	}
	if _, ok := lib.ByKey("nope"); ok {
		t.Error("ByKey found a key that is not there")
	}
	var nilLib *Library
	if _, ok := nilLib.ByKey("A"); ok {
		t.Error("ByKey on a nil library should decline")
	}
	if got := nilLib.Search("x", 0); got != nil {
		t.Error("Search on a nil library should return nothing")
	}
}

func TestFormat(t *testing.T) {
	label, detail := Format(Item{Creators: []string{"Smith"}, Year: "2020", Title: "On outlines", Journal: "JN"})
	if label != "Smith 2020" || detail != "On outlines · JN" {
		t.Errorf("Format = %q / %q", label, detail)
	}
	if _, detail := Format(Item{Creators: []string{"Smith"}, Title: "Solo"}); detail != "Solo" {
		t.Errorf("Format with no journal = %q", detail)
	}
}

func keys(items []Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Key
	}
	return out
}
