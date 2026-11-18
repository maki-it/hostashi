// SPDX-License-Identifier: AGPL-3.0-or-later

package webctl

import (
	"bytes"
	"strings"
	"testing"
)

func TestTestRefusesOddConfigPaths(t *testing.T) {
	for _, p := range []string{
		"", "nginx.conf", "/etc/nginx/other.conf", "/a b/nginx.conf", "/x/nginx.conf;id",
		"/x/nginx.conf\n", "-c/x/nginx.conf", "/$HOME/nginx.conf",
	} {
		err := Controller{}.Test(t.Context(), p)
		if err == nil || !strings.Contains(err.Error(), "refusing") {
			t.Errorf("%q: err = %v", p, err)
		}
	}
}

func TestLimitedWriter(t *testing.T) {
	var w limitedWriter

	w.buf = new(bytes.Buffer)
	w.max = 4

	n, err := w.Write([]byte("abcdef"))
	if n != 6 || err != nil || w.buf.String() != "abcd" {
		t.Fatalf("n=%d err=%v buf=%q", n, err, w.buf.String())
	}
}
