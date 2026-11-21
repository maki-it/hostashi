// SPDX-License-Identifier: AGPL-3.0-or-later

package resticctl

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/dbserver"
)

func TestWriteDumpOptions(t *testing.T) {
	dir := t.TempDir()
	cfg := dbserver.Config{Host: "10.0.0.5", Port: 3307, AdminUser: "adm", AdminPassword: `p"w\#x`, TLS: dbserver.TLSNone}

	opt, err := WriteDumpOptions(dir, cfg)
	if err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(opt)
	if err != nil {
		t.Fatal(err)
	}

	fi, _ := os.Stat(opt)
	if fi.Mode().Perm() != 0o600 || !strings.Contains(string(raw), `password="p\"w\\#x"`) || !strings.Contains(string(raw), "port=3307") {
		t.Fatalf("%v\n%s", fi.Mode(), raw)
	}

	if _, err := os.Stat(filepath.Join(dir, "ca.pem")); err == nil {
		t.Fatal("CA file without verify-ca")
	}

	args := RemoteDumpArgs(opt, "shop_wp")
	if args[0] != MariaDBDump || args[1] != "--defaults-extra-file="+opt || args[len(args)-2] != "--" || args[len(args)-1] != "shop_wp" {
		t.Fatalf("args %q", args)
	}

	cfg.Host = "bad host"
	if _, err := WriteDumpOptions(t.TempDir(), cfg); err == nil {
		t.Fatal("bad host accepted")
	}
}

func TestBackupRemoteDatabaseChecks(t *testing.T) {
	r := &Repo{Repository: "/repo", PasswordFile: "/pw", CacheDir: "/cache", Host: "h", TempDir: t.TempDir()}
	cfg := dbserver.Config{Host: "10.0.0.5", Port: 3306, AdminUser: "adm", AdminPassword: "pw", TLS: dbserver.TLSNone}

	if _, err := r.BackupRemoteDatabase(t.Context(), []string{"site:a", "kind:db"}, "a`b", cfg); err == nil {
		t.Fatal("bad database accepted")
	}

	if _, err := r.BackupRemoteDatabase(t.Context(), []string{"bad tag"}, "shop", cfg); err == nil {
		t.Fatal("bad tag accepted")
	}
}

// TestRemoteDumpRefusesLocalInfile runs the real mariadb-dump against a
// fake server that answers the first query with a LOAD DATA LOCAL request
// for a secret file. mariadb-dump runs as root in the backup, so it must
// never send the file. Needs HOSTASHI_TEST_MARIADB_DUMP=1 and mariadb-dump
// (CI: the restic job).
func TestRemoteDumpRefusesLocalInfile(t *testing.T) {
	if os.Getenv("HOSTASHI_TEST_MARIADB_DUMP") != "1" {
		t.Skip("HOSTASHI_TEST_MARIADB_DUMP not set")
	}

	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("TOP-SECRET-CONTENT"), 0o600); err != nil {
		t.Fatal(err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	defer ln.Close()

	got := make(chan []byte, 1)

	go rogueServer(t, ln, secret, got)

	port := ln.Addr().(*net.TCPAddr).Port //nolint:forcetypeassert // a TCP listener
	cfg := dbserver.Config{Host: "127.0.0.1", Port: port, AdminUser: "adm", AdminPassword: "pw", TLS: dbserver.TLSNone}

	opt, err := WriteDumpOptions(t.TempDir(), cfg)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	args := RemoteDumpArgs(opt, "shop")
	out, _ := exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput()

	select {
	case b := <-got:
		if bytes.Contains(b, []byte("TOP-SECRET")) {
			t.Fatalf("mariadb-dump sent the local file: %q", b)
		}

		t.Logf("client answered the LOCAL INFILE request with %d bytes; output: %s", len(b), out)
	case <-ctx.Done():
		t.Fatalf("the fake server got no answer; output: %s", out)
	}
}

// rogueServer speaks just enough of the protocol: handshake without TLS,
// OK for any login, then a LOCAL INFILE request for the first query.
func rogueServer(t *testing.T, ln net.Listener, file string, got chan<- []byte) {
	t.Helper()

	c, err := ln.Accept()
	if err != nil {
		return
	}

	defer c.Close()

	_ = c.SetDeadline(time.Now().Add(15 * time.Second))

	const (
		capLocalFiles = 0x00000080
		capProtocol41 = 0x00000200
		capSecure     = 0x00008000
		capPlugin     = 0x00080000
	)

	caps := uint32(0x0000f7ff) | capLocalFiles | capProtocol41 | capSecure | capPlugin
	caps &^= 0x00000800 // no TLS

	var hs bytes.Buffer

	_ = hs.WriteByte(10)
	_, _ = hs.WriteString("8.0.99-fake\x00")
	_ = binary.Write(&hs, binary.LittleEndian, uint32(1))
	_, _ = hs.WriteString("abcdefgh\x00")
	_ = binary.Write(&hs, binary.LittleEndian, uint16(caps))
	_ = hs.WriteByte(33)
	_ = binary.Write(&hs, binary.LittleEndian, uint16(2))
	_ = binary.Write(&hs, binary.LittleEndian, uint16(caps>>16))
	_ = hs.WriteByte(21)
	_, _ = hs.Write(make([]byte, 10))
	_, _ = hs.WriteString("ijklmnopqrst\x00")
	_, _ = hs.WriteString("mysql_native_password\x00")

	write := func(seq byte, p []byte) {
		h := []byte{byte(len(p)), byte(len(p) >> 8), byte(len(p) >> 16), seq}
		_, _ = c.Write(append(h, p...))
	}

	read := func() (byte, []byte, error) {
		h := make([]byte, 4)
		if _, err := io.ReadFull(c, h); err != nil {
			return 0, nil, err
		}

		p := make([]byte, int(h[0])|int(h[1])<<8|int(h[2])<<16)
		_, err := io.ReadFull(c, p)

		return h[3], p, err
	}

	write(0, hs.Bytes())

	seq, _, err := read()
	if err != nil {
		return
	}

	write(seq+1, []byte{0, 0, 0, 2, 0, 0, 0})

	seq, _, err = read()
	if err != nil {
		return
	}

	write(seq+1, append([]byte{0xfb}, file...))

	var all []byte

	for {
		_, p, err := read()
		all = append(all, p...)

		if err != nil || len(p) == 0 {
			break
		}
	}

	got <- all
}
