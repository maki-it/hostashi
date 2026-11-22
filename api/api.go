// SPDX-License-Identifier: AGPL-3.0-or-later

// Package api holds the OpenAPI 3.1 document of /api/v1 (ADR-0058). It is
// generated from the route records in internal/serve; regenerate it with
//
//	go test ./internal/serve -run TestOpenAPIDocument -update
package api

import _ "embed"

// Spec is the OpenAPI document as JSON.
//
//go:embed openapi.json
var Spec []byte
