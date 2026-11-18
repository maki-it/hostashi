// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/mariadb"
)

// openDB builds the database manager. It does not connect: a server without
// MariaDB still starts the agent, and db.* report that MariaDB is missing.
func openDB(stateDir string, log *slog.Logger) (*mariadb.Manager, error) {
	if stateDir == "" {
		stateDir = DefaultStateDir
	}

	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, fmt.Errorf("state directory: %w", err)
	}

	m, err := mariadb.New(stateDir, mariadb.Connect(mariadb.DefaultSocket), mariadb.Options{Log: log})
	if err != nil {
		return nil, fmt.Errorf("database registry: %w", err)
	}

	return m, nil
}

var errNoDB = &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "MariaDB is not available on this server"}

func (h *siteOps) registerDB(s *agentrpc.Server) {
	agentrpc.Handle(s, agentapi.DBCreate, func(ctx context.Context, _ agentrpc.Caller, req agentapi.DBCreateRequest) (agentapi.DBInfo, error) {
		if h.db == nil {
			return agentapi.DBInfo{}, errNoDB
		}

		// Under the site lock: site.delete must not run between the check
		// and the create.
		h.mu.Lock()
		defer h.mu.Unlock()

		if _, ok := h.m.Home(req.Site); !ok {
			return agentapi.DBInfo{}, &agentrpc.Error{Code: agentrpc.CodeNotFound, Message: "unknown site"}
		}

		d, err := h.db.Create(ctx, req.Site, req.Name)
		if err != nil {
			return agentapi.DBInfo{}, mapDBError(err)
		}

		return dbInfo(d), nil
	})

	agentrpc.Handle(s, agentapi.DBDelete, func(ctx context.Context, _ agentrpc.Caller, req agentapi.DBDeleteRequest) (agentapi.DBDeleteResponse, error) {
		if h.db == nil {
			return agentapi.DBDeleteResponse{}, errNoDB
		}

		return agentapi.DBDeleteResponse{}, mapDBError(h.db.Delete(ctx, req.Name))
	})

	agentrpc.Handle(s, agentapi.DBList, func(_ context.Context, _ agentrpc.Caller, _ agentapi.DBListRequest) (agentapi.DBListResponse, error) {
		out := []agentapi.DBInfo{}
		if h.db == nil {
			return agentapi.DBListResponse{Databases: out}, nil
		}

		all := h.db.List()
		if len(all) > agentapi.MaxDatabases {
			all = all[:agentapi.MaxDatabases]
		}

		for _, d := range all {
			out = append(out, dbInfo(d))
		}

		return agentapi.DBListResponse{Databases: out}, nil
	})

	agentrpc.Handle(s, agentapi.DBUserCreate, func(ctx context.Context, _ agentrpc.Caller, req agentapi.DBUserCreateRequest) (agentapi.DBUserResponse, error) {
		if h.db == nil {
			return agentapi.DBUserResponse{}, errNoDB
		}

		return agentapi.DBUserResponse{}, mapDBError(h.db.CreateUser(ctx, req.Database, req.User, req.Password))
	})

	agentrpc.Handle(s, agentapi.DBUserDelete, func(ctx context.Context, _ agentrpc.Caller, req agentapi.DBUserDeleteRequest) (agentapi.DBUserResponse, error) {
		if h.db == nil {
			return agentapi.DBUserResponse{}, errNoDB
		}

		return agentapi.DBUserResponse{}, mapDBError(h.db.DeleteUser(ctx, req.User))
	})

	agentrpc.Handle(s, agentapi.DBUserPassword, func(ctx context.Context, _ agentrpc.Caller, req agentapi.DBUserPasswordRequest) (agentapi.DBUserResponse, error) {
		if h.db == nil {
			return agentapi.DBUserResponse{}, errNoDB
		}

		return agentapi.DBUserResponse{}, mapDBError(h.db.SetPassword(ctx, req.User, req.Password))
	})
}

func dbInfo(d mariadb.Database) agentapi.DBInfo {
	users := d.Users
	if users == nil {
		users = []string{}
	}

	return agentapi.DBInfo{Name: d.Name, Site: d.Site, Users: users}
}

// mapDBError gives the caller fixed messages. Server details stay in the
// agent log, and mariadb never puts a statement or password into an error.
func mapDBError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, mariadb.ErrNotFound):
		return &agentrpc.Error{Code: agentrpc.CodeNotFound, Message: "unknown database or database user"}
	case errors.Is(err, mariadb.ErrExists):
		return &agentrpc.Error{Code: agentrpc.CodeConflict, Message: "database or database user already exists on the server"}
	case errors.Is(err, mariadb.ErrInvalid):
		return &agentrpc.Error{Code: agentrpc.CodeInvalidArgument, Message: "invalid database name, user or password"}
	case errors.Is(err, mariadb.ErrUnavailable):
		return errNoDB
	default:
		return err
	}
}
