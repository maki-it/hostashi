// SPDX-License-Identifier: AGPL-3.0-or-later

package sysconf

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/godbus/dbus/v5"
)

type call struct {
	dest, path, method string
	args               []any
}

type fakeBus struct {
	calls  []call
	props  map[string]any // "iface.name" -> value
	zones  []string
	fail   string // method that fails
	closed int
}

func (f *fakeBus) call(_ context.Context, dest, path, method string, args []any, out ...any) error {
	f.calls = append(f.calls, call{dest, path, method, args})

	if method == f.fail {
		return dbus.Error{Name: "org.freedesktop.DBus.Error.AccessDenied"}
	}

	switch method {
	case propertiesGet:
		key := fmt.Sprint(args[0], ".", args[1])

		v, ok := f.props[key]
		if !ok {
			return fmt.Errorf("no property %s", key)
		}

		dst, ok := out[0].(*dbus.Variant)
		if !ok {
			return errors.New("out is not a variant")
		}

		*dst = dbus.MakeVariant(v)
	case timedateIface + ".ListTimezones":
		dst, ok := out[0].(*[]string)
		if !ok {
			return errors.New("out is not a string slice")
		}

		*dst = slices.Clone(f.zones)
	default:
		if len(out) != 0 {
			return errors.New("unexpected out values")
		}
	}

	return nil
}

func (f *fakeBus) close() error { f.closed++; return nil }

func client(f *fakeBus) *Client {
	return &Client{dial: func() (caller, error) { return f, nil }}
}

func TestState(t *testing.T) {
	f := &fakeBus{props: map[string]any{
		hostnameIface + ".Hostname":        "web2",
		hostnameIface + ".StaticHostname":  "web2.example.org",
		timedateIface + ".Timezone":        "Europe/Berlin",
		timedateIface + ".NTPSynchronized": true,
	}}

	st, err := client(f).State(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	want := State{Hostname: "web2", StaticHostname: "web2.example.org", Timezone: "Europe/Berlin", NTPSynchronized: true}
	if st != want {
		t.Fatalf("got %+v", st)
	}

	if f.closed != 1 {
		t.Fatalf("closed %d times", f.closed)
	}

	for _, c := range f.calls {
		if c.method != propertiesGet {
			t.Fatalf("State called %s", c.method)
		}
	}
}

func TestStateWrongType(t *testing.T) {
	f := &fakeBus{props: map[string]any{hostnameIface + ".Hostname": uint32(1)}}
	if _, err := client(f).State(t.Context()); err == nil {
		t.Fatal("no error for a wrong type")
	}
}

func TestSetters(t *testing.T) {
	cases := []struct {
		name string
		run  func(*Client) error
		want []call
	}{
		{"timezone", func(c *Client) error { return c.SetTimezone(t.Context(), "Europe/Berlin") }, []call{
			{timedateDest, timedatePath, "org.freedesktop.timedate1.SetTimezone", []any{"Europe/Berlin", false}},
		}},
		{"hostname", func(c *Client) error { return c.SetHostname(t.Context(), "web3") }, []call{
			{hostnameDest, hostnamePath, "org.freedesktop.hostname1.SetStaticHostname", []any{"web3", false}},
			{hostnameDest, hostnamePath, "org.freedesktop.hostname1.SetHostname", []any{"web3", false}},
		}},
		{"reboot", func(c *Client) error { return c.Reboot(t.Context()) }, []call{
			{loginDest, loginPath, "org.freedesktop.login1.Manager.Reboot", []any{false}},
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeBus{}
			if err := tc.run(client(f)); err != nil {
				t.Fatal(err)
			}

			if !reflect.DeepEqual(f.calls, tc.want) {
				t.Fatalf("calls %+v, want %+v", f.calls, tc.want)
			}

			if f.closed != 1 {
				t.Fatalf("closed %d times", f.closed)
			}
		})
	}
}

func TestSetHostnameStopsAfterStaticFails(t *testing.T) {
	f := &fakeBus{fail: hostnameIface + ".SetStaticHostname"}
	if err := client(f).SetHostname(t.Context(), "web3"); err == nil {
		t.Fatal("no error")
	}

	if len(f.calls) != 1 {
		t.Fatalf("calls after the failure: %+v", f.calls)
	}
}

func TestTimezonesSorted(t *testing.T) {
	f := &fakeBus{zones: []string{"UTC", "Europe/Berlin", "America/New_York"}}

	zones, err := client(f).Timezones(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(zones, []string{"America/New_York", "Europe/Berlin", "UTC"}) {
		t.Fatalf("got %v", zones)
	}
}

func TestNoBus(t *testing.T) {
	c := &Client{dial: func() (caller, error) { return nil, errors.New("connection refused") }}
	if err := c.Reboot(t.Context()); !errors.Is(err, ErrNoBus) {
		t.Fatalf("got %v", err)
	}
}

func TestCanceledContextCallsNothing(t *testing.T) {
	f := &fakeBus{}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := client(f).Reboot(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}

	if len(f.calls) != 0 {
		t.Fatalf("calls: %+v", f.calls)
	}
}
