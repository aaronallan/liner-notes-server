// Package camelot maps musical key/mode to Camelot-wheel codes and reports
// which codes mix harmonically. It is pure (standard library only) so it can be
// reused by the corpus store and the mix-match endpoint and exhaustively tested.
//
// Input keys use the Spotify pitch-class convention: 0=C, 1=C#/Db, 2=D,
// 3=D#/Eb, 4=E, 5=F, 6=F#/Gb, 7=G, 8=G#/Ab, 9=A, 10=A#/Bb, 11=B. Mode is
// 1=major, 0=minor. A Camelot code is a wheel number (1–12) plus a letter
// (B=major, A=minor), e.g. C major = 8B, A minor = 8A.
package camelot

import (
	"errors"
	"fmt"
	"strconv"
)

// ErrUnknownKey is returned by Code when the key is outside 0–11 (ReccoBeats and
// Spotify use -1 to mean "key unknown").
var ErrUnknownKey = errors.New("camelot: unknown key")

// ErrInvalidMode is returned by Code when the mode is neither 0 (minor) nor 1 (major).
var ErrInvalidMode = errors.New("camelot: invalid mode")

// majorWheel maps a Spotify pitch class to its Camelot wheel number for the
// major (B) ring; minorWheel does the same for the minor (A) ring.
var (
	majorWheel = [12]int{8, 3, 10, 5, 12, 7, 2, 9, 4, 11, 6, 1}
	minorWheel = [12]int{5, 12, 7, 2, 9, 4, 11, 6, 1, 8, 3, 10}
)

// Code returns the Camelot code for a key/mode, e.g. Code(9, 0) == "8A".
func Code(key, mode int) (string, error) {
	if key < 0 || key > 11 {
		return "", fmt.Errorf("%w: %d", ErrUnknownKey, key)
	}
	switch mode {
	case 1:
		return fmt.Sprintf("%d%s", majorWheel[key], "B"), nil
	case 0:
		return fmt.Sprintf("%d%s", minorWheel[key], "A"), nil
	default:
		return "", fmt.Errorf("%w: %d", ErrInvalidMode, mode)
	}
}

// Compatible returns the harmonically compatible codes for a Camelot code: the
// code itself, its neighbours ±1 on the wheel (wrapping 1↔12, same letter), and
// its relative major/minor (same number, other letter). It returns nil for a
// malformed code.
func Compatible(code string) []string {
	num, letter, ok := parse(code)
	if !ok {
		return nil
	}

	other := "A"
	if letter == "A" {
		other = "B"
	}

	return []string{
		code,
		fmt.Sprintf("%d%s", wrap(num-1), letter),
		fmt.Sprintf("%d%s", wrap(num+1), letter),
		fmt.Sprintf("%d%s", num, other),
	}
}

// wrap keeps a wheel number within 1–12, wrapping at the edges.
func wrap(n int) int {
	switch {
	case n < 1:
		return n + 12
	case n > 12:
		return n - 12
	default:
		return n
	}
}

// parse splits a Camelot code into its number (1–12) and letter (A/B).
func parse(code string) (num int, letter string, ok bool) {
	if len(code) < 2 {
		return 0, "", false
	}
	letter = code[len(code)-1:]
	if letter != "A" && letter != "B" {
		return 0, "", false
	}
	num, err := strconv.Atoi(code[:len(code)-1])
	if err != nil || num < 1 || num > 12 {
		return 0, "", false
	}
	return num, letter, true
}
