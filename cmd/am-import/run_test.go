package main

import (
	"bytes"
	"context"
	"encoding/json"
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
	if err != errNothingMatched {
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
			if code := cli(context.Background(), tt.args, &out, io.Discard); code != tt.wantCode {
				t.Errorf("cli(%v) = %d, want %d", tt.args, code, tt.wantCode)
			}
			if !strings.Contains(out.String(), tt.wantOut) {
				t.Errorf("stdout = %q, want containing %q", out.String(), tt.wantOut)
			}
		})
	}
}
