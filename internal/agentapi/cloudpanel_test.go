// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"strings"
	"testing"
)

func TestCloudPanelImportDatabases(t *testing.T) {
	base := CloudPanelImportRequest{Site: "blog", RemoteUser: "oldblog", DocRoot: "/home/oldblog/htdocs/blog.example.com"}

	ok := base
	ok.Databases = []CloudPanelDBMap{{From: "shop", To: "blog_shop"}, {From: "shop", To: "blog_shop2"}}

	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}

	many := base
	for range MaxImportDatabases + 1 {
		many.Databases = append(many.Databases, CloudPanelDBMap{From: "a", To: "b"})
	}

	for _, dbs := range [][]CloudPanelDBMap{
		{{From: "shop", To: "x"}, {From: "blog", To: "x"}},
		{{From: "shop; drop", To: "x"}},
		{{From: "shop", To: "mysql"}},
		{{From: "shop", To: "hostashi_x"}},
		{{From: "", To: "x"}},
		many.Databases,
	} {
		r := base
		r.Databases = dbs

		if err := r.Validate(); err == nil {
			t.Errorf("%v accepted", dbs)
		}
	}
}

func TestCloudPanelDBSQL(t *testing.T) {
	sql := CloudPanelDBSQL("abc123")

	for _, want := range []string{
		"CREATE OR REPLACE USER 'hostashi_import'@'127.0.0.1' IDENTIFIED BY 'abc123';",
		"GRANT " + CloudPanelDBGrant + " ON *.* TO 'hostashi_import'@'127.0.0.1';",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("missing %q in %s", want, sql)
		}
	}
}
