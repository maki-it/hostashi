// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/backup"
)

// auditor writes the audit log (the store).
type auditor interface {
	Audit(ctx context.Context, userID *int64, action, target string, detail map[string]string) error
}

// CloudPanelSource is the agent's migration source (ADR-0026).
type CloudPanelSource interface {
	Get(ctx context.Context) (agentapi.CloudPanelSourceInfo, error)
	Set(ctx context.Context, s agentapi.CloudPanelSource) (agentapi.CloudPanelSourceInfo, error)
	SetHostKey(ctx context.Context, key string) (agentapi.CloudPanelSourceInfo, error)
	Delete(ctx context.Context) (agentapi.CloudPanelSourceInfo, error)
	// ScanHostKey connects to the old server and returns its host key, for
	// the administrator to confirm against the old server's own output.
	ScanHostKey(ctx context.Context, s agentapi.CloudPanelSource) (ssh.PublicKey, error)
}

// RPCCloudPanelSource talks to the agent.
type RPCCloudPanelSource struct{ C *agentrpc.Client }

// Get implements CloudPanelSource.
func (c RPCCloudPanelSource) Get(ctx context.Context) (agentapi.CloudPanelSourceInfo, error) {
	return agentapi.CloudPanelSourceGet.Call(ctx, c.C, agentapi.CloudPanelSourceGetRequest{})
}

// Set implements CloudPanelSource.
func (c RPCCloudPanelSource) Set(ctx context.Context, s agentapi.CloudPanelSource) (agentapi.CloudPanelSourceInfo, error) {
	return agentapi.CloudPanelSourceSet.Call(ctx, c.C, agentapi.CloudPanelSourceSetRequest{Source: s})
}

// SetHostKey implements CloudPanelSource.
func (c RPCCloudPanelSource) SetHostKey(ctx context.Context, key string) (agentapi.CloudPanelSourceInfo, error) {
	return agentapi.CloudPanelHostKey.Call(ctx, c.C, agentapi.CloudPanelHostKeyRequest{Key: key})
}

// Delete implements CloudPanelSource.
func (c RPCCloudPanelSource) Delete(ctx context.Context) (agentapi.CloudPanelSourceInfo, error) {
	return agentapi.CloudPanelSourceDelete.Call(ctx, c.C, agentapi.CloudPanelSourceDeleteRequest{})
}

// ScanHostKey implements CloudPanelSource.
func (RPCCloudPanelSource) ScanHostKey(ctx context.Context, s agentapi.CloudPanelSource) (ssh.PublicKey, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}

	return backup.FetchHostKeyAt(ctx, s.Addr(), "root")
}

func sourceFail(log *slog.Logger, w http.ResponseWriter, err error, what string) {
	var ae *agentrpc.Error

	switch {
	case errors.Is(err, agentapi.ErrSourceInvalid):
		writeError(w, http.StatusUnprocessableEntity, "invalid_argument", err.Error())
	case errors.Is(err, backup.ErrBadHostKey):
		writeError(w, http.StatusUnprocessableEntity, "invalid_argument", "the server offers no supported host key (Ed25519, ECDSA or RSA of 3072 bits and more)")
	case errors.As(err, &ae) && ae.Code == agentrpc.CodePrecondition:
		writeError(w, http.StatusConflict, "no_source", ae.Message)
	default:
		siteFail(log, w, err, what)
	}
}

// registerCloudPanelSource adds the routes for the old server's address,
// the generated key and the pinned host key. Responses never carry the
// private key.
func registerCloudPanelSource(mux *http.ServeMux, a authn, src CloudPanelSource, aud auditor, log *slog.Logger) {
	audit := func(r *http.Request, uid int64, action, target string) {
		if err := aud.Audit(r.Context(), &uid, action, target, nil); err != nil {
			log.ErrorContext(r.Context(), "audit log", "action", action, "err", err)
		}
	}

	admin := func(scope string, h func(http.ResponseWriter, *http.Request, auth.Principal)) http.HandlerFunc {
		return a.scoped(scope, func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
			if p.User.Role != "admin" {
				writeError(w, http.StatusForbidden, "forbidden", "only administrators can import sites")

				return
			}

			h(w, r, p)
		})
	}

	const base = "/api/v1/migrations/cloudpanel/source"

	reply := func(w http.ResponseWriter, info agentapi.CloudPanelSourceInfo, err error, what string) {
		if err != nil {
			sourceFail(log, w, err, what)

			return
		}

		writeJSON(w, http.StatusOK, info)
	}

	mux.HandleFunc("GET "+base, admin("migrations:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		info, err := src.Get(r.Context())
		reply(w, info, err, "read migration source")
	}))

	mux.HandleFunc("PUT "+base, admin("migrations:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in agentapi.CloudPanelSource
		if !decodeJSON(w, r, &in) {
			return
		}

		if err := in.Validate(); err != nil {
			sourceFail(log, w, err, "set migration source")

			return
		}

		info, err := src.Set(r.Context(), in)
		if err == nil {
			audit(r, p.User.ID, "cloudpanel.source.set", in.Addr())
		}

		reply(w, info, err, "set migration source")
	}))

	// The scan only shows the key; nothing is pinned until PUT /hostkey
	// with the key the administrator compared with the old server's own
	// "ssh-keygen -lf" output.
	mux.HandleFunc("POST "+base+"/hostkey/scan", admin("migrations:write", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		info, err := src.Get(r.Context())
		if err != nil {
			sourceFail(log, w, err, "read migration source")

			return
		}

		if info.Source == nil {
			writeError(w, http.StatusConflict, "no_source", "set the old server's address first")

			return
		}

		key, err := src.ScanHostKey(r.Context(), *info.Source)
		if err != nil {
			if errors.Is(err, backup.ErrBadHostKey) {
				sourceFail(log, w, err, "scan host key")

				return
			}

			log.WarnContext(r.Context(), "scan migration host key", "err", err)
			writeError(w, http.StatusBadGateway, "unreachable", "the old server did not answer over SSH")

			return
		}

		writeJSON(w, http.StatusOK, map[string]string{
			"key":         strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))),
			"fingerprint": ssh.FingerprintSHA256(key),
		})
	}))

	mux.HandleFunc("PUT "+base+"/hostkey", admin("migrations:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in struct {
			Key string `json:"key"`
		}
		if !decodeJSON(w, r, &in) {
			return
		}

		if _, err := backup.ParseHostKey(in.Key); err != nil {
			sourceFail(log, w, err, "set migration host key")

			return
		}

		info, err := src.SetHostKey(r.Context(), in.Key)
		if err == nil {
			audit(r, p.User.ID, "cloudpanel.source.hostkey", info.HostKey)
		}

		reply(w, info, err, "set migration host key")
	}))

	mux.HandleFunc("DELETE "+base, admin("migrations:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		info, err := src.Delete(r.Context())
		if err == nil {
			audit(r, p.User.ID, "cloudpanel.source.delete", "")
		}

		reply(w, info, err, "delete migration source")
	}))
}
