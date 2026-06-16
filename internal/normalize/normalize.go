// Package normalize produces stable lookup keys from free-text track metadata.
//
// Normalization is deliberately conservative: it folds case and whitespace so
// trivial spelling variants collide, but it preserves version qualifiers like
// "(Live)" or "Remastered" — those are genuinely different recordings with
// different audio features, so they must not be merged.
package normalize

import "strings"

// fieldSep separates the title and artist in a composite Key. A unit separator
// (0x1f) can't appear in normal metadata, so it can't be forged by the inputs.
const fieldSep = "\x1f"

// Field normalizes a single metadata field: lowercased, trimmed, with internal
// whitespace runs collapsed to a single space.
func Field(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// Key builds the composite cache key for a (title, artist) pair.
func Key(title, artist string) string {
	return Field(title) + fieldSep + Field(artist)
}
