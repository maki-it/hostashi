// SPDX-License-Identifier: AGPL-3.0-or-later

// Package nginxconf is a small, strict parser for the nginx configuration
// syntax (directives, blocks, quoting, comments). It does not evaluate
// anything: no include, no variables, no regex. It exists so that the adopt
// scanner and the custom snippet check (package snippet, ADR-0029) never
// have to pattern-match on raw text.
package nginxconf

import (
	"errors"
	"fmt"
	"strings"
)

// Limits protect the scanner from hostile or broken files.
const (
	MaxBytes      = 1 << 20
	MaxDepth      = 16
	MaxDirectives = 20000
)

// Directive is one statement. Block is nil for simple directives and non-nil
// (possibly empty) for blocks.
type Directive struct {
	Name  string
	Args  []string
	Block []Directive
	Line  int
	// Raw is the source text of the directive including its block, kept so
	// that unknown directives can be preserved verbatim for review.
	Raw string
	// File is empty after Parse; callers that inline includes set it to
	// the file the directive came from.
	File string
}

// IsBlock reports whether the directive has braces.
func (d Directive) IsBlock() bool { return d.Block != nil }

// Error is a syntax error with a line number.
type Error struct {
	Line int
	Msg  string
}

func (e *Error) Error() string { return fmt.Sprintf("line %d: %s", e.Line, e.Msg) }

type tokKind int

const (
	tWord tokKind = iota
	tSemi
	tOpen
	tClose
)

type token struct {
	kind  tokKind
	text  string
	line  int
	start int // byte offset of the first byte
	end   int // byte offset after the last byte
}

func tokenize(src string) ([]token, error) {
	var out []token

	line := 1

	i := 0
	for i < len(src) {
		c := src[i]
		switch c {
		case '\n':
			line++
			i++
		case ' ', '\t', '\r':
			i++
		case '#':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case ';':
			out = append(out, token{tSemi, ";", line, i, i + 1})
			i++
		case '{':
			out = append(out, token{tOpen, "{", line, i, i + 1})
			i++
		case '}':
			out = append(out, token{tClose, "}", line, i, i + 1})
			i++
		case 0:
			return nil, &Error{line, "NUL byte"}
		case '"', '\'':
			start, startLine := i, line
			q := c
			i++

			var sb strings.Builder

			closed := false

			for i < len(src) {
				ch := src[i]
				if ch == '\\' && i+1 < len(src) {
					// nginx keeps unknown escapes literally; for the
					// quote and backslash we drop the backslash.
					if src[i+1] == q || src[i+1] == '\\' {
						_ = sb.WriteByte(src[i+1])
					} else {
						_ = sb.WriteByte(ch)
						_ = sb.WriteByte(src[i+1])
					}

					if src[i+1] == '\n' {
						line++
					}

					i += 2

					continue
				}

				if ch == q {
					closed = true
					i++

					break
				}

				if ch == '\n' {
					line++
				}

				_ = sb.WriteByte(ch)

				i++
			}

			if !closed {
				return nil, &Error{startLine, "unterminated quoted string"}
			}

			out = append(out, token{tWord, sb.String(), startLine, start, i})
		default:
			start := i
			for i < len(src) {
				ch := src[i]
				if ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n' || ch == ';' || ch == '{' || ch == '}' || ch == '#' || ch == 0 {
					break
				}

				i++
			}

			out = append(out, token{tWord, src[start:i], line, start, i})
		}
	}

	return out, nil
}

// Parse parses a whole file.
func Parse(src string) ([]Directive, error) {
	if len(src) > MaxBytes {
		return nil, &Error{1, fmt.Sprintf("file larger than %d bytes", MaxBytes)}
	}

	toks, err := tokenize(src)
	if err != nil {
		return nil, err
	}

	p := &parser{toks: toks, src: src}

	ds, err := p.list(0, false)
	if err != nil {
		return nil, err
	}

	if p.pos != len(toks) {
		return nil, &Error{toks[p.pos].line, "unexpected '}'"}
	}

	return ds, nil
}

type parser struct {
	toks  []token
	pos   int
	src   string
	count int
}

func (p *parser) list(depth int, inBlock bool) ([]Directive, error) {
	if depth > MaxDepth {
		return nil, &Error{p.toks[p.pos-1].line, "blocks nested too deeply"}
	}

	out := []Directive{}

	for p.pos < len(p.toks) {
		t := p.toks[p.pos]
		if t.kind == tClose {
			if !inBlock {
				return nil, &Error{t.line, "unexpected '}'"}
			}

			return out, nil
		}

		if t.kind != tWord {
			return nil, &Error{t.line, fmt.Sprintf("unexpected %q, expected a directive name", t.text)}
		}

		if t.text == "" {
			return nil, &Error{t.line, "empty directive name"}
		}

		p.count++
		if p.count > MaxDirectives {
			return nil, &Error{t.line, "too many directives"}
		}

		d := Directive{Name: t.text, Line: t.line}
		startOff := t.start
		p.pos++

		done := false
		for !done {
			if p.pos >= len(p.toks) {
				return nil, &Error{t.line, fmt.Sprintf("directive %q is not terminated", d.Name)}
			}

			a := p.toks[p.pos]
			switch a.kind {
			case tWord:
				d.Args = append(d.Args, a.text)
				p.pos++
			case tSemi:
				d.Raw = p.src[startOff:a.end]
				p.pos++
				done = true
			case tOpen:
				p.pos++

				blk, err := p.list(depth+1, true)
				if err != nil {
					return nil, err
				}

				if p.pos >= len(p.toks) {
					return nil, &Error{a.line, "missing '}'"}
				}

				closeTok := p.toks[p.pos]
				p.pos++
				d.Block = blk
				d.Raw = p.src[startOff:closeTok.end]
				done = true
			case tClose:
				return nil, &Error{a.line, fmt.Sprintf("directive %q is missing ';'", d.Name)}
			}
		}

		out = append(out, d)
	}

	if inBlock {
		return nil, errors.New("missing '}' at end of file")
	}

	return out, nil
}

// Find returns the direct children with the given name.
func Find(ds []Directive, name string) []Directive {
	var out []Directive

	for _, d := range ds {
		if d.Name == name {
			out = append(out, d)
		}
	}

	return out
}

// First returns the first direct child with the given name.
func First(ds []Directive, name string) (Directive, bool) {
	for _, d := range ds {
		if d.Name == name {
			return d, true
		}
	}

	return Directive{}, false
}
