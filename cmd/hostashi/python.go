// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"io"
	"net/http"
)

// pythonCmd runs "hostashi python" through the panel's API (ADR-0061,
// ADR-0058).
//
//	python status
//	python install LINE
//	python remove LINE
func pythonCmd(ctx context.Context, args []string, out io.Writer) error {
	switch {
	case len(args) == 1 && args[0] == "status":
		return apiPrint(ctx, out, http.MethodGet, "/python", nil)
	case len(args) == 2 && args[0] == "install":
		return apiPrint(ctx, out, http.MethodPost, "/python/install", map[string]string{"line": args[1]})
	case len(args) == 2 && args[0] == "remove":
		return apiPrint(ctx, out, http.MethodPost, "/python/remove", map[string]string{"line": args[1]})
	}

	return errUsage
}
