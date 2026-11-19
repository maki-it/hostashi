// SPDX-License-Identifier: AGPL-3.0-or-later

package mariadb

import "io"

// maxClause bounds how much of a possible DEFINER clause the filter holds
// back. A user name has at most 80 and a host at most 255 characters, so a
// real clause is far shorter; anything longer passes unchanged.
const maxClause = 1024

// StripDefiners returns a reader that yields r with every
// DEFINER=`user`@`host` clause removed (ADR-0030). Names may be quoted with
// backquotes, single or double quotes. The clause is only recognized in SQL
// code and in executable comments (/*! ... */), never inside a string, a
// quoted identifier or a plain comment, so data is not changed. Nothing else
// is touched: a statement without a definer passes byte for byte.
//
// The filter is not a security boundary. A clause it does not recognize
// stays in the dump, and the hardened import then refuses the statement.
func StripDefiners(r io.Reader) io.Reader {
	return &definerReader{src: r, buf: make([]byte, 32*1024)}
}

type definerReader struct {
	src io.Reader
	buf []byte
	s   stripper
	off int
	err error
}

func (d *definerReader) Read(p []byte) (int, error) {
	for d.off == len(d.s.out) {
		if d.err != nil {
			return 0, d.err
		}

		d.s.out, d.off = d.s.out[:0], 0
		n, err := d.src.Read(d.buf)
		d.s.feed(d.buf[:n])

		if err != nil {
			d.s.finish()
			d.err = err
		}
	}

	n := copy(p, d.s.out[d.off:])
	d.off += n

	return n, nil
}

type lexState uint8

const (
	stCode       lexState = iota
	stString              // inside quote
	stSlash               // code, the last byte was '/'
	stCommentBeg          // after "/*", before the byte that tells "/*!" apart
	stComment             // plain /* */ comment
	stDash                // code, the last two bytes were "--"
	stLine                // comment to the end of the line
)

type stripper struct {
	out   []byte
	pend  []byte
	st    lexState
	quote byte
	esc   bool // the last byte in a string was a backslash
	star  bool // the last byte in a comment or code was '*'
	exec  bool // inside an executable comment /*! ... */
	prev  byte // the last byte of code, for the word boundary
	eof   bool // no more input follows
}

func (s *stripper) feed(p []byte) {
	for _, c := range p {
		s.byte(c)
	}
}

// finish decides what was held back, now that no more input follows.
func (s *stripper) finish() {
	s.eof = true

	for s.pend != nil {
		if n, st := matchDefiner(s.pend, true); st == matched {
			rest := append([]byte(nil), s.pend[n:]...)
			s.pend = nil
			s.feed(rest)

			continue
		}

		s.reject()
	}
}

func (s *stripper) byte(c byte) {
	if s.pend == nil {
		s.lex(c)

		return
	}

	s.pend = append(s.pend, c)

	switch n, st := matchDefiner(s.pend, s.eof); st {
	case needMore:
		if len(s.pend) > maxClause {
			s.reject()
		}
	case matched:
		rest := append([]byte(nil), s.pend[n:]...)
		s.pend = nil
		s.prev, s.star = ' ', false
		s.feed(rest)
	case noMatch:
		s.reject()
	}
}

// reject emits the first held-back byte as code and lexes the rest again,
// since a later byte may start a clause of its own.
func (s *stripper) reject() {
	p := s.pend
	s.pend = nil
	s.emitCode(p[0])
	s.feed(p[1:])
}

func (s *stripper) emitCode(c byte) {
	s.out = append(s.out, c)
	s.prev = c
	s.star = c == '*'
}

func (s *stripper) lex(c byte) {
	switch s.st {
	case stString:
		s.out = append(s.out, c)

		switch {
		case s.esc:
			s.esc = false
		case c == '\\' && s.quote != '`':
			s.esc = true
		case c == s.quote:
			s.st = stCode
			s.prev = c
		}

		return
	case stCommentBeg:
		s.out = append(s.out, c)

		if c == '!' {
			s.exec, s.st, s.prev, s.star = true, stCode, ' ', false

			return
		}

		s.st, s.star = stComment, c == '*'

		return
	case stComment:
		s.out = append(s.out, c)

		if s.star && c == '/' {
			s.st, s.prev = stCode, ' '
		}

		s.star = c == '*'

		return
	case stLine:
		s.out = append(s.out, c)

		if c == '\n' {
			s.st, s.prev = stCode, '\n'
		}

		return
	case stSlash:
		if c == '*' {
			s.out = append(s.out, c)
			s.st = stCommentBeg

			return
		}

		s.st = stCode
	case stDash:
		s.st = stCode

		if c <= ' ' && c != '\n' {
			s.out = append(s.out, c)
			s.st = stLine

			return
		}
	case stCode:
	}

	s.code(c)
}

func (s *stripper) code(c byte) {
	star := s.star

	switch {
	case c == 'D' || c == 'd':
		if !isIdent(s.prev) {
			s.pend = append(s.pend[:0:0], c)

			return
		}
	case c == '\'' || c == '"' || c == '`':
		s.st, s.quote, s.esc = stString, c, false
	case c == '#':
		s.st = stLine
	case c == '/':
		if s.exec && star {
			s.exec = false
		} else {
			s.st = stSlash
		}
	case c == '-' && s.prev == '-':
		s.st = stDash
	}

	s.emitCode(c)
}

func isIdent(c byte) bool {
	return c == '_' || c == '$' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
}

type matchState uint8

const (
	noMatch matchState = iota
	needMore
	matched
)

// matchDefiner reports whether p starts with DEFINER = 'user' @ 'host'. On a
// match, n is the length of the clause. With eof, p is all the input left.
func matchDefiner(p []byte, eof bool) (n int, st matchState) {
	const kw = "definer"

	for i := range len(kw) {
		if i >= len(p) {
			return 0, needMore
		}

		if p[i]|0x20 != kw[i] {
			return 0, noMatch
		}
	}

	i := skipSpace(p, len(kw))
	if i >= len(p) {
		return 0, needMore
	}

	if p[i] != '=' {
		return 0, noMatch
	}

	if i, st = quotedName(p, skipSpace(p, i+1), eof); st != matched {
		return 0, st
	}

	if i = skipSpace(p, i); i >= len(p) {
		return 0, needMore
	}

	if p[i] != '@' {
		return 0, noMatch
	}

	if i, st = quotedName(p, skipSpace(p, i+1), eof); st != matched {
		return 0, st
	}

	return i, matched
}

func skipSpace(p []byte, i int) int {
	for i < len(p) && (p[i] == ' ' || p[i] == '\t' || p[i] == '\n' || p[i] == '\r') {
		i++
	}

	return i
}

// quotedName returns the end of the quoted name at p[i]. A doubled quote is
// part of the name; in ” and "" names, so is a backslash escape.
func quotedName(p []byte, i int, eof bool) (int, matchState) {
	if i >= len(p) {
		return 0, needMore
	}

	q := p[i]
	if q != '`' && q != '\'' && q != '"' {
		return 0, noMatch
	}

	for j := i + 1; ; j++ {
		if j >= len(p) {
			return 0, needMore
		}

		switch {
		case p[j] == '\\' && q != '`':
			j++
		case p[j] == q:
			if j+1 >= len(p) {
				if eof {
					return j + 1, matched
				}

				return 0, needMore
			}

			if p[j+1] != q {
				return j + 1, matched
			}

			j++
		}
	}
}
