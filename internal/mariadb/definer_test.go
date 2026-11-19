// SPDX-License-Identifier: AGPL-3.0-or-later

package mariadb

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

func strip(t testing.TB, in string) string {
	t.Helper()

	out, err := io.ReadAll(StripDefiners(strings.NewReader(in)))
	if err != nil {
		t.Fatal(err)
	}

	return string(out)
}

func TestStripDefiners(t *testing.T) {
	t.Parallel()

	tests := []struct{ name, in, want string }{
		{
			"view as mariadb-dump writes it",
			"/*!50001 CREATE ALGORITHM=UNDEFINED */\n/*!50013 DEFINER=`wp`@`localhost` SQL SECURITY DEFINER */\n/*!50001 VIEW `v` AS select 1 AS `1` */;\n",
			"/*!50001 CREATE ALGORITHM=UNDEFINED */\n/*!50013  SQL SECURITY DEFINER */\n/*!50001 VIEW `v` AS select 1 AS `1` */;\n",
		},
		{
			"trigger",
			"/*!50003 CREATE*/ /*!50017 DEFINER=`wp`@`localhost`*/ /*!50003 TRIGGER t BEFORE INSERT ON `x` FOR EACH ROW SET NEW.a = 1 */;;\n",
			"/*!50003 CREATE*/ /*!50017 */ /*!50003 TRIGGER t BEFORE INSERT ON `x` FOR EACH ROW SET NEW.a = 1 */;;\n",
		},
		{
			"procedure",
			"CREATE DEFINER=`root`@`%` PROCEDURE `p`()\nBEGIN SELECT 1; END ;;\n",
			"CREATE  PROCEDURE `p`()\nBEGIN SELECT 1; END ;;\n",
		},
		{
			"event",
			"/*!50106 CREATE*/ /*!50117 DEFINER=`a`@`b`*/ /*!50106 EVENT `e` ON SCHEDULE EVERY 1 DAY DO SELECT 1 */ ;;\n",
			"/*!50106 CREATE*/ /*!50117 */ /*!50106 EVENT `e` ON SCHEDULE EVERY 1 DAY DO SELECT 1 */ ;;\n",
		},
		{"lower case and spaces", "create definer = 'u' @ 'h' view v as select 1;", "create  view v as select 1;"},
		{"double quotes", `CREATE DEFINER="u"@"h" VIEW v AS SELECT 1;`, "CREATE  VIEW v AS SELECT 1;"},
		{"doubled quote in name", "CREATE DEFINER=`a``b`@`h` VIEW v;", "CREATE  VIEW v;"},
		{"escaped quote in name", `CREATE DEFINER='a\'b'@'h' VIEW v;`, "CREATE  VIEW v;"},
		{"two clauses", "DEFINER=`a`@`b`DEFINER=`c`@`d`;", ";"},
		{"in a string", "INSERT INTO t VALUES ('DEFINER=`a`@`b`');", "INSERT INTO t VALUES ('DEFINER=`a`@`b`');"},
		{"in a string after escape", `INSERT INTO t VALUES ('x\' DEFINER=` + "`a`@`b`" + `');`, `INSERT INTO t VALUES ('x\' DEFINER=` + "`a`@`b`" + `');`},
		{"in an identifier", "SELECT `DEFINER=``a``@``b```;", "SELECT `DEFINER=``a``@``b```;"},
		{"in a plain comment", "/* DEFINER=`a`@`b` */ SELECT 1;", "/* DEFINER=`a`@`b` */ SELECT 1;"},
		{"in a line comment", "-- DEFINER=`a`@`b`\nSELECT 1;", "-- DEFINER=`a`@`b`\nSELECT 1;"},
		{"in a hash comment", "# DEFINER=`a`@`b`\nDEFINER=`c`@`d`", "# DEFINER=`a`@`b`\n"},
		{"clause at the very end", "x DEFINER=`a`@`b`", "x "},
		{"after a comment", "/* x */DEFINER=`a`@`b` x", "/* x */ x"},
		{"double dash without space is code", "SELECT 1--DEFINER=`a`@`b`\n", "SELECT 1--\n"},
		{"part of a word", "XDEFINER=`a`@`b`", "XDEFINER=`a`@`b`"},
		{"longer word", "DEFINERS=`a`@`b`", "DEFINERS=`a`@`b`"},
		{"no host", "CREATE DEFINER=`a` VIEW v;", "CREATE DEFINER=`a` VIEW v;"},
		{"unquoted", "CREATE DEFINER=root@localhost VIEW v;", "CREATE DEFINER=root@localhost VIEW v;"},
		{"current_user stays", "CREATE DEFINER=CURRENT_USER VIEW v;", "CREATE DEFINER=CURRENT_USER VIEW v;"},
		{"sql security stays", "SQL SECURITY DEFINER VIEW", "SQL SECURITY DEFINER VIEW"},
		{"cut at the end", "CREATE DEFINER=`a`@`b", "CREATE DEFINER=`a`@`b"},
		{"cut before the host", "CREATE DEFINER=`a`@", "CREATE DEFINER=`a`@"},
		{"empty", "", ""},
		{"after the executable comment ends", "/*!50013 x */ DEFINER=`a`@`b`;", "/*!50013 x */ ;"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := strip(t, tc.in); got != tc.want {
				t.Errorf("strip(%q)\n got %q\nwant %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestStripDefinersLongClausePasses(t *testing.T) {
	t.Parallel()

	in := "CREATE DEFINER=`" + strings.Repeat("a", 2*maxClause) + "`@`b` VIEW v;"
	if got := strip(t, in); got != in {
		t.Errorf("a clause longer than %d bytes changed", maxClause)
	}
}

func TestStripDefinersOneByteReads(t *testing.T) {
	t.Parallel()

	in := "INSERT INTO t VALUES ('DEFINER=`a`@`b`');\nCREATE DEFINER=`a`@`b` PROCEDURE p() SELECT 1;"
	want := strip(t, in)

	out, err := io.ReadAll(iotest.OneByteReader(StripDefiners(iotest.HalfReader(strings.NewReader(in)))))
	if err != nil {
		t.Fatal(err)
	}

	if string(out) != want {
		t.Errorf("one-byte reads: got %q, want %q", out, want)
	}

	if strings.Contains(want, "DEFINER=`a`@`b` PROCEDURE") {
		t.Errorf("clause not stripped: %q", want)
	}
}

func TestStripDefinersReaderError(t *testing.T) {
	t.Parallel()

	r := StripDefiners(io.MultiReader(strings.NewReader("SELECT 1; DEFINER=`a`"), iotest.ErrReader(io.ErrUnexpectedEOF)))

	out, err := io.ReadAll(r)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err = %v, want io.ErrUnexpectedEOF", err)
	}

	if string(out) != "SELECT 1; DEFINER=`a`" {
		t.Errorf("out = %q", out)
	}
}

// isSubsequence reports whether a can be made from b by deleting bytes.
func isSubsequence(a, b []byte) bool {
	for _, c := range b {
		if len(a) > 0 && a[0] == c {
			a = a[1:]
		}
	}

	return len(a) == 0
}

func FuzzStripDefiners(f *testing.F) {
	for _, s := range []string{
		"CREATE DEFINER=`a`@`b` VIEW v;",
		"/*!50017 DEFINER=`a`@`b`*/",
		"'DEFINER=`a`@`b`'",
		"-- DEFINER=`a`@`b`\n",
		"/* */DEFINER='a\\'b'@\"h\"",
		"DEFINER=`a``b`@`h`DEFINER=`c`@`d`",
	} {
		f.Add([]byte(s), uint8(1))
	}

	f.Fuzz(func(t *testing.T, in []byte, chunk uint8) {
		whole, err := io.ReadAll(StripDefiners(bytes.NewReader(in)))
		if err != nil {
			t.Fatal(err)
		}

		// Reading in other chunk sizes gives the same output.
		n := int(chunk%64) + 1

		chunked, err := io.ReadAll(StripDefiners(&chunkReader{b: in, n: n}))
		if err != nil {
			t.Fatal(err)
		}

		if !bytes.Equal(whole, chunked) {
			t.Fatalf("chunk size %d: %q != %q", n, chunked, whole)
		}

		// The filter only ever removes bytes.
		if !isSubsequence(whole, in) || len(whole) > len(in) {
			t.Fatalf("output %q is not a subsequence of %q", whole, in)
		}

		// Without the keyword nothing changes.
		if !bytes.Contains(bytes.ToLower(in), []byte("definer")) && !bytes.Equal(whole, in) {
			t.Fatalf("input without DEFINER changed: %q -> %q", in, whole)
		}
	})
}

type chunkReader struct {
	b []byte
	n int
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if len(c.b) == 0 {
		return 0, io.EOF
	}

	k := min(c.n, len(p), len(c.b))
	copy(p, c.b[:k])
	c.b = c.b[k:]

	return k, nil
}
