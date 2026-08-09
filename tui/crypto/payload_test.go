package crypto

import (
	"strings"
	"testing"
)

func TestContentRoundTripAndWalk(t *testing.T) {
	c := Content{
		Title: "bank",
		Note:  "the joint account",
		Children: []Node{
			{Name: "iban", Type: "bullets", Children: []Node{{Name: "TR00 0000", Style: "bold"}}},
			{Name: "pin", Starred: true},
		},
	}
	b, err := MarshalContent(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := UnmarshalContent(b)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Title != "bank" || got.Note != "the joint account" || len(got.Children) != 2 {
		t.Fatalf("round trip lost content: %+v", got)
	}
	if got.CountNodes() != 3 {
		t.Errorf("CountNodes = %d, want 3", got.CountNodes())
	}

	var paths []string
	got.Walk(func(n Node, p Path) { paths = append(paths, strings.Join(append(p, n.Name), "/")) })
	want := []string{"bank/iban", "bank/iban/TR00 0000", "bank/pin"}
	if len(paths) != len(want) {
		t.Fatalf("walk visited %v, want %v", paths, want)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Errorf("walk[%d] = %q, want %q", i, paths[i], want[i])
		}
	}
}

func TestParseTokenResponse(t *testing.T) {
	// ykchalresp prints the bare hex digest; ykman may precede it with chatter.
	for _, in := range []string{
		"9f7c1b0aa5e2d3c4b5a69788796a5b4c3d2e1f00\n",
		"Touch your YubiKey...\n9f7c1b0aa5e2d3c4b5a69788796a5b4c3d2e1f00\n",
	} {
		got, err := parseTokenResponse(in)
		if err != nil {
			t.Fatalf("parse %q: %v", in, err)
		}
		if len(got) != 20 {
			t.Errorf("parse %q: %d bytes, want 20", in, len(got))
		}
	}
	if _, err := parseTokenResponse("no key present\n"); err == nil {
		t.Error("parsed a response out of an error message")
	}
}

func TestTokenFactorLabelsItsDevice(t *testing.T) {
	f := Factor{Kind: KindToken, Device: "ykman"}
	if got := f.Label(); got != "token · ykman" {
		t.Errorf("label = %q", got)
	}
	if got := (Factor{Kind: KindKeyfile}).Label(); got != "keyfile" {
		t.Errorf("label = %q", got)
	}
}

func TestMissingReportsUnansweredFactors(t *testing.T) {
	fs := []Factor{{Kind: KindPassword}, {Kind: KindKeyfile}, {Kind: KindToken}}
	got := Secrets{Password: []byte("pw")}.Missing(fs)
	if len(got) != 1 || got[0].Kind != KindKeyfile {
		t.Fatalf("Missing = %+v, want just the keyfile", got)
	}
	// the token is deliberately absent: only the device can answer it, so a
	// prompt has nothing to collect and Missing must not demand a field for it.
}
