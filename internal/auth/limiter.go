// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"net/netip"
	"sync"
	"time"
)

// ipv6Prefix is how much of an IPv6 address one client is counted by. A
// host usually gets a whole /64, and picks any address in it, so counting
// single addresses would give one client 2^64 tries.
const ipv6Prefix = 64

// ipKey is the limiter key of a client address: an IPv4 address (also one
// mapped into IPv6) as it is, an IPv6 address by its /64. Anything else is
// used verbatim, so it still counts.
func ipKey(kind, ip string) string {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return kind + ip
	}

	a = a.Unmap().WithZone("")
	if a.Is4() {
		return kind + a.String()
	}

	return kind + netip.PrefixFrom(a, ipv6Prefix).Masked().String()
}

// Limiter blocks a key after max failures within window. State is in memory
// and resets on restart, which is acceptable for a login throttle.
// When the table is full of live entries, new keys are blocked (fail closed).
type Limiter struct {
	max     int
	window  time.Duration
	maxKeys int
	now     func() time.Time

	mu sync.Mutex
	m  map[string]entry
}

type entry struct {
	count int
	first time.Time
}

// NewLimiter creates a limiter. now may be nil.
func NewLimiter(maxFailures int, window time.Duration, now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}

	return &Limiter{max: maxFailures, window: window, maxKeys: 10000, now: now, m: map[string]entry{}}
}

// Blocked reports whether key has used up its failures.
func (l *Limiter) Blocked(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	e, ok := l.m[key]
	if !ok {
		return len(l.m) >= l.maxKeys && !l.pruneLocked()
	}

	if l.now().Sub(e.first) >= l.window {
		delete(l.m, key)

		return false
	}

	return e.count >= l.max
}

// Fail records one failure for key.
func (l *Limiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()

	e, ok := l.m[key]
	if ok && now.Sub(e.first) >= l.window {
		ok = false
	}

	if !ok {
		if len(l.m) >= l.maxKeys && !l.pruneLocked() {
			return
		}

		e = entry{first: now}
	}

	e.count++
	l.m[key] = e
}

// Reset clears key, for example after a successful login.
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	delete(l.m, key)
}

// pruneLocked drops expired entries and reports whether there is room.
func (l *Limiter) pruneLocked() bool {
	now := l.now()

	for k, e := range l.m {
		if now.Sub(e.first) >= l.window {
			delete(l.m, k)
		}
	}

	return len(l.m) < l.maxKeys
}
