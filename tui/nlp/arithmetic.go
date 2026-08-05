// This file is the expression calculator that powers Calculate in nlp.go:
// a tiny, dependency-free arithmetic engine. It parses and evaluates
// expressions like "sqrt(x^2 + y^2) + 2*pi" with no external dependencies.
//
// An expression is compiled once with parse and evaluated many times with
// expr.eval against a variable environment. Compile-time errors (bad syntax,
// unknown functions) surface at parse; run-time errors (unknown variables,
// division by zero) surface at eval. A compiled expr is immutable and safe
// for concurrent use.
//
// calculate is the one-call entry point for string-in/string-out use.
package nlp

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
)

// env maps variable names to values.
type env map[string]float64

// expr is a compiled expression.
type expr struct {
	src  string
	root node
	vars []string
}

// String returns the original source text.
func (e *expr) String() string { return e.src }

// Vars lists the variable names the expression depends on (constants such as
// pi are excluded), sorted. Useful to discover what inputs a calculator node
// needs to provide.
func (e *expr) Vars() []string { return e.vars }

// eval evaluates the expression with the given environment.
func (e *expr) eval(env env) (float64, error) { return e.root.eval(env) }

// parse compiles src into an expr. Unknown variables are allowed at this
// stage (they may be supplied at eval time); unknown functions are not.
func parse(src string) (*expr, error) {
	root, err := newParser(src).parse()
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	collect(root, names)
	vars := make([]string, 0, len(names))
	for name := range names {
		if _, ok := consts[name]; !ok {
			vars = append(vars, name)
		}
	}
	sort.Strings(vars)
	return &expr{src: src, root: root, vars: vars}, nil
}

// evaluate parses and evaluates src in one step.
func evaluate(src string, env env) (float64, error) {
	e, err := parse(src)
	if err != nil {
		return 0, err
	}
	return e.eval(env)
}

// calculate is the string-level entry point: expression and variables in,
// result out. varsJSON is a JSON object mapping variable names to numbers
// ("{\"x\":3,\"y\":4}"); an empty string means no variables. The result is
// returned as a JSON number; overflow produces the strings "Infinity" or
// "-Infinity".
func calculate(expr string, varsJSON string) (string, error) {
	env, err := envFromJSON(varsJSON)
	if err != nil {
		return "", err
	}
	v, err := evaluate(expr, env)
	if err != nil {
		return "", err
	}
	return marshalResult(v)
}

func envFromJSON(s string) (env, error) {
	if s == "" {
		return nil, nil
	}
	var m map[string]float64
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, fmt.Errorf("nlp: invalid variables JSON: %w", err)
	}
	return env(m), nil
}

func marshalResult(v float64) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		// JSON cannot represent infinities; return a string instead.
		label := "NaN"
		if math.IsInf(v, 1) {
			label = "Infinity"
		} else if math.IsInf(v, -1) {
			label = "-Infinity"
		}
		b, err = json.Marshal(label)
		if err != nil {
			return "", err
		}
	}
	return string(b), nil
}

// consts are built-in constants. An explicit env entry takes precedence.
var consts = map[string]float64{
	"pi":  math.Pi,
	"e":   math.E,
	"tau": 2 * math.Pi,
}

// fn describes a built-in function and its argument count bounds.
type fn struct {
	min, max int // argument count bounds
	f        func(args []float64) (float64, error)
}

func one(f func(float64) float64) fn {
	return fn{min: 1, max: 1, f: func(a []float64) (float64, error) { return f(a[0]), nil }}
}

func two(f func(float64, float64) float64) fn {
	return fn{min: 2, max: 2, f: func(a []float64) (float64, error) { return f(a[0], a[1]), nil }}
}

func variadic(f func(args []float64) float64) fn {
	return fn{min: 1, max: math.MaxInt, f: func(a []float64) (float64, error) { return f(a), nil }}
}

// funcs is the built-in function table. Angles are in radians.
var funcs = map[string]fn{
	"sqrt":  one(math.Sqrt),
	"cbrt":  one(math.Cbrt),
	"sin":   one(math.Sin),
	"cos":   one(math.Cos),
	"tan":   one(math.Tan),
	"asin":  one(math.Asin),
	"acos":  one(math.Acos),
	"atan":  one(math.Atan),
	"sinh":  one(math.Sinh),
	"cosh":  one(math.Cosh),
	"tanh":  one(math.Tanh),
	"exp":   one(math.Exp),
	"ln":    one(math.Log),
	"log":   one(math.Log10),
	"abs":   one(math.Abs),
	"floor": one(math.Floor),
	"ceil":  one(math.Ceil),
	"round": one(math.Round),
	"trunc": one(math.Trunc),
	"sign": one(func(x float64) float64 {
		if x < 0 {
			return -1
		}
		if x > 0 {
			return 1
		}
		return 0
	}),
	"pow":   two(math.Pow),
	"atan2": two(math.Atan2),
	"hypot": two(math.Hypot),
	"mod":   two(math.Mod),
	"max":   variadic(func(a []float64) float64 { return a[fold(a, math.Max)] }),
	"min":   variadic(func(a []float64) float64 { return a[fold(a, math.Min)] }),
}

func fold(a []float64, f func(float64, float64) float64) int {
	m := 0
	for i := 1; i < len(a); i++ {
		if f(a[i], a[m]) == a[i] {
			m = i
		}
	}
	return m
}

// checkNaN rejects results that are not a number (domain errors like
// sqrt(-1) or log(-1)) instead of letting NaN propagate silently.
func checkNaN(v float64) (float64, error) {
	if math.IsNaN(v) {
		return 0, errors.New("result is not a number (domain error)")
	}
	return v, nil
}

// node is an AST node.
type node interface {
	eval(env env) (float64, error)
}

type numNode struct{ v float64 }

func (n numNode) eval(env) (float64, error) { return n.v, nil }

type varNode struct{ name string }

func (n varNode) eval(env env) (float64, error) {
	if v, ok := env[n.name]; ok {
		return v, nil
	}
	if v, ok := consts[n.name]; ok {
		return v, nil
	}
	return 0, fmt.Errorf("unknown variable %q", n.name)
}

type negNode struct{ x node }

func (n negNode) eval(env env) (float64, error) {
	v, err := n.x.eval(env)
	if err != nil {
		return 0, err
	}
	return -v, nil
}

type binNode struct {
	op   byte
	l, r node
}

func (n binNode) eval(env env) (float64, error) {
	l, err := n.l.eval(env)
	if err != nil {
		return 0, err
	}
	r, err := n.r.eval(env)
	if err != nil {
		return 0, err
	}
	switch n.op {
	case '+':
		return checkNaN(l + r)
	case '-':
		return checkNaN(l - r)
	case '*':
		return checkNaN(l * r)
	case '/':
		if r == 0 {
			return 0, errors.New("division by zero")
		}
		return checkNaN(l / r)
	case '%':
		if r == 0 {
			return 0, errors.New("division by zero")
		}
		return checkNaN(math.Mod(l, r))
	case '^':
		return checkNaN(math.Pow(l, r))
	}
	return 0, fmt.Errorf("internal error: unknown operator %q", n.op)
}

type callNode struct {
	name string
	fn   fn
	args []node
}

func (n callNode) eval(env env) (float64, error) {
	vals := make([]float64, len(n.args))
	for i, a := range n.args {
		v, err := a.eval(env)
		if err != nil {
			return 0, err
		}
		vals[i] = v
	}
	v, err := n.fn.f(vals)
	if err != nil {
		return 0, err
	}
	return checkNaN(v)
}

func collect(n node, names map[string]bool) {
	switch t := n.(type) {
	case varNode:
		names[t.name] = true
	case negNode:
		collect(t.x, names)
	case binNode:
		collect(t.l, names)
		collect(t.r, names)
	case callNode:
		for _, a := range t.args {
			collect(a, names)
		}
	}
}
