package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"text/tabwriter"

	"github.com/moudlajs/am-import/internal/applemusic"
	"github.com/moudlajs/am-import/internal/matcher"
	"github.com/moudlajs/am-import/internal/parser"
)

// errNothingMatched means no line produced a usable match, so there is
// nothing to put in a playlist.
var errNothingMatched = errors.New("no line matched a song; nothing to import")

// resolved pairs an input line with its chosen song. ok is false when no
// search result was acceptable.
type resolved struct {
	query parser.Query
	song  applemusic.Song
	ok    bool
}

// run does the actual work: read, search, match, then print or create.
func run(ctx context.Context, opts options, api *applemusic.Client, stdout io.Writer, log *slog.Logger) error {
	queries, err := readQueries(opts.file)
	if err != nil {
		return err
	}
	log.Debug("parsed input", "file", opts.file, "queries", len(queries))

	results := make([]resolved, 0, len(queries))
	for _, q := range queries {
		r, err := resolve(ctx, api, opts.storefront, q)
		if err != nil {
			return err
		}
		if r.ok {
			log.Debug("matched", "line", q.Line, "query", q.Raw, "artist", r.song.Artist, "title", r.song.Name, "id", r.song.ID)
		} else {
			log.Warn("no match", "line", q.Line, "query", q.Raw)
		}
		results = append(results, r)
	}

	if opts.dryRun {
		return printTable(stdout, results)
	}

	var ids []string
	for _, r := range results {
		if r.ok {
			ids = append(ids, r.song.ID)
		}
	}
	if len(ids) == 0 {
		return errNothingMatched
	}

	id, err := api.CreatePlaylist(ctx, opts.name, ids)
	if err != nil {
		return fmt.Errorf("create playlist %q: %w", opts.name, err)
	}
	log.Info("created playlist", "name", opts.name, "id", id, "tracks", len(ids))
	fmt.Fprintf(stdout, "Created %q with %d of %d songs.\n", opts.name, len(ids), len(results))
	return nil
}

func readQueries(path string) ([]parser.Query, error) {
	f, err := os.Open(path) // #nosec G304 -- the user names the file to read.
	if err != nil {
		return nil, fmt.Errorf("open input: %w", err)
	}
	defer func() { _ = f.Close() }() // read-only; a close error can't lose data

	queries, err := parser.Parse(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(queries) == 0 {
		return nil, fmt.Errorf("%s: no songs found (only blank lines or comments?)", path)
	}
	return queries, nil
}

// resolve searches for one query and picks the best result.
func resolve(ctx context.Context, api *applemusic.Client, storefront string, q parser.Query) (resolved, error) {
	songs, err := api.Search(ctx, storefront, q.Term())
	if err != nil {
		return resolved{}, fmt.Errorf("line %d: search: %w", q.Line, err)
	}

	candidates := make([]matcher.Candidate, len(songs))
	for i, s := range songs {
		candidates[i] = matcher.Candidate{Artist: s.Artist, Title: s.Name}
	}
	i, ok := matcher.Best(q.Artist, q.Title, candidates)
	if !ok {
		return resolved{query: q}, nil
	}
	return resolved{query: q, song: songs[i], ok: true}, nil
}

// printTable writes the dry-run report as aligned columns.
func printTable(w io.Writer, results []resolved) error {
	// tabwriter pads tab-separated cells into columns; nothing is written
	// until Flush.
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "LINE\tQUERY\tMATCH\tID")
	for _, r := range results {
		match, id := "(no match)", "-"
		if r.ok {
			match, id = r.song.Artist+" - "+r.song.Name, r.song.ID
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\n", r.query.Line, r.query.Raw, match, id)
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("write table: %w", err)
	}
	return nil
}
