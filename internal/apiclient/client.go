// SPDX-License-Identifier: AGPL-3.0-or-later

// Package apiclient is the CLI's client of the panel's API (ADR-0058): the
// stored login and plain JSON requests with an API token.
package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// maxAnswer bounds an answer the client reads into memory.
const maxAnswer = 32 << 20

// Error is an error answer of the API.
type Error struct {
	Status    int
	Code      string
	Message   string
	RequestID string
}

func (e *Error) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.Status)
	}

	if e.RequestID != "" {
		return fmt.Sprintf("%s (HTTP %d, request %s)", msg, e.Status, e.RequestID)
	}

	return fmt.Sprintf("%s (HTTP %d)", msg, e.Status)
}

// Client talks to one panel. Token may be empty for the public routes.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
	// UserAgent names the CLI, e.g. "hostashi/0.2.0".
	UserAgent string
}

// New returns a client with a timeout fit for the CLI.
func New(base, token, userAgent string) *Client {
	return &Client{BaseURL: strings.TrimRight(base, "/"), Token: token, UserAgent: userAgent, HTTP: &http.Client{Timeout: 5 * time.Minute}}
}

// Do sends in as JSON (nil: no body) to path below /api/v1 and decodes the
// answer into out (nil: ignored).
func (c *Client) Do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader

	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}

		body = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+"/api/v1"+path, body)
	if err != nil {
		return err
	}

	req.Header.Set("Accept", "application/json")

	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}

	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}

	defer func() { _ = res.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(res.Body, maxAnswer))
	if err != nil {
		return err
	}

	if res.StatusCode >= 300 {
		return answerError(res, raw)
	}

	if out == nil || res.StatusCode == http.StatusNoContent {
		return nil
	}

	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("apiclient: answer of %s %s: %w", method, path, err)
	}

	return nil
}

func answerError(res *http.Response, raw []byte) error {
	e := &Error{Status: res.StatusCode, RequestID: res.Header.Get("X-Request-Id")}

	var body struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			RequestID string `json:"requestId"`
		} `json:"error"`
	}

	if json.Unmarshal(raw, &body) == nil {
		e.Code, e.Message = body.Error.Code, body.Error.Message
		if body.Error.RequestID != "" {
			e.RequestID = body.Error.RequestID
		}
	}

	return e
}

// Code returns the API error code of err, or "".
func Code(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}

	return ""
}

// CheckURL accepts an http or https URL without path, query or user info.
// Plain http is allowed only for loopback addresses: a token must not
// cross a network in clear text.
func CheckURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimRight(raw, "/"))
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "", fmt.Errorf("panel URL %q: want https://host[:port]", raw)
	}

	if u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("panel URL %q: only scheme, host and port", raw)
	}

	if u.Scheme == "http" && !loopback(u.Hostname()) {
		return "", fmt.Errorf("panel URL %q: plain http only for 127.0.0.1, ::1 or localhost", raw)
	}

	return u.Scheme + "://" + u.Host, nil
}

func loopback(host string) bool {
	return host == "localhost" || host == "::1" || strings.HasPrefix(host, "127.")
}
