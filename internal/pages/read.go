// SPDX-License-Identifier: AGPL-3.0-or-later

package pages

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// DefaultDir is where the agent keeps the pages and nginx reads them. The
// directory and its files are world-readable by design.
const DefaultDir = "/var/lib/hostashi-pages"

// ErrNoFile is returned by ReadFile for a file the page does not have.
var ErrNoFile = errors.New("pages: no such file in the page")

// ReadFile returns file rel of the committed page id.digest below dir,
// for the panel's preview (ADR-0027 addendum). It only reads: through an
// os.Root, so no symlink or ".." leads outside the page, only regular
// files, and at most agentapi.MaxPageBytes.
func ReadFile(dir, id, digest, rel string) ([]byte, error) {
	if err := agentapi.ValidatePageID(id); err != nil {
		return nil, err
	}

	if err := agentapi.ValidatePageDigest(digest); err != nil {
		return nil, err
	}

	if err := agentapi.ValidatePagePath(rel); err != nil {
		return nil, ErrNoFile
	}

	root, err := os.OpenRoot((&Store{Dir: dir}).Path(id, digest))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNoFile
	}

	if err != nil {
		return nil, fmt.Errorf("pages: %w", err)
	}
	defer root.Close()

	fi, err := root.Lstat(rel)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNoFile
	}

	if err != nil {
		return nil, fmt.Errorf("pages: %w", err)
	}

	if !fi.Mode().IsRegular() {
		return nil, ErrNoFile
	}

	f, err := root.Open(rel)
	if err != nil {
		return nil, fmt.Errorf("pages: %w", err)
	}
	defer f.Close()

	b, err := io.ReadAll(io.LimitReader(f, agentapi.MaxPageBytes+1))
	if err != nil {
		return nil, fmt.Errorf("pages: %w", err)
	}

	if len(b) > agentapi.MaxPageBytes {
		return nil, fmt.Errorf("pages: %s is larger than the page limit", rel)
	}

	return b, nil
}
