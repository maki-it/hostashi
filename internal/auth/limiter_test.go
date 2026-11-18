// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"strconv"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func TestLimiterBlocksAndExpires(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	l := NewLimiter(3, time.Minute, clk.now)

	for range 2 {
		l.Fail("a")
	}

	if l.Blocked("a") {
		t.Fatal("blocked too early")
	}

	l.Fail("a")

	if !l.Blocked("a") || l.Blocked("b") {
		t.Fatal("wrong blocking")
	}

	clk.t = clk.t.Add(time.Minute)

	if l.Blocked("a") {
		t.Fatal("still blocked after the window")
	}

	l.Fail("a")
	l.Reset("a")

	for range 2 {
		l.Fail("a")
	}

	if l.Blocked("a") {
		t.Fatal("Reset did not clear the count")
	}
}

func TestLimiterFailsClosedWhenFull(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	l := NewLimiter(3, time.Minute, clk.now)
	l.maxKeys = 5

	for i := range 5 {
		l.Fail("k" + strconv.Itoa(i))
	}

	if !l.Blocked("new") {
		t.Fatal("new key allowed while the table is full of live entries")
	}

	clk.t = clk.t.Add(2 * time.Minute)

	if l.Blocked("new") {
		t.Fatal("expired entries were not pruned")
	}
}
