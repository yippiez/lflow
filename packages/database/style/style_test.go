package style

import "testing"

func TestValidate(t *testing.T) {
	cases := []struct {
		style string
		ok    bool
	}{
		{"", true},
		{"bold", true},
		{"bold,italic,underline,strike", true},
		{"color:blue", true},
		{"bold,color:red", true},
		{"glow", false},
		{"color:chartreuse", false},
		{"bold,color:nope", false},
	}
	for _, c := range cases {
		err := Validate(c.style)
		if (err == nil) != c.ok {
			t.Errorf("Validate(%q) ok=%v, err=%v", c.style, c.ok, err)
		}
	}
}

func TestChangeApply(t *testing.T) {
	str := func(s string) *string { return &s }
	bl := func(b bool) *bool { return &b }

	cases := []struct {
		name string
		cur  string
		ch   Change
		want string
	}{
		{"set bold from empty", "", Change{Bold: bl(true)}, "bold"},
		{"add color preserving attr", "bold", Change{Color: str("blue")}, "bold,color:blue"},
		{"clear color keeps attr", "underline,color:red", Change{Color: str("")}, "underline"},
		{"remove bold", "bold,italic", Change{Bold: bl(false)}, "italic"},
		{"style replaces wholesale", "bold,color:red", Change{Style: str("italic")}, "italic"},
		{"style then refine", "", Change{Style: str("bold"), Color: str("green")}, "bold,color:green"},
		{"normalizes order", "color:red,bold", Change{}, "bold,color:red"},
	}
	for _, c := range cases {
		got, err := c.ch.Apply(c.cur)
		if err != nil {
			t.Errorf("%s: unexpected error %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: Apply(%q) = %q, want %q", c.name, c.cur, got, c.want)
		}
	}
}

func TestChangeApplyInvalid(t *testing.T) {
	bad := "octarine"
	if _, err := (Change{Color: &bad}).Apply(""); err == nil {
		t.Error("expected error for unknown color")
	}
}

func TestChangeAny(t *testing.T) {
	if (Change{}).Any() {
		t.Error("empty change should report no edits")
	}
	on := true
	if !(Change{Bold: &on}).Any() {
		t.Error("change with bold should report an edit")
	}
}
