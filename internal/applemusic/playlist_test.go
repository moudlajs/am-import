package applemusic

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCreatePlaylistRequest(t *testing.T) {
	var (
		method, path, contentType string
		body                      []byte
	)
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		method, path, contentType = r.Method, r.URL.Path, r.Header.Get("Content-Type")
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"data":[{"id":"p.abc123","type":"library-playlists"}]}`))
	})

	id, err := c.CreatePlaylist(context.Background(), "Road trip", []string{"111", "222"})
	if err != nil {
		t.Fatalf("CreatePlaylist() error = %v", err)
	}
	if id != "p.abc123" {
		t.Errorf("id = %q, want p.abc123", id)
	}
	if method != http.MethodPost || path != "/v1/me/library/playlists" {
		t.Errorf("request = %s %s", method, path)
	}
	if contentType != "application/json" {
		t.Errorf("Content-Type = %q", contentType)
	}

	// Compare as decoded JSON so key order and whitespace don't matter.
	const want = `{"attributes":{"name":"Road trip"},"relationships":{"tracks":{"data":[
		{"id":"111","type":"songs"},{"id":"222","type":"songs"}]}}}`
	assertJSONEqual(t, body, want)
}

func TestCreatePlaylistErrors(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		body      string
		wantErrIs error
		wantErr   string
	}{
		{name: "401", status: http.StatusUnauthorized, wantErrIs: ErrUnauthorized},
		{name: "403", status: http.StatusForbidden, wantErrIs: ErrUnauthorized},
		{name: "429", status: http.StatusTooManyRequests, wantErrIs: ErrRateLimited},
		{name: "no data", status: http.StatusCreated, body: `{"data":[]}`, wantErr: "no playlist ID"},
		{name: "empty ID", status: http.StatusCreated, body: `{"data":[{"id":""}]}`, wantErr: "no playlist ID"},
		{name: "malformed JSON", status: http.StatusCreated, body: `{`, wantErr: "decode"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			})

			_, err := c.CreatePlaylist(context.Background(), "x", []string{"1"})
			if tt.wantErrIs != nil && !errors.Is(err, tt.wantErrIs) {
				t.Fatalf("error = %v, want errors.Is %v", err, tt.wantErrIs)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("error = %v, want containing %q", err, tt.wantErr)
			}
			if err != nil && (strings.Contains(err.Error(), testDevToken) || strings.Contains(err.Error(), testUserToken)) {
				t.Errorf("error leaks a token: %v", err)
			}
		})
	}
}

func TestCreatePlaylistNoTracksEncodesEmptyArray(t *testing.T) {
	var body []byte
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"data":[{"id":"p.x"}]}`))
	})

	if _, err := c.CreatePlaylist(context.Background(), "Empty", nil); err != nil {
		t.Fatalf("CreatePlaylist() error = %v", err)
	}
	// A nil slice would encode as null, which is not what the API expects.
	assertJSONEqual(t, body, `{"attributes":{"name":"Empty"},"relationships":{"tracks":{"data":[]}}}`)
}

func TestAddTracksRequest(t *testing.T) {
	var method, rawPath string
	var body []byte
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		method, rawPath = r.Method, r.URL.EscapedPath()
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	})

	if err := c.AddTracks(context.Background(), "p.abc/../x", []string{"111", "222"}); err != nil {
		t.Fatalf("AddTracks() error = %v", err)
	}
	if method != http.MethodPost {
		t.Errorf("method = %s", method)
	}
	// The ID is path-escaped, so it can't walk to another endpoint.
	if rawPath != "/v1/me/library/playlists/p.abc%2F..%2Fx/tracks" {
		t.Errorf("path = %s", rawPath)
	}
	assertJSONEqual(t, body, `{"data":[{"id":"111","type":"songs"},{"id":"222","type":"songs"}]}`)
}

func TestAddTracksErrors(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		wantErrIs error
	}{
		{"404", http.StatusNotFound, ErrNotFound},
		{"401", http.StatusUnauthorized, ErrUnauthorized},
		{"403", http.StatusForbidden, ErrUnauthorized},
		{"429", http.StatusTooManyRequests, ErrRateLimited},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(tt.status) })
			err := c.AddTracks(context.Background(), "p.x", []string{"1"})
			if !errors.Is(err, tt.wantErrIs) {
				t.Fatalf("error = %v, want errors.Is %v", err, tt.wantErrIs)
			}
			if strings.Contains(err.Error(), testDevToken) || strings.Contains(err.Error(), testUserToken) {
				t.Errorf("error leaks a token: %v", err)
			}
		})
	}
}

func assertJSONEqual(t *testing.T, got []byte, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("request body is not JSON: %v\n%s", err, got)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("bad want JSON: %v", err)
	}
	gb, _ := json.Marshal(g)
	wb, _ := json.Marshal(w)
	if string(gb) != string(wb) {
		t.Errorf("body =\n  %s\nwant\n  %s", gb, wb)
	}
}
