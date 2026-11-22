// SPDX-License-Identifier: AGPL-3.0-or-later

package apiclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// DeviceStart is the panel's answer to a new browser login.
type DeviceStart struct {
	DeviceCode              string `json:"deviceCode"`
	UserCode                string `json:"userCode"`
	VerificationURI         string `json:"verificationUri"`
	VerificationURIComplete string `json:"verificationUriComplete"`
	ExpiresIn               int    `json:"expiresIn"`
	Interval                int    `json:"interval"`
}

// NewToken is a token the panel hands out once.
type NewToken struct {
	Token     string     `json:"token"`
	Name      string     `json:"name"`
	Scopes    []string   `json:"scopes"`
	Sites     []string   `json:"sites"`
	ExpiresAt *time.Time `json:"expiresAt"`
}

// Device login outcomes.
var (
	ErrDenied  = errors.New("the login was denied in the panel")
	ErrExpired = errors.New("the code expired before it was approved; run hostashi login again")
)

// DeviceLogin runs a browser login (RFC 8628, ADR-0058 addendum 1): it
// starts it, calls show with the code and link for the user, and polls
// until the login is approved, denied or expired. wait sleeps between
// polls and returns early with the context's error.
func (c *Client) DeviceLogin(ctx context.Context, client string, show func(DeviceStart), wait func(context.Context, time.Duration) error) (NewToken, error) {
	var st DeviceStart
	if err := c.Do(ctx, http.MethodPost, "/auth/device", map[string]string{"client": client}, &st); err != nil {
		return NewToken{}, err
	}

	show(st)

	interval := time.Duration(st.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}

	for {
		if err := wait(ctx, interval); err != nil {
			return NewToken{}, err
		}

		var nt NewToken

		err := c.Do(ctx, http.MethodPost, "/auth/device/token", map[string]string{"deviceCode": st.DeviceCode}, &nt)

		switch Code(err) {
		case "":
			if err != nil {
				return NewToken{}, err
			}

			if nt.Token == "" {
				return NewToken{}, errors.New("apiclient: the panel sent no token")
			}

			return nt, nil
		case "authorization_pending":
		case "slow_down":
			// RFC 8628 3.5: add five seconds to the interval.
			interval += 5 * time.Second
		case "access_denied":
			return NewToken{}, ErrDenied
		case "expired_token":
			return NewToken{}, ErrExpired
		default:
			return NewToken{}, fmt.Errorf("apiclient: poll: %w", err)
		}
	}
}

// Sleep waits d or until ctx ends.
func Sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
