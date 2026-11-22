// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/mariadb"
)

// recordingDB accepts every statement, or fails all with err.
type recordingDB struct {
	mu    sync.Mutex
	stmts []string
	err   error
}

func (r *recordingDB) ExecContext(_ context.Context, q string, _ ...any) (sql.Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.stmts = append(r.stmts, q)

	return nil, r.err
}

func newDBManager(t *testing.T, db mariadb.Execer) *mariadb.Manager {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "db")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	m, err := mariadb.New(dir, db, mariadb.Options{OwnerUID: uint32(os.Getuid())})
	if err != nil {
		t.Fatal(err)
	}

	return m
}

func TestDBOperationsThroughServer(t *testing.T) {
	db := &recordingDB{}
	c, _ := startSiteAgentCfg(t, Config{DB: newDBManager(t, db)})
	ctx := context.Background()

	if _, err := agentapi.SiteCreate.Call(ctx, c, agentapi.SiteCreateRequest{User: "shop"}); err != nil {
		t.Fatal(err)
	}

	_, err := agentapi.DBCreate.Call(ctx, c, agentapi.DBCreateRequest{Site: "blog", Name: "blog"})
	wantCode(t, err, agentrpc.CodeNotFound)

	info, err := agentapi.DBCreate.Call(ctx, c, agentapi.DBCreateRequest{Site: "shop", Name: "shop_wp"})
	if err != nil || info.Name != "shop_wp" || info.Site != "shop" || info.Users == nil {
		t.Fatalf("create = %+v, %v", info, err)
	}

	_, err = agentapi.DBCreate.Call(ctx, c, agentapi.DBCreateRequest{Site: "shop", Name: "shop_wp"})
	wantCode(t, err, agentrpc.CodeConflict)

	if _, err := agentapi.DBUserCreate.Call(ctx, c, agentapi.DBUserCreateRequest{Database: "shop_wp", User: "shop_wp", Password: "Secret-pass_12345"}); err != nil {
		t.Fatal(err)
	}

	list, err := agentapi.DBList.Call(ctx, c, agentapi.DBListRequest{})
	if err != nil || len(list.Databases) != 1 || len(list.Databases[0].Users) != 1 {
		t.Fatalf("list = %+v, %v", list, err)
	}

	// A site with a database cannot be deleted.
	_, err = agentapi.SiteDelete.Call(ctx, c, agentapi.SiteDeleteRequest{User: "shop"})
	wantCode(t, err, agentrpc.CodePrecondition)

	if _, err := agentapi.DBUserPassword.Call(ctx, c, agentapi.DBUserPasswordRequest{User: "shop_wp", Password: "Other-pass_123456"}); err != nil {
		t.Fatal(err)
	}

	_, err = agentapi.DBUserDelete.Call(ctx, c, agentapi.DBUserDeleteRequest{User: "nobody1"})
	wantCode(t, err, agentrpc.CodeNotFound)

	if _, err := agentapi.DBDelete.Call(ctx, c, agentapi.DBDeleteRequest{Name: "shop_wp"}); err != nil {
		t.Fatal(err)
	}

	if _, err := agentapi.SiteDelete.Call(ctx, c, agentapi.SiteDeleteRequest{User: "shop"}); err != nil {
		t.Fatal(err)
	}
}

func TestDBOperationsRejectBadInput(t *testing.T) {
	db := &recordingDB{}
	c, _ := startSiteAgentCfg(t, Config{DB: newDBManager(t, db)})
	ctx := context.Background()

	bad := []agentapi.DBUserCreateRequest{
		{Database: "shop", User: "u1", Password: "short"},
		{Database: "shop", User: "u1", Password: "has'quote-1234567"},
		{Database: "mysql", User: "u1", Password: "Secret-pass_12345"},
		{Database: "shop", User: "root", Password: "Secret-pass_12345"},
		{Database: "shop", User: "U1", Password: "Secret-pass_12345"},
		{Database: "shop", User: strings.Repeat("a", 33), Password: "Secret-pass_12345"},
	}
	for _, req := range bad {
		_, err := agentapi.DBUserCreate.Call(ctx, c, req)
		wantCode(t, err, agentrpc.CodeInvalidArgument)
	}

	_, err := agentapi.DBCreate.Call(ctx, c, agentapi.DBCreateRequest{Site: "shop", Name: "a-b"})
	wantCode(t, err, agentrpc.CodeInvalidArgument)

	if len(db.stmts) != 0 {
		t.Fatalf("statements reached the server: %q", db.stmts)
	}
}

func TestDBUnavailable(t *testing.T) {
	t.Run("no manager", func(t *testing.T) {
		c, _ := startSiteAgentCfg(t, Config{})
		ctx := context.Background()

		_, err := agentapi.DBDelete.Call(ctx, c, agentapi.DBDeleteRequest{Name: "shop"})
		wantCode(t, err, agentrpc.CodePrecondition)

		list, err := agentapi.DBList.Call(ctx, c, agentapi.DBListRequest{})
		if err != nil || list.Databases == nil || len(list.Databases) != 0 {
			t.Fatalf("list = %+v, %v", list, err)
		}
	})

	t.Run("server not running", func(t *testing.T) {
		db := &recordingDB{err: &os.SyscallError{Syscall: "connect", Err: syscall.ENOENT}}
		c, _ := startSiteAgentCfg(t, Config{DB: newDBManager(t, db)})
		ctx := context.Background()

		if _, err := agentapi.SiteCreate.Call(ctx, c, agentapi.SiteCreateRequest{User: "shop"}); err != nil {
			t.Fatal(err)
		}

		_, err := agentapi.DBCreate.Call(ctx, c, agentapi.DBCreateRequest{Site: "shop", Name: "shop"})
		wantCode(t, err, agentrpc.CodePrecondition)
	})
}

func TestDBSizes(t *testing.T) {
	t.Run("no manager", func(t *testing.T) {
		c, _ := startSiteAgentCfg(t, Config{})
		ctx := context.Background()

		got, err := agentapi.DBSizes.Call(ctx, c, agentapi.DBSizesRequest{})
		if err != nil || got.Databases == nil || len(got.Databases) != 0 {
			t.Fatalf("sizes = %+v, %v", got, err)
		}
	})

	t.Run("connection cannot read", func(t *testing.T) {
		c, _ := startSiteAgentCfg(t, Config{DB: newDBManager(t, &recordingDB{})})
		ctx := context.Background()

		if _, err := agentapi.SiteCreate.Call(ctx, c, agentapi.SiteCreateRequest{User: "shop"}); err != nil {
			t.Fatal(err)
		}

		if _, err := agentapi.DBCreate.Call(ctx, c, agentapi.DBCreateRequest{Site: "shop", Name: "shop_wp"}); err != nil {
			t.Fatal(err)
		}

		_, err := agentapi.DBSizes.Call(ctx, c, agentapi.DBSizesRequest{})
		wantCode(t, err, agentrpc.CodePrecondition)
	})
}
