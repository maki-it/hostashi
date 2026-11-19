// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"errors"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentrpc"
)

func TestSnippets(t *testing.T) {
	s, ag, st := newService(t)
	ctx := t.Context()

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "blog", PHP: "8.4", Domains: []string{"blog.example.com"}}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "shop", Domains: []string{"shop.example.com"}}); err != nil {
		t.Fatal(err)
	}

	sn, err := s.SetSnippets(ctx, 1, "blog", "add_header X-A b;", "expires 1h;")
	if err != nil || sn.Server != "add_header X-A b;" || sn.Root != "expires 1h;" || sn.UpdatedBy != 1 {
		t.Fatalf("set: %+v %v", sn, err)
	}

	sites := ag.web.Sites
	if len(sites) != 2 || sites[0].Snippets == nil || sites[0].Snippets.Root != "expires 1h;" || sites[1].Snippets != nil {
		t.Fatalf("web state: %+v", sites)
	}

	// Rejected: nothing stored, nothing applied, every line named.
	applies := ag.applies

	_, err = s.SetSnippets(ctx, 1, "blog", "add_header X-A b;\naccess_log /etc/x;\nlisten 81;", "")

	var se *SnippetError
	if !errors.As(err, &se) || !errors.Is(err, ErrInvalid) || len(se.Problems.Server) != 2 || se.Problems.Server[0].Line != 2 {
		t.Fatalf("rejected: %v %+v", err, se)
	}

	if ag.applies != applies {
		t.Fatal("rejected snippets were applied")
	}

	if got, _ := s.Snippets(ctx, "blog"); got.Root != "expires 1h;" {
		t.Fatalf("rejected snippets were stored: %+v", got)
	}

	// The site's own PHP socket only for a PHP site.
	if _, err := s.SetSnippets(ctx, 1, "shop", "location /x { fastcgi_pass unix:/run/hostashi-php/shop.sock; }", ""); !errors.As(err, &se) {
		t.Fatalf("fastcgi on a static site: %v", err)
	}

	// The agent fails the apply: the old snippets stay.
	ag.webErr = &agentrpc.Error{Code: agentrpc.CodeInternal, Message: "nginx down"}

	if _, err := s.SetSnippets(ctx, 1, "blog", "add_header X-B c;", ""); err == nil {
		t.Fatal("set succeeded although the agent refused")
	}

	ag.webErr = nil

	if got, _ := s.Snippets(ctx, "blog"); got.Server != "add_header X-A b;" || got.Root != "expires 1h;" {
		t.Fatalf("failed change was stored: %+v", got)
	}

	if _, err := s.SetSnippets(ctx, 1, "blog", strings.Repeat("#", 64<<10+1), ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("too big: %v", err)
	}

	if _, err := s.SetSnippets(ctx, 1, "nope", "", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown site: %v", err)
	}

	// Clearing them removes them from the web state.
	if sn, err := s.SetSnippets(ctx, 1, "blog", "", ""); err != nil || !sn.Empty() || ag.web.Sites[0].Snippets != nil {
		t.Fatalf("clear: %+v %v %+v", sn, err, ag.web.Sites[0])
	}

	var n int
	if err := st.RawCount(ctx, `SELECT count(*) FROM audit_log WHERE action = ? AND detail NOT LIKE '%add_header%'`, "site.snippets.set", &n); err != nil || n != 2 {
		t.Fatalf("audit entries without content: %d %v", n, err)
	}
}
