// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"net/netip"
	"sort"
)

type clientIPKey struct{}

// WithClientIP returns ctx carrying the address of the request's client, so
// that every audit entry written while serving it records the address. A
// value that is not an IP address is dropped.
func WithClientIP(ctx context.Context, ip string) context.Context {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return ctx
	}

	return context.WithValue(ctx, clientIPKey{}, a.Unmap().String())
}

// ClientIP returns the address WithClientIP put in ctx, or "".
func ClientIP(ctx context.Context) string {
	ip, _ := ctx.Value(clientIPKey{}).(string)

	return ip
}

// Change is the old and new value of one field. A nil side means the field
// did not exist on that side.
type Change struct {
	Old *string `json:"old"`
	New *string `json:"new"`
}

// Diff maps a field to its change.
type Diff map[string]Change

// DiffOf compares two descriptions of the same object, as the audit details
// describe it, and returns the fields that differ. Equal maps give an empty
// diff. Neither map may hold secrets.
func DiffOf(before, after map[string]string) Diff {
	keys := make(map[string]bool, len(before)+len(after))
	for k := range before {
		keys[k] = true
	}

	for k := range after {
		keys[k] = true
	}

	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}

	sort.Strings(sorted)

	d := Diff{}

	for _, k := range sorted {
		o, okO := before[k]
		n, okN := after[k]

		if okO == okN && o == n {
			continue
		}

		var c Change
		if okO {
			c.Old = &o
		}

		if okN {
			c.New = &n
		}

		d[k] = c
	}

	return d
}
