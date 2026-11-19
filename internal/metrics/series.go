// SPDX-License-Identifier: AGPL-3.0-or-later

package metrics

import (
	"context"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// authKinds are the label values of the login series, set to zero up front
// so a dashboard sees them before the first failure.
var authKinds = []string{"password", "totp", "passkey", "token"}

// Panel series that other packages feed through hooks (ADR-0022). The label
// values come from closed sets in code: operation names, error codes,
// credential kinds and site system names.
type series struct {
	agentOps      *prometheus.CounterVec
	agentDuration *prometheus.HistogramVec
	authFailures  *prometheus.CounterVec
	authThrottled *prometheus.CounterVec
	webApply      *prometheus.CounterVec
	webGeneration prometheus.Gauge
}

func newSeries() series {
	s := series{
		agentOps: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "hostashi_agent_ops_total",
			Help: "Calls of the panel to the agent by operation and result.",
		}, []string{"op", "result"}),
		agentDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "hostashi_agent_op_duration_seconds",
			Help:    "Duration of agent calls by operation.",
			Buckets: []float64{0.01, 0.05, 0.25, 1, 5, 15, 60},
		}, []string{"op"}),
		authFailures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "hostashi_auth_failures_total",
			Help: "Failed logins and rejected API tokens by kind of credential.",
		}, []string{"kind"}),
		authThrottled: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "hostashi_auth_throttled_total",
			Help: "Login and token attempts refused by the rate limit, by kind of credential.",
		}, []string{"kind"}),
		webApply: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "hostashi_web_apply_total",
			Help: "Web server states sent to the agent, by result.",
		}, []string{"result"}),
		webGeneration: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "hostashi_web_generation",
			Help: "Generation of the web server state the agent last accepted (0: none since start).",
		}),
	}

	for _, k := range authKinds {
		s.authFailures.WithLabelValues(k)
		s.authThrottled.WithLabelValues(k)
	}

	for _, r := range []string{"ok", "error"} {
		s.webApply.WithLabelValues(r)
	}

	return s
}

func (s series) collectors() []prometheus.Collector {
	return []prometheus.Collector{s.agentOps, s.agentDuration, s.authFailures, s.authThrottled, s.webApply, s.webGeneration}
}

// AgentOp records one agent call. It fits agentrpc.Client.Observe.
func (m *Metrics) AgentOp(op, result string, d time.Duration) {
	m.s.agentOps.WithLabelValues(op, result).Inc()
	m.s.agentDuration.WithLabelValues(op).Observe(d.Seconds())
}

// Failure records a failed login or a rejected token (auth.Observer).
func (m *Metrics) Failure(kind string) { m.s.authFailures.WithLabelValues(kind).Inc() }

// Throttled records an attempt the rate limit refused (auth.Observer).
func (m *Metrics) Throttled(kind string) { m.s.authThrottled.WithLabelValues(kind).Inc() }

// WebApplied records one web.apply. It fits hosting.Service.ObserveApply.
func (m *Metrics) WebApplied(ok bool, generation int) {
	if !ok {
		m.s.webApply.WithLabelValues("error").Inc()

		return
	}

	m.s.webApply.WithLabelValues("ok").Inc()
	m.s.webGeneration.Set(float64(generation))
}

// Cert is one certificate end for hostashi_tls_cert_not_after_timestamp_seconds.
type Cert struct {
	Site     string // the site's system name, empty for the panel
	Cert     string // Hostashi's certificate name, never a domain
	NotAfter time.Time
}

// RegisterCerts reads the certificates on every gather through list. A
// failure leaves the series out of that scrape and is logged.
func (m *Metrics) RegisterCerts(list func(ctx context.Context) ([]Cert, error), log *slog.Logger) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	m.reg.MustRegister(&certCollector{list: list, log: log})
}

var certDesc = prometheus.NewDesc(
	"hostashi_tls_cert_not_after_timestamp_seconds",
	"End of validity of a live certificate, as Unix time.",
	[]string{"site", "cert"}, nil,
)

type certCollector struct {
	list func(ctx context.Context) ([]Cert, error)
	log  *slog.Logger
}

func (c *certCollector) Describe(ch chan<- *prometheus.Desc) { ch <- certDesc }

func (c *certCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	certs, err := c.list(ctx)
	if err != nil {
		c.log.Error("metrics: list certificates", "err", err)

		return
	}

	for _, ct := range certs {
		ch <- prometheus.MustNewConstMetric(certDesc, prometheus.GaugeValue, float64(ct.NotAfter.Unix()), ct.Site, ct.Cert)
	}
}
