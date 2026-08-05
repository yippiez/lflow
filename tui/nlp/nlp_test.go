package nlp

import (
	"context"
	"testing"
)

func TestCalculate(t *testing.T) {
	cases := []struct {
		query string
		want  string
	}{
		{"2+2", "4"},
		{"sqrt(16)", "4"},
		{"what is 2+2?", "4"},
		{"What is 2^10?", "1024"},
		{"compute sqrt(2+2)*10", "20"},
		{"calculate pi", "3.141592653589793"},
		{"the value of 10/4", "2.5"},
		{"what is the value of 3^3", "27"},
		{"evaluate max(1,2,3) + min(4,5)", "7"},
		{"1e308 * 10", `"Infinity"`},
	}
	for _, c := range cases {
		got, err := Calculate(context.Background(), c.query)
		if err != nil {
			t.Errorf("Calculate(%q): %v", c.query, err)
			continue
		}
		if got != c.want {
			t.Errorf("Calculate(%q) = %q, want %q", c.query, got, c.want)
		}
	}
}

func TestCalculateErrors(t *testing.T) {
	for _, q := range []string{
		"", "hello world", "what color is the sky", "1/0", "what's 2*x?",
	} {
		if _, err := Calculate(context.Background(), q); err == nil {
			t.Errorf("Calculate(%q): expected error, got none", q)
		}
	}
}

func TestCalculateCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Calculate(ctx, "2+2"); err == nil {
		t.Error("expected cancellation error, got none")
	}
}
