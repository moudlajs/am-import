// Package parser turns an input file of "Artist - Title" lines into queries.
package parser

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// separator splits artist from title. Only the first occurrence counts, so a
// title that itself contains " - " (e.g. "Song - Remastered") stays intact.
const separator = " - "

// bom is the UTF-8 byte order mark some editors (notably Windows Notepad)
// put at the start of a file. Go strings are bytes, so "\uFEFF" is those
// three bytes: EF BB BF.
const bom = "\uFEFF"

// Query is one song to look up.
type Query struct {
	Artist string // empty when the line had no separator
	Title  string // the whole line when Artist is empty
	Raw    string // the trimmed line as written, for the unmatched report
	Line   int    // 1-based line number in the input file
}

// Term is the search string sent to the API.
func (q Query) Term() string {
	if q.Artist == "" {
		return q.Title
	}
	return q.Artist + " " + q.Title
}

// Parse reads r line by line and returns a Query for every line that is not
// blank and not a # comment, plus how many lines were skipped as blank or
// comments (for the end-of-run summary).
func Parse(r io.Reader) (queries []Query, skipped int, err error) {

	// bufio.Scanner's default split function (ScanLines) strips both "\n"
	// and a preceding "\r", so CRLF files need no special handling here.
	// TrimSpace below only trims incidental whitespace around the content.
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if n == 1 {
			line = strings.TrimPrefix(line, bom)
		}
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			skipped++
			continue
		}

		q := Query{Raw: line, Line: n, Title: line}
		// strings.Cut splits around the first separator; ok is false if absent.
		if artist, title, ok := strings.Cut(line, separator); ok {
			q.Artist = strings.TrimSpace(artist)
			q.Title = strings.TrimSpace(title)
		}
		queries = append(queries, q)
	}
	if err := sc.Err(); err != nil {
		return nil, 0, fmt.Errorf("read input: %w", err)
	}
	return queries, skipped, nil
}
