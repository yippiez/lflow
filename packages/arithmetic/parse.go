// Grammar (precedence low to high):
//
//	expr    := term (('+' | '-') term)*
//	term    := unary (('*' | '/' | '%') unary)*
//	unary   := ('-' | '+') unary | power
//	power   := primary ('^' unary)?            // right-associative
//	primary := number | ident | ident '(' args ')' | '(' expr ')'
//
// Implicit multiplication ("2x", "2(3+4)", "(1)(2)") is allowed, like on a
// handheld calculator. Exponentiation binds tighter than unary minus, as in
// conventional math: -2^2 is -(2^2) = -4.
package arithmetic

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// maxDepth bounds parser recursion so hostile input cannot overflow the
// stack. 200 nested parens or calls is far beyond any real expression.
const maxDepth = 200

type tokenKind int

const (
	tokNum tokenKind = iota
	tokIdent
	tokOp
	tokLParen
	tokRParen
	tokComma
	tokBad
	tokEnd
)

type token struct {
	kind tokenKind
	text string
	pos  int // 1-based byte position in the source
}

type parser struct {
	src   string
	toks  []token
	i     int
	depth int
}

func newParser(src string) *parser {
	return &parser{src: src, toks: lex(src)}
}

func (p *parser) parse() (node, error) {
	n, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if t := p.peek(); t.kind != tokEnd {
		return nil, p.errf(t, "unexpected %q", t.text)
	}
	return n, nil
}

func (p *parser) peek() token { return p.toks[p.i] }

func (p *parser) next() token {
	t := p.toks[p.i]
	p.i++
	return t
}

func (p *parser) errf(t token, format string, args ...any) error {
	return fmt.Errorf("arithmetic: "+format+" at position %d", append(args, t.pos)...)
}

func (p *parser) enter() error {
	if p.depth++; p.depth > maxDepth {
		return errors.New("arithmetic: expression too deeply nested")
	}
	return nil
}

func (p *parser) leave() { p.depth-- }

func (p *parser) parseExpr() (node, error) {
	l, err := p.parseTerm()
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		if t.kind == tokOp && (t.text == "+" || t.text == "-") {
			p.next()
			r, err := p.parseTerm()
			if err != nil {
				return nil, err
			}
			l = binNode{op: t.text[0], l: l, r: r}
			continue
		}
		return l, nil
	}
}

func (p *parser) parseTerm() (node, error) {
	l, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		if t.kind == tokOp && (t.text == "*" || t.text == "/" || t.text == "%") {
			p.next()
			r, err := p.parseUnary()
			if err != nil {
				return nil, err
			}
			l = binNode{op: t.text[0], l: l, r: r}
			continue
		}
		if t.kind == tokNum || t.kind == tokIdent || t.kind == tokLParen {
			// Implicit multiplication: "2x", "2(3+4)", "(1)(2)".
			r, err := p.parseUnary()
			if err != nil {
				return nil, err
			}
			l = binNode{op: '*', l: l, r: r}
			continue
		}
		return l, nil
	}
}

func (p *parser) parseUnary() (node, error) {
	t := p.peek()
	if t.kind == tokOp && (t.text == "-" || t.text == "+") {
		p.next()
		x, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		if t.text == "-" {
			return negNode{x: x}, nil
		}
		return x, nil
	}
	return p.parsePower()
}

func (p *parser) parsePower() (node, error) {
	l, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	if t := p.peek(); t.kind == tokOp && t.text == "^" {
		p.next()
		r, err := p.parseUnary() // right-associative, allows "2^-3"
		if err != nil {
			return nil, err
		}
		return binNode{op: '^', l: l, r: r}, nil
	}
	return l, nil
}

func (p *parser) parsePrimary() (node, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer p.leave()

	t := p.next()
	switch t.kind {
	case tokNum:
		v, err := strconv.ParseFloat(t.text, 64)
		if err != nil {
			if errors.Is(err, strconv.ErrRange) {
				return numNode{v: v}, nil // overflow: ±Inf, underflow: ±0
			}
			return nil, p.errf(t, "invalid number %q", t.text)
		}
		return numNode{v: v}, nil
	case tokIdent:
		if p.peek().kind == tokLParen {
			return p.parseCall(t)
		}
		return varNode{name: t.text}, nil
	case tokLParen:
		n, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if t := p.peek(); t.kind != tokRParen {
			return nil, p.errf(t, "expected %q", ")")
		}
		p.next()
		return n, nil
	case tokBad:
		return nil, p.errf(t, "unexpected character %q", t.text)
	case tokEnd:
		return nil, p.errf(t, "unexpected end of expression")
	default:
		return nil, p.errf(t, "unexpected %q", t.text)
	}
}

func (p *parser) parseCall(name token) (node, error) {
	p.next() // consume '('
	f, ok := funcs[name.text]
	if !ok {
		return nil, p.errf(name, "unknown function %q", name.text)
	}
	var args []node
	if p.peek().kind != tokRParen {
		for {
			a, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			args = append(args, a)
			if p.peek().kind == tokComma {
				p.next()
				continue
			}
			break
		}
	}
	if t := p.peek(); t.kind != tokRParen {
		return nil, p.errf(t, "expected %q", ")")
	}
	p.next()
	if len(args) < f.min || len(args) > f.max {
		return nil, p.errf(name, "wrong number of arguments for %q: got %d", name.text, len(args))
	}
	return callNode{name: name.text, fn: f, args: args}, nil
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isLetter(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func lex(src string) []token {
	var toks []token
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case isDigit(c) || c == '.' && i+1 < len(src) && isDigit(src[i+1]):
			j := i
			for j < len(src) && (isDigit(src[j]) || src[j] == '.') {
				j++
			}
			if j < len(src) && (src[j] == 'e' || src[j] == 'E') {
				k := j + 1
				if k < len(src) && (src[k] == '+' || src[k] == '-') {
					k++
				}
				if k < len(src) && isDigit(src[k]) {
					for k < len(src) && isDigit(src[k]) {
						k++
					}
					j = k
				}
			}
			toks = append(toks, token{kind: tokNum, text: src[i:j], pos: i + 1})
			i = j
		case c == '_' || isLetter(c):
			j := i
			for j < len(src) && (isLetter(src[j]) || isDigit(src[j]) || src[j] == '_') {
				j++
			}
			toks = append(toks, token{kind: tokIdent, text: src[i:j], pos: i + 1})
			i = j
		case strings.ContainsRune("+-*/%^", rune(c)):
			toks = append(toks, token{kind: tokOp, text: string(c), pos: i + 1})
			i++
		case c == '(':
			toks = append(toks, token{kind: tokLParen, text: "(", pos: i + 1})
			i++
		case c == ')':
			toks = append(toks, token{kind: tokRParen, text: ")", pos: i + 1})
			i++
		case c == ',':
			toks = append(toks, token{kind: tokComma, text: ",", pos: i + 1})
			i++
		default:
			toks = append(toks, token{kind: tokBad, text: string(c), pos: i + 1})
			i++
		}
	}
	toks = append(toks, token{kind: tokEnd, pos: len(src) + 1})
	return toks
}
