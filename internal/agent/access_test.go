// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"errors"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/web"
)

func TestAccessFor(t *testing.T) {
	const hash = "$2a$05$7tXviV9DiqwPre8Yge0sUu000CCwS9nkgKr.DLJbkteSq.2Ar/Hv2"

	h := &siteOps{localAddrs: func() ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("203.0.113.9")}, nil
	}}
	auth := &agentapi.WebAccess{Auth: &agentapi.WebAuth{Bypass: []string{"198.51.100.0/24"}, Users: []agentapi.WebAuthUser{{Name: "kim", Hash: hash}}}}

	var rpcErr *agentrpc.Error

	site := web.Site{User: "blog"}
	if err := h.accessFor(&site, auth); !errors.As(err, &rpcErr) || rpcErr.Code != agentrpc.CodePrecondition {
		t.Errorf("password without a web server group: %v", err)
	}

	// An IP rule needs no password files.
	ip := &agentapi.WebAccess{IPMode: agentapi.AccessIPDeny, IPs: []string{"203.0.113.9"}}
	if err := h.accessFor(&site, ip); err != nil || site.Access.IPMode != agentapi.AccessIPDeny {
		t.Errorf("IP rule: %v %+v", err, site.Access)
	}

	h.auth = &web.AuthFiles{Dir: filepath.Join(t.TempDir(), "web-auth"), UID: os.Getuid(), GID: os.Getgid()}
	h.log = slog.New(slog.DiscardHandler)

	site = web.Site{User: "blog"}
	if err := h.accessFor(&site, auth); err != nil || site.Access.Auth == nil {
		t.Fatalf("password: %v", err)
	}

	own := &agentapi.WebAccess{Auth: &agentapi.WebAuth{Bypass: []string{"203.0.113.0/24"}, Users: auth.Auth.Users}}
	if err := h.accessFor(&web.Site{User: "blog"}, own); !errors.As(err, &rpcErr) || rpcErr.Code != agentrpc.CodeInvalidArgument {
		t.Errorf("bypass with this server's address: %v", err)
	}

	d := web.Desired{Sites: []web.Site{site}}
	if err := h.writeAuthFiles(d); err != nil {
		t.Fatal(err)
	}

	h.pruneAuthFiles(t.Context(), web.Desired{})

	if es, _ := os.ReadDir(h.auth.Dir); len(es) != 0 {
		t.Errorf("left %v", es)
	}
}
