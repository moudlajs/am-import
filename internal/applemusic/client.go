// Package applemusic is a minimal client for the private endpoints the Apple
// Music web player uses. It authenticates with the web player's tokens.
package applemusic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// DefaultBaseURL is the API host the web player talks to.
const DefaultBaseURL = "https://amp-api.music.apple.com"

// origin is sent on every request; the API rejects requests without it.
const origin = "https://music.apple.com"

// Sentinel errors. Callers check them with errors.Is, which also matches
// when they are wrapped in more context with %w.
var (
	// ErrUnauthorized means the API returned 401 or 403: the web-player
	// tokens are missing, expired or revoked.
	ErrUnauthorized = errors.New("unauthorized: the Apple Music web-player tokens were rejected")
	// ErrRateLimited means the API returned 429.
	ErrRateLimited = errors.New("rate limited by Apple Music")
)

// Client calls the Apple Music API. It is safe to reuse; it holds no
// per-request state.
type Client struct {
	http      *http.Client
	baseURL   string
	devToken  string
	userToken string
}

// New returns a Client. Pass http.DefaultClient (or one with a timeout) and
// DefaultBaseURL in production; tests pass an httptest.Server's client and URL.
func New(httpClient *http.Client, baseURL, devToken, userToken string) *Client {
	return &Client{
		http:      httpClient,
		baseURL:   baseURL,
		devToken:  devToken,
		userToken: userToken,
	}
}

// do sends one request and decodes a JSON response into out (if out is not
// nil). It is the single place that sets headers and maps status codes, so
// every endpoint behaves the same.
//
// Errors never include the request headers, so they cannot leak tokens.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	// A nil io.Reader means "no body". bodyReader stays nil unless we have one.
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode %s %s body: %w", method, path, err)
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, u, bodyReader)
	if err != nil {
		return fmt.Errorf("build %s %s: %w", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.devToken)
	req.Header.Set("Media-User-Token", c.userToken)
	req.Header.Set("Origin", origin)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		// Covers network errors and context cancellation; errors.Is(err,
		// context.Canceled) still works through the wrapping.
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	// The body must always be closed or the connection is not reused.
	// A close error on a response we've finished reading is not actionable.
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%s %s: status %d: %w", method, path, resp.StatusCode, ErrUnauthorized)
	case resp.StatusCode == http.StatusTooManyRequests:
		return fmt.Errorf("%s %s: %w", method, path, ErrRateLimited)
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return fmt.Errorf("%s %s: unexpected status %d", method, path, resp.StatusCode)
	}

	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode %s %s response: %w", method, path, err)
	}
	return nil
}
