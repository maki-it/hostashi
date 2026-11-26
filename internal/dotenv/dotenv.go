// SPDX-License-Identifier: AGPL-3.0-or-later

// Package dotenv reads and changes single keys of a .env file (Laravel,
// Symfony; ADR-0070) as typed edits: every other line stays as it is. It
// understands KEY=value, export KEY=value, single and double quotes and
// comments, the subset both frameworks' loaders share. Values it writes
// are double-quoted and hold nothing that needs an escape.
package dotenv

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// ErrValue is a value Set cannot write.
var ErrValue = errors.New("dotenv: the value holds a control character, a quote, a backslash, $ or a backtick")

// ErrUnclosed is a file whose last double-quoted value never ends; a key
// appended to it would be part of that value.
var ErrUnclosed = errors.New("dotenv: a quoted value is not closed")

// keyRe is the name of a variable.
var keyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// line is one assignment: where its value starts and ends in the line,
// and the parsed value.
type line struct {
	key        string
	start, end int // value bytes, quotes included
	value      string
}

// parse reads an assignment; ok is false for blank lines, comments and
// anything else.
func parse(l string) (line, bool) {
	i := len(l) - len(strings.TrimLeft(l, " \t"))
	rest := l[i:]

	if strings.HasPrefix(rest, "export ") {
		i += len("export ")
		rest = strings.TrimLeft(l[i:], " \t")
		i = len(l) - len(rest)
	}

	eq := strings.IndexByte(rest, '=')
	if eq <= 0 {
		return line{}, false
	}

	key := strings.TrimRight(rest[:eq], " \t")
	if !keyRe.MatchString(key) {
		return line{}, false
	}

	vs := i + eq + 1
	for vs < len(l) && (l[vs] == ' ' || l[vs] == '\t') {
		vs++
	}

	out := line{key: key, start: vs}
	v := l[vs:]

	switch {
	case strings.HasPrefix(v, `"`):
		var b []byte

		j := 1
		for ; j < len(v) && v[j] != '"'; j++ {
			if v[j] == '\\' && j+1 < len(v) {
				j++

				if v[j] == 'n' {
					b = append(b, '\n')
				} else {
					b = append(b, v[j])
				}

				continue
			}

			b = append(b, v[j])
		}

		if j >= len(v) {
			// A multi-line value: not ours to change.
			return line{key: key, start: -1}, false
		}

		out.end, out.value = vs+j+1, string(b)
	case strings.HasPrefix(v, "'"):
		j := strings.IndexByte(v[1:], '\'')
		if j < 0 {
			return line{}, false
		}

		out.end, out.value = vs+j+2, v[1:j+1]
	default:
		end := len(v)
		if c := strings.Index(v, " #"); c >= 0 {
			end = c
		}

		if c := strings.Index(v, "\t#"); c >= 0 && c < end {
			end = c
		}

		if strings.HasPrefix(v, "#") {
			end = 0
		}

		out.end, out.value = vs+end, strings.TrimRight(v[:end], " \t")
	}

	return out, true
}

// lines splits content into lines without their "\n".
func lines(content []byte) []string {
	s := strings.TrimSuffix(string(content), "\n")
	if s == "" {
		return nil
	}

	return strings.Split(s, "\n")
}

// assignments calls fn for every assignment line, skipping the lines of
// a double-quoted value that goes on over several lines. It reports
// whether the last such value is never closed.
func assignments(ls []string, fn func(i int, a line)) (open bool) {
	inside := false

	for i, l := range ls {
		body := strings.TrimSuffix(l, "\r")

		if inside {
			if closesQuote(body) {
				inside = false
			}

			continue
		}

		a, ok := parse(body)
		if ok {
			fn(i, a)
		} else if a.start == -1 {
			inside = true
		}
	}

	return inside
}

// closesQuote reports whether a continuation line ends the value: it has
// a double quote that no backslash escapes.
func closesQuote(l string) bool {
	for j := 0; j < len(l); j++ {
		switch l[j] {
		case '\\':
			j++
		case '"':
			return true
		}
	}

	return false
}

// Get returns the value of key as the last assignment sets it.
func Get(content []byte, key string) (string, bool) {
	var (
		val string
		ok  bool
	)

	assignments(lines(content), func(_ int, a line) {
		if a.key == key {
			val, ok = a.value, true
		}
	})

	return val, ok
}

// Quote writes v as a double-quoted value. It refuses what the loaders
// would read differently inside double quotes (escapes, variables,
// command substitution), so no escaping is needed.
func Quote(v string) (string, error) {
	for _, r := range v {
		if r < 0x20 || r == 0x7f || strings.ContainsRune("\"\\$`", r) {
			return "", ErrValue
		}
	}

	return `"` + v + `"`, nil
}

// Set changes the values of the keys in set and appends the ones the file
// lacks, in the order of keys. Every assignment of a key is changed, so
// the last one, which wins, has the new value.
func Set(content []byte, keys []string, set map[string]string) ([]byte, error) {
	quoted := map[string]string{}

	for _, k := range keys {
		if !keyRe.MatchString(k) {
			return nil, fmt.Errorf("dotenv: invalid key %q", k)
		}

		q, err := Quote(set[k])
		if err != nil {
			return nil, fmt.Errorf("%w: %s", err, k)
		}

		quoted[k] = q
	}

	seen := map[string]bool{}
	ls := lines(content)

	open := assignments(ls, func(i int, a line) {
		q, want := quoted[a.key]
		if !want {
			return
		}

		l := ls[i]
		cr := strings.HasSuffix(l, "\r")
		body := strings.TrimSuffix(l, "\r")

		seen[a.key] = true
		body = body[:a.start] + q + body[a.end:]

		if cr {
			body += "\r"
		}

		ls[i] = body
	})

	if open {
		return nil, ErrUnclosed
	}

	var b []byte

	for _, l := range ls {
		b = append(b, l+"\n"...)
	}

	for _, k := range keys {
		if !seen[k] {
			b = append(b, k+"="+quoted[k]+"\n"...)
		}
	}

	return b, nil
}
