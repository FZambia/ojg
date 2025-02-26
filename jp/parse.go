// Copyright (c) 2020, Peter Ohler, All rights reserved.

package jp

import (
	"fmt"
	"math"
	"strconv"

	"github.com/ohler55/ojg"
)

const (
	//   0123456789abcdef0123456789abcdef
	tokenMap = "" +
		"................................" + // 0x00
		"...o.o..........oooooooooooo...o" + // 0x20
		".oooooooooooooooooooooooooo...oo" + // 0x40
		".oooooooooooooooooooooooooooooo." + // 0x60
		"oooooooooooooooooooooooooooooooo" + // 0x80
		"oooooooooooooooooooooooooooooooo" + // 0xa0
		"oooooooooooooooooooooooooooooooo" + // 0xc0
		"oooooooooooooooooooooooooooooooo" //   0xe0

	// o for an operatio
	// v for a value start character
	//   0123456789abcdef0123456789abcdef
	eqMap = "" +
		"................................" + // 0x00
		".ov.v.ovv.oo.o.ovvvvvvvvvv..ooo." + // 0x20
		"v..............................." + // 0x40
		"......v.......v.....v.......o.o." + // 0x60
		"................................" + // 0x80
		"................................" + // 0xa0
		"................................" + // 0xc0
		"................................" //   0xe0
)

// Performance is less a concern with Expr parsing as it is usually done just
// once if performance is important. Alternatively, an Expr can be built using
// function calls or bare structs. Parsing is more for convenience. Using this
// approach over modes only adds 10% so a reasonable penalty for
// maintainability.
type parser struct {
	buf []byte
	pos int
}

// ParseString parses a string into an Expr.
func ParseString(s string) (x Expr, err error) {
	return Parse([]byte(s))
}

// MustParseString parses a string into an Expr and panics on error.
func MustParseString(s string) (x Expr) {
	return MustParse([]byte(s))
}

// Parse parses a []byte into an Expr.
func Parse(buf []byte) (x Expr, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = ojg.NewError(r)
		}
	}()
	x = MustParse(buf)

	return
}

// MustParse parses a []byte into an Expr and panics on error.
func MustParse(buf []byte) (x Expr) {
	p := &parser{buf: buf}
	x = p.readExpr()
	if p.pos < len(buf) {
		p.raise("parse error")
	}
	return
}

func (p *parser) readExpr() (x Expr) {
	x = Expr{}
	var f Frag
	first := true
	lastDescent := false
	for {
		if f = p.nextFrag(first, lastDescent); f == nil {
			return
		}
		first = false
		if _, ok := f.(Descent); ok {
			lastDescent = true
		} else {
			lastDescent = false
		}
		x = append(x, f)
	}
}

func (p *parser) nextFrag(first, lastDescent bool) (f Frag) {
	if p.pos < len(p.buf) {
		b := p.buf[p.pos]
		p.pos++
		switch b {
		case '$':
			if first {
				f = Root('$')
			}
		case '@':
			if first {
				f = At('@')
			}
		case '.':
			f = p.afterDot()
		case '*':
			return Wildcard('*')
		case '[':
			f = p.afterBracket()
		case ']':
			// done
		default:
			p.pos--
			if tokenMap[b] == 'o' {
				if first {
					f = p.afterDot()
				} else if lastDescent {
					f = p.afterDotDot()
				}
			}
		}
		// Any other character is the end of the Expr, figure out later if
		// that is an error.
	}
	return
}

func (p *parser) afterDot() Frag {
	if len(p.buf) <= p.pos {
		p.raise("not terminated")
	}
	var token []byte
	b := p.buf[p.pos]
	p.pos++
	switch b {
	case '*':
		return Wildcard('*')
	case '.':
		return Descent('.')
	default:
		if tokenMap[b] == '.' {
			p.raise("an expression fragment can not start with a '%c'", b)
		}
		token = append(token, b)
	}
	for p.pos < len(p.buf) {
		b := p.buf[p.pos]
		p.pos++
		if tokenMap[b] == '.' {
			p.pos--
			break
		}
		token = append(token, b)
	}
	return Child(token)
}

func (p *parser) afterDotDot() Frag {
	var token []byte
	b := p.buf[p.pos]
	p.pos++
	token = append(token, b)
	for p.pos < len(p.buf) {
		b := p.buf[p.pos]
		p.pos++
		if tokenMap[b] == '.' {
			p.pos--
			break
		}
		token = append(token, b)
	}
	return Child(token)
}

func (p *parser) afterBracket() Frag {
	if len(p.buf) <= p.pos {
		p.raise("not terminated")
	}
	b := p.skipSpace()
	switch b {
	case '*':
		// expect ]
		b := p.skipSpace()
		if b != ']' {
			p.raise("not terminated")
		}
		return Wildcard('#')
	case '\'', '"':
		s := p.readStr(b)
		b = p.skipSpace()
		switch b {
		case ']':
			return Child(s)
		case ',':
			return p.readUnion(s, b)
		default:
			p.raise("invalid bracket fragment")
		}
	case ':':
		return p.readSlice(0)
	case '?':
		return p.readFilter()
	case '(':
		p.raise("scripts not implemented yet")
	case '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		var i int
		i, b = p.readInt(b)
	Next:
		switch b {
		case ' ':
			b = p.skipSpace()
			goto Next
		case ']':
			return Nth(i)
		case ',':
			return p.readUnion(int64(i), b)
		case ':':
			return p.readSlice(i)
		default:
			p.raise("invalid bracket fragment")
		}
	}
	p.pos--
	// Kind of ugly but needed to attain full cod coverage as the cover tool
	// and the complier don't know about panics in functions so get the return
	// and raise on the same line.
	return func() Frag { p.raise("parse error"); return nil }()
}

func (p *parser) readInt(b byte) (int, byte) {
	// Allow numbers to begin with a zero.
	/*
		if b == '0' {
			if p.pos < len(p.buf) {
				b = p.buf[p.pos]
				p.pos++
			}
			return 0, b, nil
		}
	*/
	neg := b == '-'
	if neg {
		if len(p.buf) <= p.pos {
			p.raise("expected a number")
		}
		b = p.buf[p.pos]
		p.pos++
	}
	start := p.pos
	var i int
	for {
		if b < '0' || '9' < b {
			break
		}
		i = i*10 + int(b-'0')
		if len(p.buf) <= p.pos {
			break
		}
		b = p.buf[p.pos]
		p.pos++
	}
	if p.pos == start {
		p.raise("expected a number")
	}
	if neg {
		i = -i
	}
	return i, b
}

func (p *parser) readNum(b byte) interface{} {
	var num []byte

	num = append(num, b)
	// Read digits first
	for p.pos < len(p.buf) {
		b = p.buf[p.pos]
		if b < '0' || '9' < b {
			break
		}
		num = append(num, b)
		p.pos++
	}
	switch b {
	case '.':
		num = append(num, b)
		p.pos++
		for p.pos < len(p.buf) {
			b = p.buf[p.pos]
			if b < '0' || '9' < b {
				break
			}
			num = append(num, b)
			p.pos++
		}
		if b == 'e' || b == 'E' {
			p.pos++
			num = append(num, b)
			if len(p.buf) <= p.pos {
				p.raise("expected a number")
			}
			b = p.buf[p.pos]
		} else {
			f, _ := strconv.ParseFloat(string(num), 64)
			return f
		}
	case 'e', 'E':
		p.pos++
		if len(p.buf) <= p.pos {
			p.raise("expected a number")
		}
		num = append(num, b)
		b = p.buf[p.pos]
	default:
		i, err := strconv.ParseInt(string(num), 10, 64)
		if err != nil {
			p.raise(err.Error())
		}
		return int(i)
	}
	if b == '+' || b == '-' {
		num = append(num, b)
		p.pos++
		if len(p.buf) <= p.pos {
			p.raise("expected a number")
		}
	}
	for p.pos < len(p.buf) {
		b = p.buf[p.pos]
		if b < '0' || '9' < b {
			break
		}
		num = append(num, b)
		p.pos++
	}
	f, _ := strconv.ParseFloat(string(num), 64)
	return f
}

func (p *parser) readSlice(i int) Frag {
	if len(p.buf) <= p.pos {
		p.raise("not terminated")
	}
	f := Slice{i}
	b := p.buf[p.pos]
	if b == ']' {
		f = append(f, math.MaxInt)
		p.pos++
		return f
	}
	b = p.skipSpace()
	// read the end
	if b == ':' {
		f = append(f, math.MaxInt)
		if len(p.buf) <= p.pos {
			p.raise("not terminated")
		}
		b = p.buf[p.pos]
		p.pos++
		if b != ']' {
			i, b = p.readInt(b)
			f = append(f, i)
		}
	} else {
		i, b = p.readInt(b)
		f = append(f, i)
		if b == ':' {
			if len(p.buf) <= p.pos {
				p.raise("not terminated")
			}
			b = p.buf[p.pos]
			p.pos++
			if b != ']' {
				i, b = p.readInt(b)
				f = append(f, i)
			}
		}
	}
	if b != ']' {
		p.raise("invalid slice syntax")
	}
	return f
}

func (p *parser) readUnion(v interface{}, b byte) Frag {
	if len(p.buf) <= p.pos {
		p.raise("not terminated")
	}
	f := Union{v}
	for {
		switch b {
		case ',':
			// next union member
		case ']':
			return f
		default:
			p.raise("invalid union syntax")
		}
		b = p.skipSpace()
		switch b {
		case '\'', '"':
			var s string
			s = p.readStr(b)
			b = p.skipSpace()
			f = append(f, s)
		case '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
			var i int
			i, b = p.readInt(b)
			f = append(f, int64(i))
			if b == ' ' {
				b = p.skipSpace()
			}
		default:
			p.raise("invalid union syntax")
		}
	}
}

func (p *parser) readStr(term byte) string {
	start := p.pos
	esc := false
	for p.pos < len(p.buf) {
		b := p.buf[p.pos]
		p.pos++
		if b == term && !esc {
			break
		}
		if b == '\\' {
			esc = !esc
		} else {
			esc = false
		}
	}
	return string(p.buf[start : p.pos-1])
}

func (p *parser) readFilter() *Filter {
	if len(p.buf) <= p.pos {
		p.raise("not terminated")
	}
	b := p.buf[p.pos]
	p.pos++
	if b != '(' {
		p.raise("expected a '(' in filter")
	}
	eq := p.readEquation()
	if len(p.buf) <= p.pos || p.buf[p.pos] != ']' {
		p.raise("not terminated")
	}
	p.pos++

	return eq.Filter()
}

func (p *parser) readEquation() (eq *Equation) {
	if len(p.buf) <= p.pos {
		p.raise("not terminated")
	}
	eq = &Equation{}

	b := p.nextNonSpace()
	if b == '!' {
		eq.o = not
		p.pos++
		eq.left = p.readEqValue()
		b := p.nextNonSpace()
		if b != ')' {
			p.raise("not terminated")
		}
		p.pos++
		return
	}
	eq.left = p.readEqValue()
	eq.o = p.readEqOp()
	eq.right = p.readEqValue()
	for {
		b = p.nextNonSpace()
		if b == ')' {
			p.pos++
			return
		}
		o := p.readEqOp()
		if eq.o.prec <= o.prec {
			eq = &Equation{left: eq, o: o}
			eq.right = p.readEqValue()
		} else {
			eq.right = &Equation{left: eq.right, o: o}
			eq.right.right = p.readEqValue()
		}
	}
}

func (p *parser) readEqValue() (eq *Equation) {
	b := p.nextNonSpace()
	switch b {
	case '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		var v interface{}
		p.pos++
		v = p.readNum(b)
		eq = &Equation{result: v}
	case '\'', '"':
		p.pos++
		var s string
		s = p.readStr(b)
		eq = &Equation{result: s}
	case 'n':
		p.readEqToken([]byte("null"))
		eq = &Equation{result: nil}
	case 't':
		p.readEqToken([]byte("true"))
		eq = &Equation{result: true}

	case 'f':
		p.readEqToken([]byte("false"))
		eq = &Equation{result: false}
	case '@', '$':
		x := p.readExpr()
		eq = &Equation{result: x}
	case '(':
		p.pos++
		eq = p.readEquation()
	default:
		p.raise("expected a value")
	}
	return
}

func (p *parser) readEqToken(token []byte) {
	for _, t := range token {
		if len(p.buf) <= p.pos || p.buf[p.pos] != t {
			p.raise("expected %s", token)
		}
		p.pos++
	}
}

func (p *parser) readEqOp() (o *op) {
	var token []byte
	b := p.nextNonSpace()
	for {
		if eqMap[b] != 'o' {
			break
		}
		token = append(token, b)
		if b == '-' && 1 < len(token) {
			p.raise("'%s' is not a valid operation", token)
			return
		}
		p.pos++
		if len(p.buf) <= p.pos {
			p.raise("equation not terminated")
		}
		b = p.buf[p.pos]
	}
	o = opMap[string(token)]
	if o == nil {
		p.raise("'%s' is not a valid operation", token)
	}
	return
}

func (p *parser) skipSpace() (b byte) {
	for p.pos < len(p.buf) {
		b = p.buf[p.pos]
		p.pos++
		if b != ' ' {
			break
		}
	}
	return
}

func (p *parser) nextNonSpace() (b byte) {
	for p.pos < len(p.buf) {
		b = p.buf[p.pos]
		if b != ' ' {
			break
		}
		p.pos++
	}
	return
}

func (p *parser) raise(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	panic(fmt.Errorf("%s at %d in %s", msg, p.pos+1, p.buf))
}
