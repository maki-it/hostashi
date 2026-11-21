// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"errors"
	"path"
	"regexp"

	"github.com/maki-it/hostashi/internal/agentrpc"
)

// poolPathRe is the character set of a pool path; it ends up in unit files.
var poolPathRe = regexp.MustCompile(`^/[A-Za-z0-9_./-]{1,255}$`)

// PoolInfo describes one storage pool (ADR-0016).
type PoolInfo struct {
	Name string `json:"name"`
	Path string `json:"path"`
	// Sites is the number of sites on the pool.
	Sites int `json:"sites"`
}

// PoolAddRequest registers a mounted volume as a pool. It carries a path,
// so the agent takes it only from root on the server, never from the
// panel (ADR-0016, spike S9).
type PoolAddRequest struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// Validate checks the request; the agent checks the directory itself.
func (r PoolAddRequest) Validate() error {
	if err := ValidatePoolName(r.Name); err != nil {
		return err
	}

	if !poolPathRe.MatchString(r.Path) || path.Clean(r.Path) != r.Path || r.Path == "/" {
		return errors.New("path: must be an absolute clean path of letters, digits and _ . / -")
	}

	return nil
}

// PoolRemoveRequest names a pool without sites to unregister.
type PoolRemoveRequest struct {
	Name string `json:"name"`
}

// Validate checks the request.
func (r PoolRemoveRequest) Validate() error { return ValidatePoolName(r.Name) }

// PoolRemoveResponse is empty on success.
type PoolRemoveResponse struct{}

// PoolListRequest has no parameters.
type PoolListRequest struct{}

// Validate accepts the empty request.
func (PoolListRequest) Validate() error { return nil }

// PoolListResponse lists the pools sorted by name.
type PoolListResponse struct {
	Pools []PoolInfo `json:"pools"`
}

// Operations of the storage pools. Add and remove are for root only.
var (
	PoolAdd    = agentrpc.Op[PoolAddRequest, PoolInfo]{Name: "pool.add"}
	PoolRemove = agentrpc.Op[PoolRemoveRequest, PoolRemoveResponse]{Name: "pool.remove"}
	PoolList   = agentrpc.Op[PoolListRequest, PoolListResponse]{Name: "pool.list"}
)
