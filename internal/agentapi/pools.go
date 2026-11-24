// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"errors"
	"fmt"
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
	// Quota is the project quota state of the pool's file system: "off",
	// "counting" (usage only, limits not enforced) or "enforced". Empty when
	// the volume cannot be checked, for example because it is not mounted.
	Quota string `json:"quota,omitempty"`
	// Available: the volume is mounted and carries the pool's marker. Only
	// then are the sizes below set.
	Available  bool   `json:"available"`
	FreeBytes  uint64 `json:"freeBytes,omitempty"`
	TotalBytes uint64 `json:"totalBytes,omitempty"`
	// Default: new sites that name no pool land here.
	Default bool `json:"default"`
}

// PoolAddRequest registers a mounted volume as a pool. It carries a path,
// so the agent takes it only from root on the server, never from the
// panel (ADR-0016, spike S9).
type PoolAddRequest struct {
	Name string `json:"name"`
	Path string `json:"path"`
	// Default makes the new pool the default right away.
	Default bool `json:"default,omitempty"`
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

// PoolRenameRequest gives a pool a new name. It carries no path, so the
// panel may send it (ADR-0016 addendum 2).
type PoolRenameRequest struct {
	Name    string `json:"name"`
	NewName string `json:"newName"`
}

// Validate checks both names.
func (r PoolRenameRequest) Validate() error {
	if err := ValidatePoolName(r.Name); err != nil {
		return err
	}

	if err := ValidatePoolName(r.NewName); err != nil {
		return fmt.Errorf("new name: %w", err)
	}

	return nil
}

// PoolDefaultRequest makes a pool the default for new sites. It carries no
// path, so the panel may send it (ADR-0016 addendum 2).
type PoolDefaultRequest struct {
	Name string `json:"name"`
}

// Validate checks the name.
func (r PoolDefaultRequest) Validate() error { return ValidatePoolName(r.Name) }

// Operations of the storage pools. Add and remove are for root only;
// rename and default also for the panel.
var (
	PoolRename  = agentrpc.Op[PoolRenameRequest, PoolInfo]{Name: "pool.rename"}
	PoolDefault = agentrpc.Op[PoolDefaultRequest, PoolInfo]{Name: "pool.default"}
	PoolAdd     = agentrpc.Op[PoolAddRequest, PoolInfo]{Name: "pool.add"}
	PoolRemove  = agentrpc.Op[PoolRemoveRequest, PoolRemoveResponse]{Name: "pool.remove"}
	PoolList    = agentrpc.Op[PoolListRequest, PoolListResponse]{Name: "pool.list"}
)
