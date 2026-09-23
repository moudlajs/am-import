// Command am-import creates an Apple Music library playlist from a text file
// of "Artist - Title" lines, using the web player's tokens.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/moudlajs/am-import/internal/applemusic"
	"github.com/moudlajs/am-import/internal/config"
)

// version is overwritten at build time with -ldflags "-X main.version=...".
// It must be a package-level var (not a const) for -X to work.
var version = "dev"

// Exit codes. See the table in README.md and CLAUDE.md; exitCode maps
// errors onto them.
const (
	exitOK    = 0
	exitError = 1 // generic failure (API/network), and "partial" once #9 lands
	exitAuth  = 2
	exitInput = 3
)

// authHelp is printed when Apple rejects the tokens. It names the steps and
// the variables, never the token values.
var authHelp = fmt.Sprintf(`Apple Music rejected your web-player tokens; they have probably expired.
Refresh them:
  1. Open https://music.apple.com in a browser and sign in.
  2. Open DevTools (Cmd+Opt+I) > Network, filter on "amp-api", click around.
  3. From any amp-api request's Request Headers copy
       authorization (without "Bearer ")  -> %s
       media-user-token                   -> %s
     into .env or your shell.
See "Token setup" in the README.`, config.EnvDevToken, config.EnvUserToken)

func main() {
	// os.Exit skips deferred calls, so the work happens in realMain, whose
	// defers run before we exit with its result.
	os.Exit(realMain())
}

func realMain() int {
	// Ctrl-C (SIGINT) or SIGTERM cancels ctx. Every request and every wait
	// between requests watches ctx, so the run stops promptly and no
	// playlist is created. stop() restores default signal handling, so a
	// second Ctrl-C kills the process outright.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return cli(ctx, os.Args[1:], os.Stdout, os.Stderr, applemusic.DefaultBaseURL)
}

// cli parses flags, builds dependencies, calls run and maps its error to an
// exit code. It takes everything it touches as arguments, including the API
// base URL, so tests can drive it end to end against an httptest.Server.
func cli(ctx context.Context, args []string, stdout, stderr io.Writer, baseURL string) int {
	opts, err := parseFlags(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return exitOK
	}
	if errors.Is(err, errFlagsReported) {
		return exitInput // the flag package already printed the error and usage
	}
	if err != nil {
		fmt.Fprintf(stderr, "am-import: %v\n", err)
		return exitInput
	}
	if opts.showVersion {
		fmt.Fprintln(stdout, version)
		return exitOK
	}

	level := slog.LevelInfo
	if opts.verbose {
		level = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: level}))

	if err := config.LoadDotEnv(".env"); err != nil {
		fmt.Fprintf(stderr, "am-import: %v\n", err)
		return exitInput
	}
	cfg, err := config.FromEnv()
	if err != nil {
		return report(stderr, err)
	}
	if opts.storefront == "" {
		opts.storefront = cfg.Storefront
	}

	httpClient := &http.Client{Timeout: 30 * time.Second}
	api := applemusic.New(httpClient, baseURL, cfg.DevToken, cfg.UserToken)

	if err := run(ctx, opts, api, stdout, logger); err != nil {
		return report(stderr, err)
	}
	return exitOK
}

// report prints err, plus advice for the errors a user can act on, and
// returns the matching exit code.
func report(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "am-import: %v\n", err)
	code := exitCode(err)
	switch {
	case errors.Is(err, applemusic.ErrUnauthorized):
		fmt.Fprintln(stderr, authHelp)
	case errors.Is(err, applemusic.ErrRateLimited):
		fmt.Fprintln(stderr, "Apple Music is rate limiting requests. Wait a minute, then run again with a larger -delay (e.g. -delay 2s).")
	case errors.Is(err, context.Canceled):
		fmt.Fprintln(stderr, "Interrupted; no playlist was created or changed.")
	}
	return code
}

// exitCode maps an error from config or run onto an exit code.
// errors.Is and errors.As look through every %w wrapping layer.
func exitCode(err error) int {
	var pathErr *fs.PathError
	switch {
	case err == nil:
		return exitOK
	case errors.Is(err, config.ErrMissingToken), errors.Is(err, applemusic.ErrUnauthorized):
		return exitAuth
	case errors.Is(err, errNoSongs), errors.Is(err, errNothingMatched), errors.As(err, &pathErr):
		// *fs.PathError: the input file could not be opened or read.
		return exitInput
	default:
		return exitError
	}
}

// errFlagsReported means flag parsing failed and the flag package has
// already printed the problem and the usage text.
var errFlagsReported = errors.New("invalid flags")

// options are the parsed command-line flags and argument.
type options struct {
	name        string
	storefront  string // empty means "use AM_STOREFRONT or its default"
	dryRun      bool
	delay       time.Duration // pause between search requests
	verbose     bool
	showVersion bool
	file        string
}

func parseFlags(args []string, stderr io.Writer) (options, error) {
	var o options
	// A FlagSet of our own with ContinueOnError returns errors instead of
	// calling os.Exit(2), which would collide with our auth exit code.
	fset := flag.NewFlagSet("am-import", flag.ContinueOnError)
	fset.SetOutput(stderr)
	fset.StringVar(&o.name, "name", "", "name of the playlist to create (required unless -dry-run)")
	fset.StringVar(&o.storefront, "storefront", "", "catalog storefront, e.g. cz or us (default $AM_STOREFRONT or us)")
	fset.BoolVar(&o.dryRun, "dry-run", false, "search and show matches, but create nothing")
	fset.DurationVar(&o.delay, "delay", 500*time.Millisecond, "pause between search requests, e.g. 500ms or 2s")
	fset.BoolVar(&o.verbose, "v", false, "verbose (debug) logging")
	fset.BoolVar(&o.showVersion, "version", false, "print version and exit")
	fset.Usage = func() {
		fmt.Fprintln(stderr, "usage: am-import -name \"Playlist name\" [-storefront cz] [-dry-run] [-delay 500ms] [-v] <file.txt>")
		fset.PrintDefaults()
	}

	if err := fset.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return o, err
		}
		return o, errFlagsReported
	}
	if o.showVersion {
		return o, nil
	}
	if fset.NArg() != 1 {
		fset.Usage()
		return o, fmt.Errorf("expected exactly one input file, got %d arguments", fset.NArg())
	}
	o.file = fset.Arg(0)
	if o.delay < 0 {
		return o, errors.New("-delay must not be negative")
	}
	if o.name == "" && !o.dryRun {
		return o, errors.New("-name is required (or use -dry-run)")
	}
	return o, nil
}
