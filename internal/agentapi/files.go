// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import "github.com/maki-it/hostashi/internal/agentrpc"

// FilesSocketDir holds the file manager's sockets, one per site
// (hostashi-files@<site>.socket, ADR-0040). systemd creates them; only root
// and the panel's group may connect.
const FilesSocketDir = "/run/hostashi-files"

// FilesSocket is the file manager socket of a site.
func FilesSocket(site string) string { return FilesSocketDir + "/" + site + ".sock" }

// FilesOpen makes sure the file manager's socket of a site listens. systemd
// starts the helper on the first connection and the helper stops itself
// when idle; file data never passes the agent.
var FilesOpen = agentrpc.Op[FilesOpenRequest, FilesOpenResponse]{Name: "files.open"}

// FilesOpenRequest names the site.
type FilesOpenRequest struct {
	Site string `json:"site"`
}

// Validate checks the site name.
func (r FilesOpenRequest) Validate() error { return ValidateSiteUser(r.Site) }

// FilesOpenResponse tells the panel where to connect.
type FilesOpenResponse struct {
	Socket string `json:"socket"`
}
