// Package matcher picks the best catalog search result for a query.
//
// It is pure: no network, no I/O, no global state. Everything is decided
// from the arguments, which makes it trivially table-testable.
package matcher

import (
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// Candidate is one search result, reduced to what matching needs.
type Candidate struct {
	Artist string
	Title  string
}

// junkPatterns mark cover/karaoke uploads that search often ranks above the
// original. Stored already normalised.
var junkPatterns = []string{"karaoke", "tribute", "made famous by", "in the style of"}

// Scores. An exact artist outweighs any title evidence, so "right artist,
// slightly different title" beats "right title, different artist credit".
const (
	artistExact   = 4
	artistPartial = 2
	titleExact    = 2
	titlePartial  = 1
)

// Best returns the index in candidates of the best match for the query, and
// false if none is acceptable. artist may be empty, meaning title is a
// free-form search term (a line without " - ").
//
// Ties keep the earlier candidate, i.e. the API's relevance order.
func Best(artist, title string, candidates []Candidate) (int, bool) {
	qArtist, qTitle := Normalize(artist), Normalize(title)
	queryIsJunk := isJunk(qArtist) || isJunk(qTitle)

	best, bestScore := -1, 0
	for i, c := range candidates {
		cArtist, cTitle := Normalize(c.Artist), Normalize(c.Title)
		if !queryIsJunk && (isJunk(cArtist) || isJunk(cTitle)) {
			continue
		}

		var s int
		if qArtist == "" {
			s = scoreTerm(qTitle, cArtist, cTitle)
		} else {
			s = score(qArtist, qTitle, cArtist, cTitle)
		}
		// Strictly greater: an equal score never displaces an earlier result.
		if s > bestScore {
			best, bestScore = i, s
		}
	}
	return best, best >= 0
}

// score rates a candidate against an "Artist - Title" query. Both the
// artist and the title must match at least partially, otherwise 0.
func score(qArtist, qTitle, cArtist, cTitle string) int {
	var a, t int
	switch {
	case qArtist == cArtist:
		a = artistExact
	case containsWords(cArtist, qArtist) || containsWords(qArtist, cArtist):
		a = artistPartial // "Björk" vs "Björk & Thom Yorke"
	default:
		return 0
	}
	switch {
	case qTitle == cTitle:
		t = titleExact
	case containsWords(cTitle, qTitle) || containsWords(qTitle, cTitle):
		t = titlePartial // "Glory Box" vs "Glory Box (Remastered)"
	default:
		return 0
	}
	return a + t
}

// scoreTerm rates a candidate against a free-form term. The candidate's
// title must appear in the term; its artist appearing too scores higher.
func scoreTerm(term, cArtist, cTitle string) int {
	if cTitle == "" || !containsWords(term, cTitle) {
		return 0
	}
	s := titlePartial
	if cArtist != "" && containsWords(term, cArtist) {
		s += artistPartial
	}
	return s
}

// containsWords reports whether needle occurs in haystack on word
// boundaries, so "me" matches "army of me" but not "home".
func containsWords(haystack, needle string) bool {
	if needle == "" {
		return false
	}
	return strings.Contains(" "+haystack+" ", " "+needle+" ")
}

func isJunk(s string) bool {
	for _, p := range junkPatterns {
		if containsWords(s, p) {
			return true
		}
	}
	return false
}

// foldExtra covers letters that are distinct characters rather than a base
// letter plus an accent, so Unicode decomposition alone leaves them as is.
var foldExtra = strings.NewReplacer(
	"ø", "o", "æ", "ae", "œ", "oe", "ß", "ss", "ł", "l", "đ", "d", "ð", "d", "þ", "th", "ı", "i",
)

// Normalize makes two spellings of the same name compare equal: lowercase,
// no diacritics, punctuation turned into spaces, whitespace collapsed.
// "Björk" and "BJORK!" both become "bjork".
func Normalize(s string) string {
	// NFD splits "ö" into "o" + a combining diaeresis (Unicode category Mn,
	// "nonspacing mark"); runes.Remove then drops every such mark.
	// A Transformer carries state, so build a fresh chain per call rather
	// than sharing a package-level one.
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	folded, _, err := transform.String(t, strings.ToLower(s))
	if err != nil {
		// Only possible on invalid UTF-8; fall back to the lowercase input
		// rather than losing the string.
		folded = strings.ToLower(s)
	}
	folded = foldExtra.Replace(folded)

	// strings.Fields splits on any run of whitespace and drops empties, so
	// joining with one space both collapses and trims.
	return strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return ' '
	}, folded)), " ")
}
