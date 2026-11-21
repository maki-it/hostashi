// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
)

// External database servers in the web database tool (ADR-0041 addendum
// 1). The panel sends the full list of servers with their resolved
// addresses; the agent writes a hosts file, the servers' own CAs and a
// drop-in that lets the tool's pool reach exactly those addresses, and
// restarts the pool when any of it changed. A ticket for an external
// database names its server; the panel created the temporary user there.

// DBToolUnits restarts the tool's pool; resticctl.Units implements it.
type DBToolUnits interface {
	Reload(ctx context.Context) error
	RestartDBTool(ctx context.Context) error
}

const (
	dbToolServersFile = "servers.json"
	dbToolHostsFile   = "hosts"
	dbToolDropIn      = "servers.conf"
)

var dbToolCARe = regexp.MustCompile(`^ca-[0-9]+\.pem$`)

func (t *dbTool) serverDir() string {
	if t.serversDir != "" {
		return t.serversDir
	}

	return agentapi.DBToolServerDir
}

func (t *dbTool) dropInPath() string {
	dir := t.unitDir
	if dir == "" {
		dir = "/etc/systemd/system"
	}

	return filepath.Join(dir, agentapi.DBToolUnit+".d", dbToolDropIn)
}

// caFile is where the CA of server id goes, below serverDir.
func (t *dbTool) caFile(id int64) string {
	return filepath.Join(t.serverDir(), filepath.Base(agentapi.DBToolCAFile(id)))
}

func (t *dbTool) setServers(ctx context.Context, _ agentrpc.Caller, req agentapi.DBToolServersRequest) (agentapi.DBToolServersResponse, error) {
	if t.units == nil {
		return agentapi.DBToolServersResponse{}, errNoDBTool
	}

	if _, _, err := toolIdentity(); err != nil {
		return agentapi.DBToolServersResponse{}, errNoDBTool
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	servers := append([]agentapi.DBToolServer(nil), req.Servers...)
	sort.Slice(servers, func(i, j int) bool { return servers[i].ID < servers[j].ID })

	dir := t.serverDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return agentapi.DBToolServersResponse{}, fmt.Errorf("database tool: %w", err)
	}

	changed := false
	keepCA := map[string]bool{}

	for _, srv := range servers {
		if srv.CAPEM == "" {
			continue
		}

		p := t.caFile(srv.ID)
		keepCA[filepath.Base(p)] = true

		c, err := writeIfChanged(p, []byte(srv.CAPEM))
		if err != nil {
			return agentapi.DBToolServersResponse{}, err
		}

		changed = changed || c
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return agentapi.DBToolServersResponse{}, fmt.Errorf("database tool: %w", err)
	}

	for _, e := range entries {
		if dbToolCARe.MatchString(e.Name()) && !keepCA[e.Name()] {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
				return agentapi.DBToolServersResponse{}, fmt.Errorf("database tool: %w", err)
			}

			changed = true
		}
	}

	// The list without the CAs, for db.tool.remote to check a ticket's
	// server against.
	list := make([]agentapi.DBToolServer, 0, len(servers))
	for _, srv := range servers {
		srv.CAPEM = ""
		list = append(list, srv)
	}

	raw, err := json.Marshal(list)
	if err != nil {
		return agentapi.DBToolServersResponse{}, err
	}

	if _, err := writeIfChanged(filepath.Join(dir, dbToolServersFile), append(raw, '\n')); err != nil {
		return agentapi.DBToolServersResponse{}, err
	}

	c, err := writeIfChanged(filepath.Join(dir, dbToolHostsFile), []byte(toolHosts(servers)))
	if err != nil {
		return agentapi.DBToolServersResponse{}, err
	}

	changed = changed || c

	c, err = t.writeDropIn(servers)
	if err != nil {
		return agentapi.DBToolServersResponse{}, err
	}

	changed = changed || c

	if !changed {
		return agentapi.DBToolServersResponse{}, nil
	}

	if err := t.units.Reload(ctx); err != nil {
		return agentapi.DBToolServersResponse{}, err
	}

	if err := t.units.RestartDBTool(ctx); err != nil {
		return agentapi.DBToolServersResponse{}, err
	}

	t.log.InfoContext(ctx, "database tool: external servers changed, pool restarted", "servers", len(servers))

	return agentapi.DBToolServersResponse{Restarted: true}, nil
}

// writeDropIn writes the drop-in, or removes it without servers: then the
// pool has no network at all, as the unit itself says.
func (t *dbTool) writeDropIn(servers []agentapi.DBToolServer) (bool, error) {
	p := t.dropInPath()

	if len(servers) == 0 {
		err := os.Remove(p)
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}

		if err != nil {
			return false, fmt.Errorf("database tool: %w", err)
		}

		return true, nil
	}

	var addrs []string

	for _, srv := range servers {
		addrs = append(addrs, srv.Addrs...)
	}

	content := "# Written by the Hostashi agent: the external database servers (ADR-0041 addendum 1).\n[Service]\n" +
		"RestrictAddressFamilies=AF_INET AF_INET6\n" +
		"IPAddressAllow=" + strings.Join(addrs, " ") + "\n" +
		"BindReadOnlyPaths=" + filepath.Join(t.serverDir(), dbToolHostsFile) + ":/etc/hosts\n"

	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return false, fmt.Errorf("database tool: %w", err)
	}

	return writeIfChanged(p, []byte(content))
}

// toolHosts maps the servers' names to their addresses. A server given by
// address needs no line.
func toolHosts(servers []agentapi.DBToolServer) string {
	var b strings.Builder

	b.WriteString("# Written by the Hostashi agent for the web database tool.\n127.0.0.1 localhost\n::1 localhost\n")

	for _, srv := range servers {
		if _, err := netip.ParseAddr(srv.Host); err == nil {
			continue
		}

		for _, a := range srv.Addrs {
			b.WriteString(a + " " + srv.Host + "\n")
		}
	}

	return b.String()
}

// writeIfChanged writes a world-readable file atomically unless it holds
// content already.
func writeIfChanged(p string, content []byte) (bool, error) {
	if old, err := os.ReadFile(p); err == nil && bytes.Equal(old, content) {
		return false, nil
	}

	tmp := p + ".tmp"
	_ = os.Remove(tmp)

	if err := os.WriteFile(tmp, content, 0o644); err != nil { //nolint:gosec // read by the tool's pool, holds no secret
		return false, fmt.Errorf("database tool: %w", err)
	}

	// The agent runs with UMask=0077; the pool's user must read the CAs
	// and the hosts file.
	if err := os.Chmod(tmp, 0o644); err != nil {
		_ = os.Remove(tmp)

		return false, fmt.Errorf("database tool: %w", err)
	}

	if err := os.Rename(tmp, p); err != nil {
		_ = os.Remove(tmp)

		return false, fmt.Errorf("database tool: %w", err)
	}

	return true, nil
}

// remote writes the ticket of a database on an external server. The
// server must be in the list the tool may reach.
func (t *dbTool) remote(ctx context.Context, _ agentrpc.Caller, req agentapi.DBToolRemoteRequest) (agentapi.DBToolOpenResponse, error) {
	uid, gid, err := toolIdentity()
	if err != nil {
		return agentapi.DBToolOpenResponse{}, errNoDBTool
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	srv, err := t.knownServer(req.ServerID)
	if err != nil {
		return agentapi.DBToolOpenResponse{}, err
	}

	if srv.Host != req.Host {
		return agentapi.DBToolOpenResponse{}, &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "the server's address changed: send the server list again"}
	}

	ticket := agentapi.DBToolTicket{
		Database: req.Database, User: req.User, Password: req.Password,
		Expires:       t.now().Add(time.Duration(req.Minutes) * time.Minute).UTC(),
		TicketExpires: t.now().Add(agentapi.DBToolTicketTTL).UTC(),
		Host:          req.Host, Port: req.Port, TLS: req.TLS,
	}

	if req.Engine == agentapi.DBToolEnginePostgreSQL {
		ticket.Driver = agentapi.DBToolDriverPgSQL
	}

	if req.TLS == "verify-ca" {
		ticket.CAFile = agentapi.DBToolCAFile(req.ServerID)

		if _, err := os.Stat(t.caFile(req.ServerID)); err != nil {
			return agentapi.DBToolOpenResponse{}, &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "the server's CA is missing: send the server list again"}
		}
	}

	if err := t.prepareDir(uid, gid); err != nil {
		return agentapi.DBToolOpenResponse{}, err
	}

	if err := t.writeSession(req.User, ticket.Expires); err != nil {
		return agentapi.DBToolOpenResponse{}, err
	}

	id := newTicketID()
	if err := t.writeTicket(id, ticket, uid, gid); err != nil {
		t.removeSession(req.User)

		return agentapi.DBToolOpenResponse{}, err
	}

	// An unused ticket goes after its TTL. The panel drops the user on
	// the external server when the session ends.
	bg := context.WithoutCancel(ctx)

	t.after(agentapi.DBToolTicketTTL, func() {
		err := os.Remove(filepath.Join(t.dir, id))
		if err == nil {
			t.removeSession(req.User)
		} else if !errors.Is(err, os.ErrNotExist) {
			t.log.ErrorContext(bg, "database tool: remove unused ticket", "err", err)
		}
	})

	t.log.InfoContext(ctx, "database tool session", "database", req.Database, "user", req.User, "server", req.Host, "expires", ticket.Expires)

	return agentapi.DBToolOpenResponse{Ticket: id, User: req.User, Expires: ticket.Expires}, nil
}

func (t *dbTool) knownServer(id int64) (agentapi.DBToolServer, error) {
	raw, err := os.ReadFile(filepath.Join(t.serverDir(), dbToolServersFile))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return agentapi.DBToolServer{}, fmt.Errorf("database tool: %w", err)
	}

	var list []agentapi.DBToolServer

	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &list); err != nil {
			return agentapi.DBToolServer{}, fmt.Errorf("database tool: server list: %w", err)
		}
	}

	for _, srv := range list {
		if srv.ID == id {
			return srv, nil
		}
	}

	return agentapi.DBToolServer{}, &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "the tool does not know this server: send the server list first"}
}
