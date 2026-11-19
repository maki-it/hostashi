// SPDX-License-Identifier: AGPL-3.0-or-later

package metrics

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSeriesExposition(t *testing.T) {
	m := New()

	m.AgentOp("web.apply", "ok", 200*time.Millisecond)
	m.AgentOp("web.apply", "unavailable", time.Second)
	m.Failure("password")
	m.Failure("password")
	m.Throttled("token")
	m.WebApplied(true, 7)
	m.WebApplied(false, 0)

	end := time.Unix(1801958400, 0)

	m.RegisterCerts(func(context.Context) ([]Cert, error) {
		return []Cert{{Site: "shop", Cert: "shop", NotAfter: end}, {Cert: "_panel", NotAfter: end}}, nil
	}, nil)

	body, err := m.Exposition()
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		`hostashi_agent_ops_total{op="web.apply",result="ok"} 1`,
		`hostashi_agent_ops_total{op="web.apply",result="unavailable"} 1`,
		`hostashi_agent_op_duration_seconds_count{op="web.apply"} 2`,
		`hostashi_auth_failures_total{kind="password"} 2`,
		`hostashi_auth_failures_total{kind="passkey"} 0`,
		`hostashi_auth_throttled_total{kind="token"} 1`,
		`hostashi_web_apply_total{result="ok"} 1`,
		`hostashi_web_apply_total{result="error"} 1`,
		`hostashi_web_generation 7`,
		`hostashi_tls_cert_not_after_timestamp_seconds{cert="shop",site="shop"} 1.8019584e+09`,
		`hostashi_tls_cert_not_after_timestamp_seconds{cert="_panel",site=""} 1.8019584e+09`,
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("missing %s", want)
		}
	}
}

func TestCertFailureKeepsTheRest(t *testing.T) {
	m := New()
	m.RegisterCerts(func(context.Context) ([]Cert, error) { return nil, errors.New("database locked") }, nil)

	body, err := m.Exposition()
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(body), "hostashi_tls_cert") || !strings.Contains(string(body), "go_goroutines") {
		t.Fatal(string(body))
	}
}
