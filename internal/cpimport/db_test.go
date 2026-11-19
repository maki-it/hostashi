// SPDX-License-Identifier: AGPL-3.0-or-later

package cpimport

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/mariadbctl"
	"github.com/maki-it/hostashi/internal/sites"
)

// fakeMariaDB answers every connection with "dump of " and the first line
// the client sent.
func fakeMariaDB(t *testing.T) string {
	t.Helper()

	l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	must(t, err)
	t.Cleanup(func() { _ = l.Close() })

	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}

			go func() {
				defer c.Close()

				buf := make([]byte, 256)
				n, _ := c.Read(buf)
				_, _ = c.Write([]byte("dump of " + strings.TrimSpace(string(buf[:n])) + "\n"))
			}()
		}
	}()

	return l.Addr().String()
}

func siteFor(t *testing.T) sites.Home {
	t.Helper()

	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}

	home := filepath.Join(openTemp(t), "home")
	must(t, os.Mkdir(home, 0o750))
	must(t, os.Chown(home, 65534, 65534))

	return sites.Home{Path: home, UID: 65534, GID: 65534}
}

func TestDumpDatabases(t *testing.T) {
	home := siteFor(t)
	addr := fakeMariaDB(t)

	var dialed []string

	dial := func(network, a string) (net.Conn, error) {
		dialed = append(dialed, network+" "+a)

		return (&net.Dialer{}).DialContext(t.Context(), "tcp", addr)
	}

	var sockDir string

	export := func(ctx context.Context, r mariadbctl.Run, out io.Writer) error {
		if r.User != agentapi.CloudPanelDBUser || r.Password != "pw" || r.UID != 65534 || r.GID != 65534 {
			t.Errorf("run %+v", r)
		}

		sockDir = filepath.Dir(r.Socket)

		// Only the site user may reach the tunnel.
		var st syscall.Stat_t
		must(t, syscall.Stat(sockDir, &st))

		if st.Uid != 65534 || st.Mode&0o077 != 0 {
			t.Errorf("tunnel dir uid %d mode %o", st.Uid, st.Mode)
		}

		c, err := (&net.Dialer{}).DialContext(ctx, "unix", r.Socket)
		if err != nil {
			return err
		}
		defer c.Close()

		if _, err := c.Write([]byte(r.Database + "\n")); err != nil {
			return err
		}

		_, err = io.Copy(out, c)

		return err
	}

	got, err := dumpDatabases(t.Context(), dumpConfig{
		Home: home, Password: "pw", Dial: dial, Export: export, TempDir: openTemp(t),
		Databases: []agentapi.CloudPanelDBMap{{From: "shop", To: "site_shop"}, {From: "blog", To: "site_blog"}},
	})
	must(t, err)

	if len(got) != 2 || !strings.HasPrefix(got[0], agentapi.CloudPanelImportDBDir+"/site_shop-") || !strings.HasSuffix(got[1], ".sql") {
		t.Fatalf("dumps %v", got)
	}

	for i, want := range []string{"dump of shop\n", "dump of blog\n"} {
		p := filepath.Join(home.Path, got[i])

		b, err := os.ReadFile(p)
		must(t, err)

		var st syscall.Stat_t
		must(t, syscall.Stat(p, &st))

		if string(b) != want || st.Uid != 65534 || st.Mode&0o777 != 0o600 {
			t.Errorf("%s: %q uid %d mode %o", got[i], b, st.Uid, st.Mode)
		}
	}

	if len(dialed) != 2 || dialed[0] != "tcp "+remoteDB {
		t.Errorf("dialed %v", dialed)
	}

	if _, err := os.Stat(sockDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("tunnel dir left behind: %v", err)
	}

	var st syscall.Stat_t
	must(t, syscall.Stat(filepath.Join(home.Path, agentapi.CloudPanelImportDBDir), &st))

	if st.Uid != 65534 || st.Mode&0o777 != 0o700 {
		t.Errorf("dump dir uid %d mode %o", st.Uid, st.Mode)
	}
}

func TestDumpDatabasesFailure(t *testing.T) {
	home := siteFor(t)
	boom := errors.New("boom")

	export := func(_ context.Context, r mariadbctl.Run, out io.Writer) error {
		if r.Database == "second" {
			_, _ = out.Write([]byte("partial"))

			return boom
		}

		_, err := out.Write([]byte("ok"))

		return err
	}

	got, err := dumpDatabases(t.Context(), dumpConfig{
		Home: home, Password: "pw", Export: export, TempDir: openTemp(t),
		Dial:      func(string, string) (net.Conn, error) { return nil, errors.New("unused") },
		Databases: []agentapi.CloudPanelDBMap{{From: "first", To: "a"}, {From: "second", To: "b"}},
	})
	if !errors.Is(err, boom) || len(got) != 1 {
		t.Fatalf("%v %v", got, err)
	}

	entries, err := os.ReadDir(filepath.Join(home.Path, agentapi.CloudPanelImportDBDir))
	must(t, err)

	// The failed dump is removed; the finished one stays and is reported.
	if len(entries) != 1 || agentapi.CloudPanelImportDBDir+"/"+entries[0].Name() != got[0] {
		t.Fatalf("left %v", entries)
	}
}

func TestDumpDirRefusesSymlink(t *testing.T) {
	home := siteFor(t)
	must(t, os.Symlink("/etc", filepath.Join(home.Path, agentapi.CloudPanelImportDBDir)))

	_, err := dumpDatabases(t.Context(), dumpConfig{
		Home: home, Databases: []agentapi.CloudPanelDBMap{{From: "x", To: "y"}}, TempDir: openTemp(t),
		Export: func(context.Context, mariadbctl.Run, io.Writer) error { t.Error("export ran"); return nil },
	})
	if err == nil {
		t.Fatal("symlinked dump dir accepted")
	}
}
