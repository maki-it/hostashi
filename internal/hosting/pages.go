// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/statuspage"
	"github.com/maki-it/hostashi/internal/store"
)

// Errors of the page operations.
var (
	ErrPageNotFound = errors.New("hosting: unknown page")
	ErrPageBuiltin  = errors.New("hosting: built-in pages cannot be changed or deleted")
	ErrPageInUse    = errors.New("hosting: a site uses this page")
)

// Page is a maintenance or parking page (ADR-0027): a built-in one or an
// uploaded one. ID is also the page's id on the agent.
type Page struct {
	ID        string
	Name      string
	Kind      string
	Builtin   bool
	Digest    string
	Files     []store.StatusPageFile
	Sites     []string // sites that name the page
	CreatedAt time.Time
	UpdatedAt time.Time
}

// PageUpload is an uploaded page file: a single HTML file or a ZIP file.
type PageUpload struct {
	Filename string
	Data     []byte
}

const pagePrefix = "p"

func pageAgentID(id int64) string { return pagePrefix + strconv.FormatInt(id, 10) }

// parsePageID turns an API page id into the stored id; built-in pages and
// unknown forms are not stored pages.
func parsePageID(id string) (int64, bool) {
	rest, ok := strings.CutPrefix(id, pagePrefix)
	if !ok || rest == "" || rest[0] == '0' {
		return 0, false
	}

	n, err := strconv.ParseInt(rest, 10, 64)

	return n, err == nil && n > 0
}

var builtinNames = map[string]string{
	statuspage.BuiltinMaintenance: "Built-in maintenance page",
	statuspage.BuiltinParking:     "Built-in parking page",
}

func builtinPage(id string) (Page, map[string][]byte, bool) {
	files, kind, ok := statuspage.Builtin(id)
	if !ok {
		return Page{}, nil, false
	}

	list := make([]store.StatusPageFile, 0, len(files))
	for p, b := range files {
		list = append(list, store.StatusPageFile{Path: p, Size: len(b)})
	}

	return Page{ID: id, Name: builtinNames[id], Kind: kind, Builtin: true, Digest: agentapi.PageDigest(files), Files: list, Sites: []string{}}, files, true
}

func fromStored(p store.StatusPage) Page {
	return Page{
		ID: pageAgentID(p.ID), Name: p.Name, Kind: p.Kind, Digest: p.Digest, Files: p.Files, Sites: p.Sites,
		CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
	}
}

// Pages returns the built-in pages first, then the uploaded ones by name.
func (s *Service) Pages(ctx context.Context) ([]Page, error) {
	stored, err := s.st.StatusPages(ctx)
	if err != nil {
		return nil, err
	}

	sites, err := s.st.Sites(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]Page, 0, len(stored)+2)

	for _, id := range []string{statuspage.BuiltinMaintenance, statuspage.BuiltinParking} {
		p, _, _ := builtinPage(id)

		for _, site := range sites {
			if id == statuspage.BuiltinMaintenance && site.MaintenancePage == 0 && site.Maintenance {
				p.Sites = append(p.Sites, site.Name)
			}
		}

		out = append(out, p)
	}

	for _, p := range stored {
		out = append(out, fromStored(p))
	}

	return out, nil
}

// Page returns one page.
func (s *Service) Page(ctx context.Context, id string) (Page, error) {
	if p, _, ok := builtinPage(id); ok {
		return p, nil
	}

	n, ok := parsePageID(id)
	if !ok {
		return Page{}, ErrPageNotFound
	}

	p, err := s.st.StatusPageByID(ctx, n)
	if errors.Is(err, store.ErrNotFound) {
		return Page{}, ErrPageNotFound
	}

	if err != nil {
		return Page{}, err
	}

	return fromStored(p), nil
}

func checkPageName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 64 || !utf8.ValidString(name) {
		return "", fmt.Errorf("%w: a page name has 1 to 64 characters", ErrInvalid)
	}

	if strings.ContainsFunc(name, unicode.IsControl) {
		return "", fmt.Errorf("%w: a page name cannot contain control characters", ErrInvalid)
	}

	return name, nil
}

func parseUpload(u PageUpload) (map[string][]byte, string, error) {
	files, err := statuspage.Parse(u.Filename, u.Data)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	return files, agentapi.PageDigest(files), nil
}

// CreatePage stores an uploaded page. Nothing reaches the agent until a
// site uses it.
func (s *Service) CreatePage(ctx context.Context, actor int64, name, kind string, u PageUpload) (Page, error) {
	name, err := checkPageName(name)
	if err != nil {
		return Page{}, err
	}

	if kind != statuspage.KindMaintenance && kind != statuspage.KindParking {
		return Page{}, fmt.Errorf("%w: kind must be %q or %q", ErrInvalid, statuspage.KindMaintenance, statuspage.KindParking)
	}

	files, digest, err := parseUpload(u)
	if err != nil {
		return Page{}, err
	}

	id, err := s.st.CreateStatusPage(ctx, name, kind, digest, files, s.now())
	if err != nil {
		return Page{}, err
	}

	s.audit(ctx, actor, "page.create", pageAgentID(id), map[string]string{
		"name": name, "kind": kind, "sha256": digest, "files": strconv.Itoa(len(files)),
	})

	return s.Page(ctx, pageAgentID(id))
}

// UpdatePage renames a page and/or replaces its files. Sites in
// maintenance with it get the new files at once; if that fails, the old
// files stay.
func (s *Service) UpdatePage(ctx context.Context, actor int64, id, name string, u *PageUpload) (Page, error) {
	if _, _, ok := builtinPage(id); ok {
		return Page{}, ErrPageBuiltin
	}

	n, ok := parsePageID(id)
	if !ok {
		return Page{}, ErrPageNotFound
	}

	if name != "" {
		var err error
		if name, err = checkPageName(name); err != nil {
			return Page{}, err
		}
	}

	var (
		files  map[string][]byte
		digest string
	)

	if u != nil {
		var err error
		if files, digest, err = parseUpload(*u); err != nil {
			return Page{}, err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	old, err := s.st.StatusPageByID(ctx, n)
	if errors.Is(err, store.ErrNotFound) {
		return Page{}, ErrPageNotFound
	}

	if err != nil {
		return Page{}, err
	}

	var oldFiles map[string][]byte
	if files != nil {
		if oldFiles, err = s.st.StatusPageFiles(ctx, n); err != nil {
			return Page{}, err
		}
	}

	if err := s.st.UpdateStatusPage(ctx, n, name, digest, files, s.now()); err != nil {
		return Page{}, err
	}

	if files != nil && digest != old.Digest && len(old.Sites) > 0 {
		if err := s.applyWeb(ctx); err != nil {
			if rerr := s.st.UpdateStatusPage(context.WithoutCancel(ctx), n, old.Name, old.Digest, oldFiles, s.now()); rerr != nil {
				s.log.ErrorContext(ctx, "restore page after failed change", "page", id, "err", rerr)
			}

			return Page{}, err
		}
	}

	detail := map[string]string{}
	if name != "" {
		detail["name"] = name
	}

	if files != nil {
		detail["sha256"], detail["files"] = digest, strconv.Itoa(len(files))
	}

	s.audit(ctx, actor, "page.update", id, detail)

	return s.Page(ctx, id)
}

// DeletePage removes a page that no site names, also from the agent.
func (s *Service) DeletePage(ctx context.Context, actor int64, id string) error {
	if _, _, ok := builtinPage(id); ok {
		return ErrPageBuiltin
	}

	n, ok := parsePageID(id)
	if !ok {
		return ErrPageNotFound
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	switch err := s.st.DeleteStatusPage(ctx, n); {
	case errors.Is(err, store.ErrNotFound):
		return ErrPageNotFound
	case errors.Is(err, store.ErrPageInUse):
		return ErrPageInUse
	case err != nil:
		return err
	}

	s.audit(ctx, actor, "page.delete", id, nil)

	// The agent may still hold an old copy; the web server no longer uses
	// it, so a failure only leaves a few files behind.
	if err := s.ag.PageDelete(ctx, id); err != nil {
		s.log.WarnContext(ctx, "remove page from the agent", "page", id, "err", err)
	}

	return nil
}

// Maintenance are the maintenance settings of a site.
type Maintenance struct {
	Enabled  bool
	Page     string // page id; empty: the built-in page
	AllowIPs []string
}

// SetMaintenance changes the maintenance settings of an active site and
// applies them. Addresses are stored in their canonical form.
func (s *Service) SetMaintenance(ctx context.Context, actor int64, name string, m Maintenance) (store.Site, error) {
	page := int64(0)

	switch m.Page {
	case "", statuspage.BuiltinMaintenance:
		m.Page = statuspage.BuiltinMaintenance
	default:
		n, ok := parsePageID(m.Page)
		if !ok {
			return store.Site{}, ErrPageNotFound
		}

		page = n
	}

	allow, err := canonicalAllow(m.AllowIPs)
	if err != nil {
		return store.Site{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	site, err := s.Get(ctx, name)
	if err != nil {
		return store.Site{}, err
	}

	if site.State != store.SiteActive {
		return store.Site{}, ErrState
	}

	if page != 0 {
		p, err := s.st.StatusPageByID(ctx, page)
		if errors.Is(err, store.ErrNotFound) {
			return store.Site{}, ErrPageNotFound
		}

		if err != nil {
			return store.Site{}, err
		}

		if p.Kind != statuspage.KindMaintenance {
			return store.Site{}, fmt.Errorf("%w: %q is a parking page, choose a maintenance page", ErrInvalid, p.Name)
		}
	}

	if err := s.st.SetSiteMaintenance(ctx, name, m.Enabled, page, allow, s.now()); err != nil {
		return store.Site{}, err
	}

	if err := s.applyWeb(ctx); err != nil {
		if rerr := s.st.SetSiteMaintenance(context.WithoutCancel(ctx), name, site.Maintenance, site.MaintenancePage, site.MaintenanceAllow, s.now()); rerr != nil {
			s.log.ErrorContext(ctx, "restore maintenance after failed change", "site", name, "err", rerr)
		}

		return store.Site{}, err
	}

	action := "site.maintenance.off"
	if m.Enabled {
		action = "site.maintenance.on"
	}

	s.audit(ctx, actor, action, name, map[string]string{"page": m.Page, "allowIps": strings.Join(allow, " ")})

	return s.Get(ctx, name)
}

// MaintenancePageID is the page id of a site's maintenance setting.
func MaintenancePageID(site store.Site) string {
	if site.MaintenancePage == 0 {
		return statuspage.BuiltinMaintenance
	}

	return pageAgentID(site.MaintenancePage)
}

func canonicalAllow(in []string) ([]string, error) {
	out := make([]string, 0, len(in))

	for _, ip := range in {
		c, err := agentapi.CanonicalIPOrPrefix(strings.TrimSpace(ip))
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
		}

		if !slices.Contains(out, c) {
			out = append(out, c)
		}
	}

	if len(out) > agentapi.MaxAllowIPs {
		return nil, fmt.Errorf("%w: at most %d allowed addresses", ErrInvalid, agentapi.MaxAllowIPs)
	}

	return out, nil
}

// maintenanceWeb returns the maintenance block of a site for web.apply and
// makes sure the agent holds that page version. Under s.mu.
func (s *Service) maintenanceWeb(ctx context.Context, site store.Site, onAgent map[string]bool) (*agentapi.WebMaintenance, error) {
	id := MaintenancePageID(site)

	var (
		files  map[string][]byte
		digest string
	)

	if _, f, ok := builtinPage(id); ok {
		files, digest = f, agentapi.PageDigest(f)
	} else {
		p, err := s.st.StatusPageByID(ctx, site.MaintenancePage)
		if err != nil {
			return nil, err
		}

		digest = p.Digest
	}

	if !onAgent[id+"."+digest] {
		if files == nil {
			var err error
			if files, err = s.st.StatusPageFiles(ctx, site.MaintenancePage); err != nil {
				return nil, err
			}
		}

		if err := s.ag.PageUpload(ctx, id, files, digest); err != nil {
			return nil, err
		}

		onAgent[id+"."+digest] = true
	}

	return &agentapi.WebMaintenance{Page: id, Digest: digest, AllowIPs: site.MaintenanceAllow}, nil
}

// agentPages lists the page versions the agent holds, only when a site
// needs one.
func (s *Service) agentPages(ctx context.Context, sites []store.Site) (map[string]bool, error) {
	need := slices.ContainsFunc(sites, func(site store.Site) bool {
		return site.State == store.SiteActive && site.Maintenance && !site.Disabled
	})
	if !need {
		return nil, nil
	}

	list, err := s.ag.PageList(ctx)
	if err != nil {
		return nil, err
	}

	out := make(map[string]bool, len(list))
	for _, v := range list {
		out[v.ID+"."+v.Digest] = true
	}

	return out, nil
}

// PageList calls page.list.
func (a RPCAgent) PageList(ctx context.Context) ([]agentapi.PageVersion, error) {
	res, err := agentapi.PageList.Call(ctx, a.C, agentapi.PageListRequest{})

	return res.Pages, err
}

// PageUpload calls page.begin, page.write for every chunk and page.commit.
func (a RPCAgent) PageUpload(ctx context.Context, id string, files map[string][]byte, digest string) error {
	if _, err := agentapi.PageBegin.Call(ctx, a.C, agentapi.PageBeginRequest{ID: id}); err != nil {
		return err
	}

	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}

	slices.Sort(names)

	for _, n := range names {
		data := files[n]

		// An empty file is created by a write without data.
		for off := 0; off == 0 || off < len(data); off += agentapi.MaxPageChunk {
			end := min(off+agentapi.MaxPageChunk, len(data))

			req := agentapi.PageWriteRequest{ID: id, Path: n, Offset: int64(off), Data: data[off:end]}
			if _, err := agentapi.PageWrite.Call(ctx, a.C, req); err != nil {
				return err
			}
		}
	}

	_, err := agentapi.PageCommit.Call(ctx, a.C, agentapi.PageCommitRequest{ID: id, Digest: digest})

	return err
}

// PageDelete calls page.delete.
func (a RPCAgent) PageDelete(ctx context.Context, id string) error {
	_, err := agentapi.PageDelete.Call(ctx, a.C, agentapi.PageDeleteRequest{ID: id})

	return err
}
