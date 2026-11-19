// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"context"
	"testing"
)

type countObs struct{ fail, throttled map[string]int }

func (c *countObs) Failure(k string)   { c.fail[k]++ }
func (c *countObs) Throttled(k string) { c.throttled[k]++ }

func TestObserverSeesKindsOnly(t *testing.T) {
	e := newEnv(t)
	obs := &countObs{fail: map[string]int{}, throttled: map[string]int{}}
	e.svc.obs = obs
	e.setup(t)

	ctx := context.Background()

	_, _, _ = e.svc.Login(ctx, "kim", "wrong password!!", "", "6.6.6.6")
	_, _, _ = e.svc.Login(ctx, "nobody", "wrong password!!", "", "6.6.6.6")

	if _, _, err := e.svc.Login(ctx, "kim", testPassword, "", "7.7.7.7"); err != nil {
		t.Fatal(err)
	}

	_, _ = e.svc.AuthenticateToken(ctx, "hst_nothing", "8.8.8.8")
	_, _ = e.svc.AuthenticateToken(ctx, "no prefix", "8.8.8.8")

	if obs.fail[KindPassword] != 2 || obs.fail[KindToken] != 2 || len(obs.throttled) != 0 {
		t.Fatalf("%+v", obs)
	}

	for range 10 {
		_, _, _ = e.svc.Login(ctx, "kim", "wrong password!!", "", "9.9.9.9")
	}

	_, _, _ = e.svc.Login(ctx, "kim", testPassword, "", "9.9.9.9")

	if obs.throttled[KindPassword] != 1 {
		t.Fatalf("%+v", obs)
	}
}
