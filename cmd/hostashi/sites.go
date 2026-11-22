// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// apiSite is the part of a site the CLI prints.
type apiSite struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Pool    string   `json:"pool"`
	State   string   `json:"state"`
	Domains []string `json:"domains"`
}

// siteCmd runs "site create/delete/list" through the panel's API
// (ADR-0058): a site gets its account, its domains and its virtual host
// like in the panel, as a static site; the panel changes it afterwards.
//
//	site create NAME DOMAIN[,DOMAIN...] [POOL]
//	site delete NAME
//	site list
func siteCmd(ctx context.Context, args []string, out io.Writer) error {
	switch {
	case len(args) == 1 && args[0] == "list":
		var r struct {
			Sites []apiSite `json:"sites"`
		}

		if err := apiDo(ctx, http.MethodGet, "/sites", nil, &r); err != nil {
			return err
		}

		for _, s := range r.Sites {
			fmt.Fprintf(out, "%s\ttype=%s\tpool=%s\tstate=%s\tdomains=%s\n", s.Name, s.Type, s.Pool, s.State, strings.Join(s.Domains, ","))
		}
	case (len(args) == 3 || len(args) == 4) && args[0] == "create":
		in := struct {
			Name    string   `json:"name"`
			Domains []string `json:"domains"`
			Pool    string   `json:"pool,omitempty"`
		}{Name: args[1], Domains: strings.Split(args[2], ",")}

		if len(args) == 4 {
			in.Pool = args[3]
		}

		var s apiSite
		if err := apiDo(ctx, http.MethodPost, "/sites", in, &s); err != nil {
			return err
		}

		fmt.Fprintf(out, "created site %s (pool %s, domains %s)\n", s.Name, s.Pool, strings.Join(s.Domains, ","))
	case len(args) == 2 && args[0] == "delete":
		if err := apiDo(ctx, http.MethodDelete, "/sites/"+url.PathEscape(args[1]), nil, nil); err != nil {
			return err
		}

		fmt.Fprintf(out, "deleted site %s\n", args[1])
	default:
		return errUsage
	}

	return nil
}
