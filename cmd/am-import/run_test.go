package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/moudlajs/am-import/internal/applemusic"
)

// fakeAPI is an httptest server that answers search by term and records
// every playlist creation.
type fakeAPI struct {
	mu      sync.Mutex // the handler runs on the server's goroutines
	posts   int
	created []string // track IDs from the last create request
}

// catalog maps a search term to the songs the fake returns for it.
var catalog = map[string][]map[string]string{
	"Björk Army of Me": {
		{"id": "k1", "name": "Army of Me (Karaoke)", "artistName": "Karaoke Stars"},
		{"id": "b1", "name": "Army of Me", "artistName": "Björk"},
	},
	"Portishead Glory Box": {
		{"id": "p1", "name": "Glory Box", "artistName": "Portishead"},
	},
}

func (f *fakeAPI) handler(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/search"):
		var data []map[string]any
		for _, s := range catalog[r.URL.Query().Get("term")] {
			data = append(data, map[string]any{"id": s["id"], "attributes": map[string]string{
				"name": s["name"], "artistName": s["artistName"],
			}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": map[string]any{"songs": map[string]any{"data": data}}})

	case r.Method == http.MethodPost && r.URL.Path == "/v1/me/library/playlists":
		var body struct {
			Relationships struct {
				Tracks struct {
					Data []struct{ ID string } `json:"data"`
				} `json:"tracks"`
			} `json:"relationships"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.posts++
		f.created = nil
		for _, d := range body.Relationships.Tracks.Data {
			f.created = append(f.created, d.ID)
		}
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"data":[{"id":"p.new"}]}`))

	default:
		http.NotFound(w, r)
	}
}

func setup(t *testing.T, input string) (*fakeAPI, *applemusic.Client, string) {
	t.Helper()
	f := &fakeAPI{}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)

	path := filepath.Join(t.TempDir(), "songs.txt")
	if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	return f, applemusic.New(srv.Client(), srv.URL, "fake-dev", "fake-user"), path
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

const input = "# test\nBjörk - Army of Me\nPortishead - Glory Box\nNobody - Nothing\n"

func TestRunDryRunCreatesNothing(t *testing.T) {
	f, api, path := setup(t, input)
	var out bytes.Buffer

	err := run(context.Background(), options{dryRun: true, storefront: "cz", file: path}, api, &out, discardLogger())
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if f.posts != 0 {
		t.Errorf("dry run sent %d POST requests, want 0", f.posts)
	}

	got := out.String()
	for _, want := range []string{"LINE", "Björk - Army of Me", "b1", "Portishead - Glory Box", "p1", "Nobody - Nothing", "(no match)"} {
		if !strings.Contains(got, want) {
			t.Errorf("table missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "k1") {
		t.Errorf("table picked the karaoke result:\n%s", got)
	}
}

// TODO(#9): one line is unmatched here; once #9 lands, run must report
// that (exit 1) rather than return nil.
func TestRunCreatesPlaylistOnce(t *testing.T) {
	f, api, path := setup(t, input)
	var out bytes.Buffer

	err := run(context.Background(), options{name: "Mix", storefront: "cz", file: path}, api, &out, discardLogger())
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if f.posts != 1 {
		t.Fatalf("sent %d POST requests, want 1", f.posts)
	}
	if strings.Join(f.created, ",") != "b1,p1" {
		t.Errorf("created with tracks %v, want [b1 p1] in input order", f.created)
	}
	if !strings.Contains(out.String(), "2 of 3") {
		t.Errorf("summary = %q", out.String())
	}
}

func TestRunNothingMatched(t *testing.T) {
	f, api, path := setup(t, "Nobody - Nothing\n")
	err := run(context.Background(), options{name: "Mix", file: path}, api, io.Discard, discardLogger())
	if !errors.Is(err, errNothingMatched) {
		t.Fatalf("run() error = %v, want errNothingMatched", err)
	}
	if f.posts != 0 {
		t.Errorf("sent %d POST requests, want 0", f.posts)
	}
}

func TestRunInputErrors(t *testing.T) {
	_, api, emptyPath := setup(t, "# only a comment\n\n")
	missing := filepath.Join(t.TempDir(), "missing.txt")

	for name, path := range map[string]string{"empty": emptyPath, "missing": missing} {
		t.Run(name, func(t *testing.T) {
			err := run(context.Background(), options{dryRun: true, file: path}, api, io.Discard, discardLogger())
			if err == nil {
				t.Fatal("run() error = nil, want an input error")
			}
		})
	}
}

func TestCLIFlags(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantCode int
		wantOut  string
	}{
		{"version", []string{"-version"}, exitOK, "dev"},
		{"help", []string{"-h"}, exitOK, ""},
		{"unknown flag", []string{"-nope", "x.txt"}, exitInput, ""},
		{"no file", []string{"-name", "x"}, exitInput, ""},
		{"two files", []string{"-name", "x", "a.txt", "b.txt"}, exitInput, ""},
		{"no name without dry-run", []string{"a.txt"}, exitInput, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			if code := cli(context.Background(), tt.args, &out, io.Discard, "http://unused.invalid"); code != tt.wantCode {
				t.Errorf("cli(%v) = %d, want %d", tt.args, code, tt.wantCode)
			}
			if !strings.Contains(out.String(), tt.wantOut) {
				t.Errorf("stdout = %q, want containing %q", out.String(), tt.wantOut)
			}
		})
	}
}

// TestCLIEndToEnd drives cli() through config loading, the storefront
// fallback and client wiring, against the fake API.
func TestCLIEndToEnd(t *testing.T) {
	f := &fakeAPI{}
	var storefrontPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/search") {
			f.mu.Lock()
			storefrontPath = r.URL.Path
			f.mu.Unlock()
		}
		f.handler(w, r)
	}))
	t.Cleanup(srv.Close)

	// Run in an empty directory so a developer's real .env is never read.
	t.Chdir(t.TempDir())
	if err := os.WriteFile("songs.txt", []byte("Portishead - Glory Box\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AM_DEV_TOKEN", "fake-dev")
	t.Setenv("AM_USER_TOKEN", "fake-user")
	t.Setenv("AM_STOREFRONT", "cz")

	var out, errOut bytes.Buffer
	code := cli(context.Background(), []string{"-name", "Mix", "songs.txt"}, &out, &errOut, srv.URL)
	if code != exitOK {
		t.Fatalf("cli() = %d, want %d; stderr:\n%s", code, exitOK, errOut.String())
	}
	if storefrontPath != "/v1/catalog/cz/search" {
		t.Errorf("searched %q, want the AM_STOREFRONT fallback cz", storefrontPath)
	}
	if f.posts != 1 {
		t.Errorf("sent %d POST requests, want 1", f.posts)
	}
	if strings.Contains(errOut.String(), "fake-dev") || strings.Contains(errOut.String(), "fake-user") {
		t.Errorf("stderr leaks a token:\n%s", errOut.String())
	}
}

func TestCLIFlagErrorPrintedOnce(t *testing.T) {
	var errOut bytes.Buffer
	cli(context.Background(), []string{"-nope", "x.txt"}, io.Discard, &errOut, "http://unused.invalid")
	if n := strings.Count(errOut.String(), "-nope"); n != 1 {
		t.Errorf("flag error printed %d times, want 1:\n%s", n, errOut.String())
	}
}

// cliRun runs cli() in a temp dir with input.txt holding input, tokens set
// (unless noTokens), and the API answered by handler. It returns the exit
// code and stderr.
func cliRun(t *testing.T, handler http.HandlerFunc, input string, noTokens bool, args ...string) (int, string) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	t.Chdir(t.TempDir()) // never read a developer's real .env
	if input != "" {
		if err := os.WriteFile("input.txt", []byte(input), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, k := range []string{"AM_DEV_TOKEN", "AM_USER_TOKEN", "AM_STOREFRONT"} {
		t.Setenv(k, "")
		_ = os.Unsetenv(k) // t.Setenv's cleanup restores the original value
	}
	if !noTokens {
		t.Setenv("AM_DEV_TOKEN", "fake-dev")
		t.Setenv("AM_USER_TOKEN", "fake-user")
	}

	var errOut bytes.Buffer
	code := cli(context.Background(), args, io.Discard, &errOut, srv.URL)
	return code, errOut.String()
}

func status(code int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }
}

func TestCLIExitCodes(t *testing.T) {
	ok := (&fakeAPI{}).handler
	tests := []struct {
		name       string
		handler    http.HandlerFunc
		input      string
		noTokens   bool
		args       []string
		wantCode   int
		wantStderr string
	}{
		{"all matched", ok, "Portishead - Glory Box\n", false, []string{"-name", "x", "input.txt"}, exitOK, ""},
		{"missing token", ok, "Portishead - Glory Box\n", true, []string{"-name", "x", "input.txt"}, exitAuth, "AM_DEV_TOKEN is not set"},
		{"401", status(http.StatusUnauthorized), "a - b\n", false, []string{"-name", "x", "input.txt"}, exitAuth, "DevTools"},
		{"403", status(http.StatusForbidden), "a - b\n", false, []string{"-dry-run", "input.txt"}, exitAuth, "media-user-token"},
		{"429", status(http.StatusTooManyRequests), "a - b\n", false, []string{"-name", "x", "input.txt"}, exitError, "rate limiting"},
		{"500", status(http.StatusInternalServerError), "a - b\n", false, []string{"-name", "x", "input.txt"}, exitError, "unexpected status 500"},
		{"missing file", ok, "", false, []string{"-name", "x", "nope.txt"}, exitInput, "open input"},
		{"only comments", ok, "# nothing\n\n", false, []string{"-name", "x", "input.txt"}, exitInput, "no songs found"},
		{"nothing matched", ok, "Nobody - Nothing\n", false, []string{"-name", "x", "input.txt"}, exitInput, "nothing to import"},
		{"bad flag", ok, "", false, []string{"-nope"}, exitInput, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, stderr := cliRun(t, tt.handler, tt.input, tt.noTokens, tt.args...)
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d; stderr:\n%s", code, tt.wantCode, stderr)
			}
			if !strings.Contains(stderr, tt.wantStderr) {
				t.Errorf("stderr missing %q:\n%s", tt.wantStderr, stderr)
			}
			if strings.Contains(stderr, "fake-dev") || strings.Contains(stderr, "fake-user") {
				t.Errorf("stderr leaks a token:\n%s", stderr)
			}
		})
	}
}
