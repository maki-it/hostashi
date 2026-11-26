// SPDX-License-Identifier: AGPL-3.0-or-later

package dotenv

import (
	"errors"
	"testing"
)

const laravel = `APP_NAME=Shop
APP_URL=https://shop.example.com # live
# DB_DATABASE=commented
export DB_DATABASE="shop_wp"
DB_USERNAME = 'shop_user'
DB_PASSWORD=old\$secret
MULTI="first
DB_HOST=inside
second"
DB_HOST=127.0.0.1
`

func TestGet(t *testing.T) {
	for key, want := range map[string]string{
		"APP_NAME": "Shop", "APP_URL": "https://shop.example.com", "DB_DATABASE": "shop_wp",
		"DB_USERNAME": "shop_user", "DB_PASSWORD": `old\$secret`, "DB_HOST": "127.0.0.1",
	} {
		if got, ok := Get([]byte(laravel), key); !ok || got != want {
			t.Errorf("%s: %q %v", key, got, ok)
		}
	}

	if _, ok := Get([]byte(laravel), "MULTI"); ok {
		t.Error("a multi-line value is read")
	}

	if got, _ := Get([]byte("A=\"x \\\" y\"\nA=last\r\n"), "A"); got != "last" {
		t.Errorf("last assignment: %q", got)
	}

	if got, _ := Get([]byte(`B="say \"hi\""`), "B"); got != `say "hi"` {
		t.Errorf("escaped quote: %q", got)
	}
}

func TestSet(t *testing.T) {
	out, err := Set([]byte(laravel), []string{"DB_DATABASE", "DB_USERNAME", "DB_PASSWORD", "APP_URL", "DB_PORT"}, map[string]string{
		"DB_DATABASE": "stage_wp", "DB_USERNAME": "stage_user", "DB_PASSWORD": "Abc123", "APP_URL": "https://stage.example.com", "DB_PORT": "3306",
	})
	if err != nil {
		t.Fatal(err)
	}

	want := `APP_NAME=Shop
APP_URL="https://stage.example.com" # live
# DB_DATABASE=commented
export DB_DATABASE="stage_wp"
DB_USERNAME = "stage_user"
DB_PASSWORD="Abc123"
MULTI="first
DB_HOST=inside
second"
DB_HOST=127.0.0.1
DB_PORT="3306"
`
	if string(out) != want {
		t.Errorf("got\n%s", out)
	}

	// Windows line ends stay.
	out, _ = Set([]byte("A=1\r\nB=2\r\n"), []string{"A"}, map[string]string{"A": "x"})
	if string(out) != "A=\"x\"\r\nB=2\r\n" {
		t.Errorf("crlf: %q", out)
	}

	for _, v := range []string{"a\nb", `a"b`, `a\b`, "a$b", "a`b", "a\x00"} {
		if _, err := Set([]byte(laravel), []string{"A"}, map[string]string{"A": v}); !errors.Is(err, ErrValue) {
			t.Errorf("%q: %v", v, err)
		}
	}

	if _, err := Set([]byte("A=\"open\nB=1\n"), []string{"C"}, map[string]string{"C": "x"}); !errors.Is(err, ErrUnclosed) {
		t.Errorf("unclosed: %v", err)
	}

	if _, err := Set(nil, []string{"1A"}, map[string]string{"1A": "x"}); err == nil {
		t.Error("invalid key accepted")
	}
}

// Set never breaks other lines, and Get reads back what it wrote.
func FuzzSet(f *testing.F) {
	f.Add([]byte(laravel), "stage_wp")
	f.Add([]byte("A=\"\n"), "0")
	f.Add([]byte("DB_DATABASE='x' # c\nexport DB_DATABASE=\"a\\\"b\"\n"), "y")

	f.Fuzz(func(t *testing.T, content []byte, v string) {
		out, err := Set(content, []string{"DB_DATABASE"}, map[string]string{"DB_DATABASE": v})
		if err != nil {
			return
		}

		if got, ok := Get(out, "DB_DATABASE"); !ok || got != v {
			t.Fatalf("read back %q %v, want %q from\n%q", got, ok, v, out)
		}

		for _, k := range []string{"APP_URL", "DB_HOST", "OTHER"} {
			a, aok := Get(content, k)
			b, bok := Get(out, k)

			if a != b || aok != bok {
				t.Fatalf("%s changed: %q -> %q", k, a, b)
			}
		}
	})
}
