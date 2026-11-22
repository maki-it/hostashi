// SPDX-License-Identifier: AGPL-3.0-or-later

package updates

import "strings"

// Compare compares two Debian package versions like dpkg --compare-versions:
// negative if a is older than b, zero if equal, positive if newer.
func Compare(a, b string) int {
	ea, ua, ra := split(a)
	eb, ub, rb := split(b)

	if c := cmpNum(ea, eb); c != 0 {
		return c
	}

	if c := cmpPart(ua, ub); c != 0 {
		return c
	}

	return cmpPart(ra, rb)
}

// split returns the epoch, the upstream version and the revision.
func split(v string) (epoch, upstream, revision string) {
	if i := strings.IndexByte(v, ':'); i >= 0 {
		epoch, v = v[:i], v[i+1:]
	}

	if i := strings.LastIndexByte(v, '-'); i >= 0 {
		return epoch, v[:i], v[i+1:]
	}

	return epoch, v, ""
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// order is the weight of a non-digit character: ~ before everything, even
// the end of the string, then the end, then letters, then the rest.
func order(c byte) int {
	switch {
	case c == '~':
		return -1
	case isDigit(c):
		return 0
	case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z':
		return int(c)
	default:
		return int(c) + 256
	}
}

func cmpPart(a, b string) int {
	for a != "" || b != "" {
		// The non-digit prefix.
		for (a != "" && !isDigit(a[0])) || (b != "" && !isDigit(b[0])) {
			var ca, cb int
			if a != "" && !isDigit(a[0]) {
				ca = order(a[0])
			}

			if b != "" && !isDigit(b[0]) {
				cb = order(b[0])
			}

			if ca != cb {
				return ca - cb
			}

			if a != "" && !isDigit(a[0]) {
				a = a[1:]
			}

			if b != "" && !isDigit(b[0]) {
				b = b[1:]
			}
		}

		// The digit run.
		var na, nb string

		na, a = digits(a)
		nb, b = digits(b)

		if c := cmpNum(na, nb); c != 0 {
			return c
		}
	}

	return 0
}

func digits(s string) (run, rest string) {
	i := 0
	for i < len(s) && isDigit(s[i]) {
		i++
	}

	return s[:i], s[i:]
}

// cmpNum compares runs of digits of any length.
func cmpNum(a, b string) int {
	a = strings.TrimLeft(a, "0")
	b = strings.TrimLeft(b, "0")

	if len(a) != len(b) {
		return len(a) - len(b)
	}

	return strings.Compare(a, b)
}
