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
	exitError = 1 // also "partial": some lines unmatched
	exitAuth  = 2
	exitInput = 3
)

// authHelp is printed when Apple rejects the tokens. It names the steps,
// never the token values.
const authHelp = `Apple Music rejected your web-player tokens; they have probably expired.
Refresh them:
  1. Open https://music.apple.com in a browser and sign in.
  2. Open DevTools (Cmd+Opt+I) > Network, filter on "amp-api", click around.
  3. From any amp-api request's Request Headers copy
       authorization (without "Bearer ")  -> AM_DEV_TOKEN
       media-user-token                   -> AM_USER_TOKEN
     into .env or your shell.
See "Token setup" in the README.`

func main() {
	// os.Exit skips deferred calls, so all the work happens in cli(), whose
	// defers run before we exit with its result.
	// TODO(#8): derive ctx from signal.NotifyContext so Ctrl-C cancels cleanly.
	os.Exit(cli(context.Background(), os.Args[1:], os.Stdout, os.Stderr, applemusic.DefaultBaseURL))
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
		fmt.Fprintln(stderr, "Apple Music is rate limiting requests. Wait a minute and run again.")
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
	verbose     bool
	showVersion bool
	file        string
}

func parseFlags(args []string, stderr io.Writer) (options, error) {
	var o options
	// A FlagSet of our own with ContinueOnError returns errors instead of
	// calling os.Exit(2), which would collide with our auth exit code.
	fs := flag.NewFlagSet("am-import", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&o.name, "name", "", "name of the playlist to create (required unless -dry-run)")
	fs.StringVar(&o.storefront, "storefront", "", "catalog storefront, e.g. cz or us (default $AM_STOREFRONT or us)")
	fs.BoolVar(&o.dryRun, "dry-run", false, "search and show matches, but create nothing")
	fs.BoolVar(&o.verbose, "v", false, "verbose (debug) logging")
	fs.BoolVar(&o.showVersion, "version", false, "print version and exit")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: am-import -name \"Playlist name\" [-storefront cz] [-dry-run] [-v] <file.txt>")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return o, err
		}
		return o, errFlagsReported
	}
	if o.showVersion {
		return o, nil
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return o, fmt.Errorf("expected exactly one input file, got %d arguments", fs.NArg())
	}
	o.file = fs.Arg(0)
	if o.name == "" && !o.dryRun {
		return o, errors.New("-name is required (or use -dry-run)")
	}
	return o, nil
}
