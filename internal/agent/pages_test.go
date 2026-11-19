// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/pages"
	"github.com/maki-it/hostashi/internal/web"
)

// upload sends files as page id in chunks of at most MaxPageChunk.
func upload(t *testing.T, c *agentrpc.Client, id string, files map[string][]byte) string {
	t.Helper()

	ctx := context.Background()

	if _, err := agentapi.PageBegin.Call(ctx, c, agentapi.PageBeginRequest{ID: id}); err != nil {
		t.Fatal(err)
	}

	for name, data := range files {
		for off := 0; off < len(data); off += agentapi.MaxPageChunk {
			end := min(off+agentapi.MaxPageChunk, len(data))

			req := agentapi.PageWriteRequest{ID: id, Path: name, Offset: int64(off), Data: data[off:end]}
			if _, err := agentapi.PageWrite.Call(ctx, c, req); err != nil {
				t.Fatal(err)
			}
		}
	}

	digest := agentapi.PageDigest(files)
	if _, err := agentapi.PageCommit.Call(ctx, c, agentapi.PageCommitRequest{ID: id, Digest: digest}); err != nil {
		t.Fatal(err)
	}

	return digest
}

func TestPagesWithoutStore(t *testing.T) {
	c, _ := startSiteAgent(t)

	_, err := agentapi.PageBegin.Call(context.Background(), c, agentapi.PageBeginRequest{ID: "maint"})
	wantCode(t, err, agentrpc.CodePrecondition)
}

func TestPageUploadAndUse(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pages")
	base := filepath.Join(t.TempDir(), "web")
	c, _ := startSiteAgentCfg(t, Config{Web: &web.Reconciler{Base: base, Driver: &web.Nginx{}}, Pages: &pages.Store{Dir: dir}})
	ctx := context.Background()

	big := make([]byte, 3*agentapi.MaxPageChunk+17)
	for i := range big {
		big[i] = byte(i)
	}

	files := map[string][]byte{"index.html": []byte("<h1>Back soon</h1>"), "img/logo.png": big}
	digest := upload(t, c, "maint", files)

	if got, err := os.ReadFile(filepath.Join(dir, "maint."+digest, "img", "logo.png")); err != nil || len(got) != len(big) {
		t.Fatalf("stored file: %d bytes, %v", len(got), err)
	}

	list, err := agentapi.PageList.Call(ctx, c, agentapi.PageListRequest{})
	if err != nil || len(list.Pages) != 1 || list.Pages[0].Digest != digest {
		t.Fatalf("list %+v %v", list, err)
	}

	// A wrong digest stores nothing.
	if _, err := agentapi.PageBegin.Call(ctx, c, agentapi.PageBeginRequest{ID: "other"}); err != nil {
		t.Fatal(err)
	}

	if _, err := agentapi.PageWrite.Call(ctx, c, agentapi.PageWriteRequest{ID: "other", Path: "index.html", Data: []byte("x")}); err != nil {
		t.Fatal(err)
	}

	_, err = agentapi.PageCommit.Call(ctx, c, agentapi.PageCommitRequest{ID: "other", Digest: digest})
	wantCode(t, err, agentrpc.CodeInvalidArgument)

	// Commit without begin.
	_, err = agentapi.PageCommit.Call(ctx, c, agentapi.PageCommitRequest{ID: "nothing", Digest: digest})
	wantCode(t, err, agentrpc.CodePrecondition)

	// The server refuses paths outside the page on its own.
	raw := agentrpc.Op[rawRequest, rawResponse]{Name: "page.write"}
	for _, body := range []string{
		`{"id":"other","path":"../x.html","data":""}`,
		`{"id":"other","path":".htaccess","data":""}`,
		`{"id":"other","path":"a/b/c/d.html","data":""}`,
		`{"id":"other","path":"x.php","data":""}`,
	} {
		if _, err := raw.Call(ctx, c, rawRequest(body)); err == nil {
			t.Errorf("%s accepted", body)
		}
	}

	if _, err := agentapi.SiteCreate.Call(ctx, c, agentapi.SiteCreateRequest{User: "blog"}); err != nil {
		t.Fatal(err)
	}

	// web.apply needs the exact version.
	_, err = agentapi.WebApply.Call(ctx, c, agentapi.WebApplyRequest{Sites: []agentapi.WebSite{{
		User: "blog", Domains: []string{"blog.example.com"},
		Maintenance: &agentapi.WebMaintenance{Page: "maint", Digest: "ab" + digest[2:]},
	}}})
	wantCode(t, err, agentrpc.CodeNotFound)

	// A page that the live configuration uses cannot be deleted.
	fakeLiveGeneration(t, base, `{"sites":[{"user":"blog","domains":["blog.example.com"],"root":"/x",`+
		`"maintenance":{"page":"maint","digest":"`+digest+`","dir":"/x"}}]}`)

	_, err = agentapi.PageDelete.Call(ctx, c, agentapi.PageDeleteRequest{ID: "maint"})
	wantCode(t, err, agentrpc.CodePrecondition)

	// A new version keeps the one in use.
	files["index.html"] = []byte("<h1>Back later</h1>")
	newer := upload(t, c, "maint", files)

	for _, d := range []string{digest, newer} {
		if _, err := os.Stat(filepath.Join(dir, "maint."+d)); err != nil {
			t.Errorf("version %s: %v", d[:8], err)
		}
	}

	if _, err := agentapi.PageDelete.Call(ctx, c, agentapi.PageDeleteRequest{ID: "other"}); err != nil {
		t.Fatal(err)
	}
}

func TestMaintenanceValidation(t *testing.T) {
	c, _ := startSiteAgentCfg(t, Config{Web: &web.Reconciler{Base: filepath.Join(t.TempDir(), "web"), Driver: &web.Nginx{}}, Pages: &pages.Store{Dir: t.TempDir()}})
	raw := agentrpc.Op[rawRequest, rawResponse]{Name: "web.apply"}
	d := `"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"`

	for _, m := range []string{
		`{"page":"../x","digest":` + d + `}`,
		`{"page":"maint","digest":"abc"}`,
		`{"page":"maint","digest":` + d + `,"allowIps":["10.1.2.3/8"]}`,
		`{"page":"maint","digest":` + d + `,"allowIps":["1.2.3.4; allow all"]}`,
		`{"page":"maint","digest":` + d + `,"allowIps":["fe80::1%eth0"]}`,
		`{"page":"maint","digest":` + d + `,"allowIps":["::ffff:1.2.3.4"]}`,
	} {
		body := `{"sites":[{"user":"blog","domains":["a.example.com"],"maintenance":` + m + `}]}`
		_, err := raw.Call(context.Background(), c, rawRequest(body))
		wantCode(t, err, agentrpc.CodeInvalidArgument)
	}
}
