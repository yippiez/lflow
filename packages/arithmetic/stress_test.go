package arithmetic

import (
	"math"
	"strings"
	"sync"
	"testing"
)

func TestStressDeepNesting(t *testing.T) {
	src := strings.Repeat("(", 100_000) + "1" + strings.Repeat(")", 100_000)
	if _, err := Parse(src); err == nil {
		t.Fatal("expected depth error, got none")
	}
}

func TestStressLongChain(t *testing.T) {
	src := "1" + strings.Repeat("+1", 100_000)
	v, err := Evaluate(src, nil)
	if err != nil {
		t.Fatal(err)
	}
	if v != 100_001 {
		t.Fatalf("got %v, want 100001", v)
	}
}

func TestStressImplicitMultChain(t *testing.T) {
	src := strings.Join([]string{"2", "3", "4", "5", "6", "7", "8", "9", "10"}, " ")
	v, err := Evaluate(src, nil)
	if err != nil {
		t.Fatal(err)
	}
	if v != 3628800 {
		t.Fatalf("got %v, want 3628800", v)
	}
}

func TestStressBigLiterals(t *testing.T) {
	v, err := Evaluate(strings.Repeat("9", 100_000), nil)
	if err != nil || !math.IsInf(v, 1) {
		t.Fatalf("100k-digit literal: v=%v err=%v", v, err)
	}
	cases := []struct{ src, want string }{
		{"1e308 * 10", `"Infinity"`},
		{"(1e308)^2", `"Infinity"`},
		{"-(1e308)^2", `"-Infinity"`},
		{"(1e308)^2 - (1e308)^2", "domain error"},             // Inf - Inf = NaN
		{"1e308 * 10 % 7", "domain error"},                    // Inf % 7 = NaN
		{strings.Repeat("9", 100_000) + " / 1", `"Infinity"`}, // overflowed literal
	}
	for _, c := range cases {
		out, err := Calculate(c.src, "")
		if err != nil {
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("%q: error %v, want containing %q", c.src, err, c.want)
			}
			continue
		}
		if out != c.want {
			t.Fatalf("%q = %s, want %s", c.src, out, c.want)
		}
	}
}

func TestConcurrentStress(t *testing.T) {
	exprs := []string{
		"sqrt(x^2 + y^2)",
		"2^10 - (x+y)*z",
		"max(x,y,z) + min(x,y,z)",
		"x%3 + y/2",
		"sin(pi/2) + x",
	}
	const goroutines, iters = 64, 200
	errs := make(chan error, goroutines)
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			env := Env{"x": float64(n % 7), "y": float64(n % 5), "z": 2}
			for j := 0; j < iters; j++ {
				e, err := Parse(exprs[j%len(exprs)])
				if err != nil {
					errs <- err
					return
				}
				if _, err := e.Eval(env); err != nil {
					errs <- err
					return
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestConcurrentSharedExpr(t *testing.T) {
	e, err := Parse("x^2 + 2*x + 1")
	if err != nil {
		t.Fatal(err)
	}
	const goroutines, iters = 64, 1000
	errs := make(chan error, goroutines)
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iters; j++ {
				v, err := e.Eval(Env{"x": 3})
				if err != nil || v != 16 {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func BenchmarkParse(b *testing.B) {
	for i := 0; i < b.N; i++ {
		if _, err := Parse("sqrt(x^2 + y^2) + 2*pi"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEval(b *testing.B) {
	e, err := Parse("sqrt(x^2 + y^2) + 2*pi")
	if err != nil {
		b.Fatal(err)
	}
	env := Env{"x": 3, "y": 4}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := e.Eval(env); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCalculate(b *testing.B) {
	for i := 0; i < b.N; i++ {
		if _, err := Calculate("sqrt(x^2 + y^2) + 2*pi", `{"x":3,"y":4}`); err != nil {
			b.Fatal(err)
		}
	}
}
