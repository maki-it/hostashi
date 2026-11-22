// SPDX-License-Identifier: AGPL-3.0-or-later

// Package sysconf changes the server's time zone and hostname and reboots
// it through systemd's own services on the system bus (ADR-0060):
// systemd-timedated, systemd-hostnamed and systemd-logind.
//
// It is the only importer of the D-Bus library (depguard). A bus
// connection could reach any method of any service, so every bus name,
// object path, interface and method is a constant in this file and no
// function takes one as an argument. The agent runs as root, so polkit
// lets the calls through without a prompt; interactive is always false.
//
// The connection goes to the fixed system bus socket, not to an address
// from the environment, and lives for one call.
package sysconf

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/godbus/dbus/v5"
)

// busAddress is the system bus of Debian and Ubuntu (dbus-daemon and
// dbus-broker listen there).
const busAddress = "unix:path=/run/dbus/system_bus_socket"

const (
	timedateDest  = "org.freedesktop.timedate1"
	timedatePath  = "/org/freedesktop/timedate1"
	timedateIface = "org.freedesktop.timedate1"

	hostnameDest  = "org.freedesktop.hostname1"
	hostnamePath  = "/org/freedesktop/hostname1"
	hostnameIface = "org.freedesktop.hostname1"

	loginDest  = "org.freedesktop.login1"
	loginPath  = "/org/freedesktop/login1"
	loginIface = "org.freedesktop.login1.Manager"

	propertiesGet = "org.freedesktop.DBus.Properties.Get"
)

// ErrNoBus means the system bus cannot be reached: dbus is not installed
// or not running.
var ErrNoBus = errors.New("sysconf: the D-Bus system bus is not available")

// caller is the part of a bus connection this package uses.
type caller interface {
	call(ctx context.Context, dest, path, method string, args []any, out ...any) error
	close() error
}

// Client talks to the three services. The zero value is not usable; use
// New.
type Client struct {
	dial func() (caller, error)
}

// New returns a client for the system bus.
func New() *Client { return &Client{dial: dialSystemBus} }

// State is what the services report now.
type State struct {
	Hostname        string // transient, the kernel's
	StaticHostname  string // /etc/hostname
	Timezone        string
	NTPSynchronized bool
}

func (c *Client) with(ctx context.Context, f func(caller) error) error {
	conn, err := c.dial()
	if err != nil {
		return fmt.Errorf("%w: %w", ErrNoBus, err)
	}

	defer func() { _ = conn.close() }()

	if err := ctx.Err(); err != nil {
		return err
	}

	return f(conn)
}

func getProp[T any](ctx context.Context, conn caller, dest, path, iface, name string) (T, error) {
	var (
		v   dbus.Variant
		out T
	)

	if err := conn.call(ctx, dest, path, propertiesGet, []any{iface, name}, &v); err != nil {
		return out, fmt.Errorf("sysconf: read %s.%s: %w", iface, name, err)
	}

	out, ok := v.Value().(T)
	if !ok {
		return out, fmt.Errorf("sysconf: %s.%s has type %s", iface, name, v.Signature())
	}

	return out, nil
}

// State reads the hostnames, the time zone and the NTP state.
func (c *Client) State(ctx context.Context) (State, error) {
	var st State

	err := c.with(ctx, func(conn caller) error {
		var err error

		if st.Hostname, err = getProp[string](ctx, conn, hostnameDest, hostnamePath, hostnameIface, "Hostname"); err != nil {
			return err
		}

		if st.StaticHostname, err = getProp[string](ctx, conn, hostnameDest, hostnamePath, hostnameIface, "StaticHostname"); err != nil {
			return err
		}

		if st.Timezone, err = getProp[string](ctx, conn, timedateDest, timedatePath, timedateIface, "Timezone"); err != nil {
			return err
		}

		st.NTPSynchronized, err = getProp[bool](ctx, conn, timedateDest, timedatePath, timedateIface, "NTPSynchronized")

		return err
	})

	return st, err
}

// Timezones lists the zones timedated accepts (from tzdata), sorted.
func (c *Client) Timezones(ctx context.Context) ([]string, error) {
	var zones []string

	err := c.with(ctx, func(conn caller) error {
		if err := conn.call(ctx, timedateDest, timedatePath, timedateIface+".ListTimezones", nil, &zones); err != nil {
			return fmt.Errorf("sysconf: list time zones: %w", err)
		}

		return nil
	})

	slices.Sort(zones)

	return zones, err
}

// SetTimezone sets the zone. timedated replaces /etc/localtime and
// /etc/timezone and tells systemd. The caller validates zone.
func (c *Client) SetTimezone(ctx context.Context, zone string) error {
	return c.with(ctx, func(conn caller) error {
		if err := conn.call(ctx, timedateDest, timedatePath, timedateIface+".SetTimezone", []any{zone, false}); err != nil {
			return fmt.Errorf("sysconf: set time zone: %w", err)
		}

		return nil
	})
}

// SetHostname sets the static hostname (/etc/hostname) and then the
// transient one, so the change holds now and after a reboot. The caller
// validates name.
func (c *Client) SetHostname(ctx context.Context, name string) error {
	return c.with(ctx, func(conn caller) error {
		if err := conn.call(ctx, hostnameDest, hostnamePath, hostnameIface+".SetStaticHostname", []any{name, false}); err != nil {
			return fmt.Errorf("sysconf: set static hostname: %w", err)
		}

		if err := conn.call(ctx, hostnameDest, hostnamePath, hostnameIface+".SetHostname", []any{name, false}); err != nil {
			return fmt.Errorf("sysconf: set hostname: %w", err)
		}

		return nil
	})
}

// Reboot asks logind to reboot the server. It returns once logind has
// queued the job.
func (c *Client) Reboot(ctx context.Context) error {
	return c.with(ctx, func(conn caller) error {
		if err := conn.call(ctx, loginDest, loginPath, loginIface+".Reboot", []any{false}); err != nil {
			return fmt.Errorf("sysconf: reboot: %w", err)
		}

		return nil
	})
}

// busConn is a real connection.
type busConn struct{ c *dbus.Conn }

func dialSystemBus() (caller, error) {
	c, err := dbus.Connect(busAddress)
	if err != nil {
		return nil, err
	}

	return busConn{c}, nil
}

func (b busConn) call(ctx context.Context, dest, path, method string, args []any, out ...any) error {
	return b.c.Object(dest, dbus.ObjectPath(path)).CallWithContext(ctx, method, 0, args...).Store(out...)
}

func (b busConn) close() error { return b.c.Close() }
