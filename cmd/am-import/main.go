// Command am-import creates an Apple Music library playlist from a text file
// of "Artist - Title" lines, using the web player's tokens.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
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

// Exit codes. See the table in README.md and CLAUDE.md.
//
// TODO(#7): add exitAuth (2) and map config.ErrMissingToken and
// applemusic.ErrUnauthorized to it with errors.Is. Until then both exit 1.
const (
	exitOK    = 0
	exitError = 1
	exitInput = 3
)

func main() {
	// os.Exit skips deferred calls, so all the work happens in cli(), whose
	// defers run before we exit with its result.
	os.Exit(cli(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

// cli parses flags, builds dependencies, calls run and maps its error to an
// exit code. It takes its inputs as arguments so tests can drive it.
func cli(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	opts, err := parseFlags(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return exitOK
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
		fmt.Fprintf(stderr, "am-import: %v\n", err)
		return exitError
	}
	if opts.storefront == "" {
		opts.storefront = cfg.Storefront
	}

	httpClient := &http.Client{Timeout: 30 * time.Second}
	api := applemusic.New(httpClient, applemusic.DefaultBaseURL, cfg.DevToken, cfg.UserToken)

	if err := run(ctx, opts, api, stdout, logger); err != nil {
		fmt.Fprintf(stderr, "am-import: %v\n", err)
		return exitError
	}
	return exitOK
}

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
		return o, err
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
