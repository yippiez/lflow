package arithmetic

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestEvaluate(t *testing.T) {
	cases := []struct {
		src  string
		env  Env
		want float64
	}{
		{"42", nil, 42},
		{"0.5", nil, 0.5},
		{".5", nil, 0.5},
		{"1e3", nil, 1000},
		{"1.5e-2", nil, 0.015},
		{"2+3*4", nil, 14},
		{"(2+3)*4", nil, 20},
		{"2*3+4*5", nil, 26},
		{"10-2-3", nil, 5},
		{"10/5/2", nil, 1},
		{"10/4", nil, 2.5},
		{"10%3", nil, 1},
		{"5.5%2", nil, 1.5},
		{"2^10", nil, 1024},
		{"2^3^2", nil, 512},
		{"-2^2", nil, -4},
		{"(-2)^2", nil, 4},
		{"2^-3", nil, 0.125},
		{"2(3+4)", nil, 14},
		{"(1+1)(2+2)", nil, 8},
		{"2 3", nil, 6},
		{"2pi", nil, 2 * math.Pi},
		{"tau/2", nil, math.Pi},
		{"sqrt(16)", nil, 4},
		{"cbrt(27)", nil, 3},
		{"sin(pi/2)", nil, 1},
		{"cos(0)", nil, 1},
		{"tan(pi/4)", nil, 1},
		{"asin(1)", nil, math.Pi / 2},
		{"acos(1)", nil, 0},
		{"atan(1)", nil, math.Pi / 4},
		{"sinh(0)", nil, 0},
		{"cosh(0)", nil, 1},
		{"tanh(0)", nil, 0},
		{"ln(e)", nil, 1},
		{"log(1000)", nil, 3},
		{"exp(1)", nil, math.E},
		{"abs(-5)", nil, 5},
		{"floor(2.7)", nil, 2},
		{"ceil(2.1)", nil, 3},
		{"round(1.5)", nil, 2},
		{"round(2.5)", nil, 3},
		{"trunc(-2.7)", nil, -2},
		{"sign(-3)", nil, -1},
		{"sign(0)", nil, 0},
		{"pow(2,10)", nil, 1024},
		{"atan2(0,1)", nil, 0},
		{"hypot(3,4)", nil, 5},
		{"mod(7,3)", nil, 1},
		{"max(1,2,3)", nil, 3},
		{"min(1,2,3)", nil, 1},
		{"max(5)", nil, 5},
		{"min(max(2,7),5)", nil, 5},
		{"sqrt(2+2)*10", nil, 20},
		{"2*x + y", Env{"x": 3, "y": 4}, 10},
		{"x^2 - y^2", Env{"x": 5, "y": 3}, 16},
		{"x+y*z^2", Env{"x": 1, "y": 2, "z": 3}, 19},
		{"pi", Env{"pi": 3}, 3}, // env overrides constants
	}
	for _, c := range cases {
		got, err := Evaluate(c.src, c.env)
		if err != nil {
			t.Errorf("Evaluate(%q): unexpected error: %v", c.src, err)
			continue
		}
		if !approx(got, c.want) {
			t.Errorf("Evaluate(%q) = %v, want %v", c.src, got, c.want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{"", "unexpected end"},
		{"2+", "unexpected end"},
		{"(2+3", "expected"},
		{"2+*3", "unexpected"},
		{"2 2+", "unexpected end"},
		{"foo(1)", "unknown function"},
		{"sin(2,3)", "wrong number of arguments"},
		{"sqrt()", "wrong number of arguments"},
		{"2@3", "unexpected"},
		{"1.2.3", "invalid number"},
		{"2 3)", "unexpected"},
	}
	for _, c := range cases {
		_, err := Parse(c.src)
		if err == nil {
			t.Errorf("Parse(%q): expected error, got none", c.src)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("Parse(%q) error = %q, want containing %q", c.src, err, c.want)
		}
	}
}

func TestEvalErrors(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{"x+1", "unknown variable"},
		{"1/0", "division by zero"},
		{"1%0", "division by zero"},
		{"sqrt(-1)", "domain error"},
		{"log(-1)", "domain error"},
		{"asin(2)", "domain error"},
		// regression: fuzz-found, Inf - finite - Inf = NaN must error
		{"-((99^549)^555-(38+49^20)-827^412)", "domain error"},
	}
	for _, c := range cases {
		_, err := Evaluate(c.src, nil)
		if err == nil {
			t.Errorf("Evaluate(%q): expected error, got none", c.src)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("Evaluate(%q) error = %q, want containing %q", c.src, err, c.want)
		}
	}
}

func TestVars(t *testing.T) {
	e, err := Parse("x + y * pi + sin(z) + sin(z)")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := e.Vars(), []string{"x", "y", "z"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Vars() = %v, want %v", got, want)
	}
	e2, err := Parse("1 + 2 * pi")
	if err != nil {
		t.Fatal(err)
	}
	if len(e2.Vars()) != 0 {
		t.Errorf("Vars() = %v, want empty", e2.Vars())
	}
}

func TestCalculate(t *testing.T) {
	cases := []struct {
		expr, vars, want string
	}{
		{"sqrt(16)", "", "4"},
		{"2*x + y", `{"x":3,"y":4}`, "10"},
		{"pi", "", "3.141592653589793"},
		{"1/4", "", "0.25"},
		{"1e308 * 10", "", `"Infinity"`},
		{"max(x, y)", `{"x":-1,"y":-2}`, "-1"},
		{"1e308 * 10 - 1e308 * 10", "", "domain error"},
		{"1/0", "", "division by zero"},
		{"x", "not json", "invalid variables JSON"},
		{"x", `{"x":"nope"}`, "cannot unmarshal"},
		{"sqrt(-1)", "", "domain error"},
	}
	for _, c := range cases {
		out, err := Calculate(c.expr, c.vars)
		if err != nil {
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("Calculate(%q, %q) error = %v, want containing %q", c.expr, c.vars, err, c.want)
			}
			continue
		}
		if out != c.want {
			t.Errorf("Calculate(%q, %q) = %s, want %s", c.expr, c.vars, out, c.want)
		}
	}
}

func TestString(t *testing.T) {
	e, err := Parse(" 2 * x ")
	if err != nil {
		t.Fatal(err)
	}
	if e.String() != " 2 * x " {
		t.Errorf("String() = %q, want original source", e.String())
	}
}

func TestDepthLimit(t *testing.T) {
	src := strings.Repeat("(", 300) + "1" + strings.Repeat(")", 300)
	if _, err := Parse(src); err == nil || !strings.Contains(err.Error(), "deeply nested") {
		t.Errorf("Parse(deep) error = %v, want depth error", err)
	}
}

func TestConcurrentEval(t *testing.T) {
	e, err := Parse("x^2 + y")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan bool, 8)
	for i := 0; i < 8; i++ {
		go func(n int) {
			v, err := e.Eval(Env{"x": float64(n), "y": 1})
			if err != nil || v != float64(n*n+1) {
				t.Errorf("Eval = %v, %v", v, err)
			}
			done <- true
		}(i)
	}
	for i := 0; i < 8; i++ {
		<-done
	}
}
