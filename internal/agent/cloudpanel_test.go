// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/cpsource"
)

func TestCloudPanelSourceThroughServer(t *testing.T) {
	ctx := context.Background()

	c0, _ := startSiteAgentCfg(t, Config{})
	if _, err := agentapi.CloudPanelSourceGet.Call(ctx, c0, agentapi.CloudPanelSourceGetRequest{}); code(err) != agentrpc.CodePrecondition {
		t.Fatalf("no source side: %v", err)
	}

	src := &cpsource.Source{Dir: filepath.Join(t.TempDir(), "cloudpanel")}
	c, _ := startSiteAgentCfg(t, Config{CloudPanel: src})

	if _, err := agentapi.CloudPanelHostKey.Call(ctx, c, agentapi.CloudPanelHostKeyRequest{Key: "ssh-ed25519 AAAA"}); code(err) != agentrpc.CodeInvalidArgument {
		t.Fatalf("bad key: %v", err)
	}

	if _, err := agentapi.CloudPanelSourceSet.Call(ctx, c, agentapi.CloudPanelSourceSetRequest{Source: agentapi.CloudPanelSource{Host: "bad host", Port: 22}}); code(err) != agentrpc.CodeInvalidArgument {
		t.Fatalf("bad host: %v", err)
	}

	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	k, _ := ssh.NewPublicKey(pub)
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k)))

	if _, err := agentapi.CloudPanelHostKey.Call(ctx, c, agentapi.CloudPanelHostKeyRequest{Key: line}); code(err) != agentrpc.CodePrecondition {
		t.Fatalf("host key before source: %v", err)
	}

	info, err := agentapi.CloudPanelSourceSet.Call(ctx, c, agentapi.CloudPanelSourceSetRequest{Source: agentapi.CloudPanelSource{Host: "203.0.113.7", Port: 22}})
	if err != nil || info.Source == nil || info.AuthorizedKeysLine == "" || strings.Contains(info.PublicKey, "PRIVATE") {
		t.Fatalf("set: %+v %v", info, err)
	}

	info, err = agentapi.CloudPanelHostKey.Call(ctx, c, agentapi.CloudPanelHostKeyRequest{Key: line})
	if err != nil || !info.Ready {
		t.Fatalf("host key: %+v %v", info, err)
	}

	info, err = agentapi.CloudPanelSourceDelete.Call(ctx, c, agentapi.CloudPanelSourceDeleteRequest{})
	if err != nil || info.Source != nil {
		t.Fatalf("delete: %+v %v", info, err)
	}
}
