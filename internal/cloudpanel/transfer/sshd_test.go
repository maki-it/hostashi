// SPDX-License-Identifier: AGPL-3.0-or-later

package transfer_test

import (
	"bufio"
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/sys/unix"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/cloudpanel/transfer"
	"github.com/maki-it/hostashi/internal/cpsource"
)

// TestLockedKeyAgainstOpenSSH checks ADR-0026 against a real sshd: the
// authorized_keys line that the panel shows must allow a read-only SFTP
// session and a tunnel to 127.0.0.1:3306, and nothing else. It runs as root
// in the CI container (see .github/workflows/go.yml), where sshd listens on
// HOSTASHI_TEST_SSHD_PORT and reads root's authorized_keys.
//
// The script prepares /home/site/htdocs owned by the user site, with one
// file in it owned by root.
func TestLockedKeyAgainstOpenSSH(t *testing.T) {
	portEnv := os.Getenv("HOSTASHI_TEST_SSHD_PORT")
	if portEnv == "" {
		t.Skip("HOSTASHI_TEST_SSHD_PORT not set (container test)")
	}

	port, err := strconv.Atoi(portEnv)
	must(t, err)

	dir := filepath.Join(t.TempDir(), "cloudpanel")
	must(t, os.Mkdir(dir, 0o700))

	src := &cpsource.Source{Dir: dir}

	info, err := src.Set(agentapi.CloudPanelSource{Host: "127.0.0.1", Port: port})
	must(t, err)

	must(t, os.WriteFile(os.Getenv("HOSTASHI_TEST_SSHD_AUTHORIZED_KEYS"), []byte(info.AuthorizedKeysLine+"\n"), 0o600))

	hostKey, err := os.ReadFile(os.Getenv("HOSTASHI_TEST_SSHD_HOST_KEY"))
	must(t, err)

	_, err = src.SetHostKey(strings.TrimSpace(string(hostKey)))
	must(t, err)

	s, cfg, err := src.ClientConfig()
	must(t, err)

	conn, err := ssh.Dial("tcp", s.Addr(), cfg)
	must(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	t.Run("copy", func(t *testing.T) {
		c, err := sftp.NewClient(conn)
		must(t, err)

		defer c.Close()

		dst := t.TempDir()

		fd, err := unix.Open(dst, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		must(t, err)

		defer unix.Close(fd)

		st, err := transfer.Copy(context.Background(), transfer.SFTP(c), "/home/site/htdocs", fd, transfer.Limits{})
		must(t, err)

		if st.Files != 2 || st.Skipped != 1 || !strings.HasPrefix(st.SkippedPaths[0], "root-owned:") {
			t.Fatalf("%+v", st)
		}

		if b, err := os.ReadFile(filepath.Join(dst, "sub/page.html")); err != nil || string(b) != "page\n" {
			t.Fatalf("%q %v", b, err)
		}
	})

	t.Run("read-only", func(t *testing.T) {
		c, err := sftp.NewClient(conn)
		must(t, err)

		defer c.Close()

		if f, err := c.Create("/home/site/htdocs/new"); err == nil {
			_ = f.Close()

			t.Fatal("created a file")
		}

		if err := c.Remove("/home/site/htdocs/index.php"); err == nil {
			t.Fatal("removed a file")
		}

		if err := c.Mkdir("/root/dir"); err == nil {
			t.Fatal("made a directory")
		}

		if err := c.Chmod("/home/site/htdocs/index.php", 0o777); err == nil {
			t.Fatal("changed a mode")
		}

		if _, err := os.Stat("/home/site/htdocs/new"); err == nil {
			t.Fatal("the file exists")
		}
	})

	t.Run("no command", func(t *testing.T) {
		sess, err := conn.NewSession()
		must(t, err)

		defer sess.Close()

		if err := sess.RequestPty("xterm", 24, 80, ssh.TerminalModes{}); err == nil {
			t.Fatal("got a terminal")
		}

		// The forced command replaces "id": sftp-server reads its closed
		// stdin and ends without output.
		out, _ := sess.CombinedOutput("id; touch /root/ran")
		if strings.Contains(string(out), "uid=") {
			t.Fatalf("a command ran: %q", out)
		}

		if _, err := os.Stat("/root/ran"); err == nil {
			t.Fatal("a command ran")
		}
	})

	t.Run("tunnel only to 3306", func(t *testing.T) {
		db := listen(t, "127.0.0.1:3306")
		other := listen(t, "127.0.0.1:3307")

		go serve(db, "db\n")
		go serve(other, "other\n")

		c, err := conn.Dial("tcp", "127.0.0.1:3306")
		must(t, err)

		line, err := bufio.NewReader(c).ReadString('\n')
		_ = c.Close()

		if err != nil || line != "db\n" {
			t.Fatalf("tunnel to 3306: %q %v", line, err)
		}

		for _, addr := range []string{"127.0.0.1:3307", "127.0.0.1:" + portEnv, "localhost:3306"} {
			if c, err := conn.Dial("tcp", addr); err == nil {
				_ = c.Close()

				t.Errorf("tunnel to %s opened", addr)
			}
		}
	})

	t.Run("no listener on the old server", func(t *testing.T) {
		// Only 127.0.0.1:1 is permitted (see cpsource.AuthorizedKeysLine).
		for _, addr := range []string{"127.0.0.1:0", "127.0.0.1:2", "0.0.0.0:8080"} {
			if l, err := conn.Listen("tcp", addr); err == nil {
				_ = l.Close()

				t.Errorf("opened a remote listener on %s", addr)
			}
		}
	})

	t.Run("no agent forwarding", func(t *testing.T) {
		sess, err := conn.NewSession()
		must(t, err)

		defer sess.Close()

		ok, err := sess.SendRequest("auth-agent-req@openssh.com", true, nil)
		if err == nil && ok {
			t.Fatal("agent forwarding accepted")
		}
	})
}

func listen(t *testing.T, addr string) net.Listener {
	t.Helper()

	l, err := net.Listen("tcp", addr)
	must(t, err)
	t.Cleanup(func() { _ = l.Close() })

	return l
}

func serve(l net.Listener, reply string) {
	for {
		c, err := l.Accept()
		if err != nil {
			return
		}

		_ = c.SetDeadline(time.Now().Add(5 * time.Second))
		_, _ = io.WriteString(c, reply)
		_ = c.Close()
	}
}

func must(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatal(err)
	}
}
