// SPDX-License-Identifier: AGPL-3.0-or-later

// Package cpmigrate keeps uploaded CloudPanel scan reports and shows their
// migration plan against this server (ADR-0017, ADR-0026). It runs in the
// unprivileged web process: creating a site goes through the site service,
// copying data through the agent's import job.
package cpmigrate

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"time"

	"github.com/maki-it/hostashi/internal/cloudpanel/plan"
	"github.com/maki-it/hostashi/internal/cloudpanel/scan"
	"github.com/maki-it/hostashi/internal/store"
)

// MaxReports bounds how many reports are kept at the same time.
const MaxReports = 20

// Errors of the service.
var (
	ErrNotFound   = errors.New("cpmigrate: no such report")
	ErrTooMany    = fmt.Errorf("cpmigrate: at most %d reports are kept, delete one first", MaxReports)
	ErrInvalid    = plan.ErrInvalid
	sealedContext = "cloudpanel-report:"
)

// Sealer encrypts the stored report (internal/secrets.Box).
type Sealer interface {
	Seal(plaintext []byte, context string) ([]byte, error)
	Open(data []byte, context string) ([]byte, error)
}

// TargetFunc describes the server as it is now: its sites, their domains and
// the installed PHP versions.
type TargetFunc func(ctx context.Context) (plan.Target, error)

// Service stores reports and builds plans.
type Service struct {
	st     *store.Store
	box    Sealer
	target TargetFunc
	log    *slog.Logger
	now    func() time.Time

	create   SiteCreator
	importer Importer
	loads    *loads
	cutover  *cutover
}

// New returns a service. log may be nil.
func New(st *store.Store, box Sealer, target TargetFunc, log *slog.Logger) *Service {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	return &Service{st: st, box: box, target: target, log: log, now: time.Now}
}

// Report is a stored report with its plan as of now.
type Report struct {
	ID        string
	Sites     int
	CreatedAt time.Time
	Plan      *plan.Plan // nil in lists
}

// Upload checks, seals and stores one report and returns its plan.
func (s *Service) Upload(ctx context.Context, actor int64, r io.Reader) (Report, error) {
	b, err := io.ReadAll(io.LimitReader(r, plan.MaxReportBytes+1))
	if err != nil {
		return Report{}, err
	}

	rep, err := plan.Parse(bytes.NewReader(b))
	if err != nil {
		return Report{}, err
	}

	// The plan first: a report is only kept when it can be shown.
	p, err := s.planFor(ctx, rep)
	if err != nil {
		return Report{}, err
	}

	list, err := s.st.CloudPanelReports(ctx)
	if err != nil {
		return Report{}, err
	}

	if len(list) >= MaxReports {
		return Report{}, ErrTooMany
	}

	id := newID()

	sealed, err := s.box.Seal(b, sealedContext+id)
	if err != nil {
		return Report{}, err
	}

	created := s.now().UTC().Truncate(time.Second)
	if err := s.st.CreateCloudPanelReport(ctx, store.CloudPanelReport{
		ID: id, Sites: len(rep.Sites), Sealed: sealed, CreatedBy: &actor, CreatedAt: created,
	}); err != nil {
		return Report{}, err
	}

	s.audit(ctx, actor, "cloudpanel.report.upload", id, map[string]string{"sites": strconv.Itoa(len(rep.Sites))})

	return Report{ID: id, Sites: len(rep.Sites), CreatedAt: created, Plan: &p}, nil
}

// List returns the stored reports without plans, newest first.
func (s *Service) List(ctx context.Context) ([]Report, error) {
	list, err := s.st.CloudPanelReports(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]Report, 0, len(list))
	for _, r := range list {
		out = append(out, Report{ID: r.ID, Sites: r.Sites, CreatedAt: r.CreatedAt})
	}

	return out, nil
}

// Get returns a report with its plan against the server as it is now.
func (s *Service) Get(ctx context.Context, id string) (Report, error) {
	rep, row, err := s.load(ctx, id)
	if err != nil {
		return Report{}, err
	}

	p, err := s.planFor(ctx, rep)
	if err != nil {
		return Report{}, err
	}

	return Report{ID: row.ID, Sites: row.Sites, CreatedAt: row.CreatedAt, Plan: &p}, nil
}

// Delete removes a report.
func (s *Service) Delete(ctx context.Context, actor int64, id string) error {
	if !validID(id) {
		return ErrNotFound
	}

	err := s.st.DeleteCloudPanelReport(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	}

	if err != nil {
		return err
	}

	s.audit(ctx, actor, "cloudpanel.report.delete", id, nil)

	return nil
}

// load opens and parses a stored report. It is parsed again, so a report
// stored by an older binary is checked against the current rules.
func (s *Service) load(ctx context.Context, id string) (*scan.Report, store.CloudPanelReport, error) {
	if !validID(id) {
		return nil, store.CloudPanelReport{}, ErrNotFound
	}

	row, err := s.st.CloudPanelReportByID(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, store.CloudPanelReport{}, ErrNotFound
	}

	if err != nil {
		return nil, store.CloudPanelReport{}, err
	}

	b, err := s.box.Open(row.Sealed, sealedContext+id)
	if err != nil {
		return nil, store.CloudPanelReport{}, fmt.Errorf("cpmigrate: open report %s: %w", id, err)
	}

	rep, err := plan.Parse(bytes.NewReader(b))
	if err != nil {
		return nil, store.CloudPanelReport{}, err
	}

	return rep, row, nil
}

func (s *Service) planFor(ctx context.Context, rep *scan.Report) (plan.Plan, error) {
	t, err := s.target(ctx)
	if err != nil {
		return plan.Plan{}, fmt.Errorf("cpmigrate: read this server's sites: %w", err)
	}

	return plan.Build(rep, t), nil
}

func (s *Service) audit(ctx context.Context, actor int64, action, target string, detail map[string]string) {
	if err := s.st.Audit(ctx, &actor, action, target, detail); err != nil {
		s.log.ErrorContext(ctx, "audit log", "action", action, "err", err)
	}
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b) // crypto/rand.Read never fails on Linux (Go 1.24+)

	return hex.EncodeToString(b)
}

func validID(id string) bool {
	if len(id) != 32 {
		return false
	}

	for i := range len(id) {
		if c := id[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}

	return true
}
