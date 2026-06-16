package camelot

import (
	"errors"
	"slices"
	"sort"
	"testing"
)

func TestCode_AllKeys(t *testing.T) {
	// key uses the Spotify pitch-class convention: 0=C, 1=C#/Db, ... 11=B.
	cases := []struct {
		key  int
		mode int
		want string
	}{
		// Major (mode=1)
		{0, 1, "8B"}, {1, 1, "3B"}, {2, 1, "10B"}, {3, 1, "5B"},
		{4, 1, "12B"}, {5, 1, "7B"}, {6, 1, "2B"}, {7, 1, "9B"},
		{8, 1, "4B"}, {9, 1, "11B"}, {10, 1, "6B"}, {11, 1, "1B"},
		// Minor (mode=0)
		{0, 0, "5A"}, {1, 0, "12A"}, {2, 0, "7A"}, {3, 0, "2A"},
		{4, 0, "9A"}, {5, 0, "4A"}, {6, 0, "11A"}, {7, 0, "6A"},
		{8, 0, "1A"}, {9, 0, "8A"}, {10, 0, "3A"}, {11, 0, "10A"},
	}
	for _, c := range cases {
		got, err := Code(c.key, c.mode)
		if err != nil {
			t.Errorf("Code(%d,%d) unexpected error: %v", c.key, c.mode, err)
			continue
		}
		if got != c.want {
			t.Errorf("Code(%d,%d) = %q, want %q", c.key, c.mode, got, c.want)
		}
	}
}

func TestCode_InvalidInput(t *testing.T) {
	if _, err := Code(-1, 1); !errors.Is(err, ErrUnknownKey) {
		t.Errorf("Code(-1,1) error = %v, want ErrUnknownKey", err)
	}
	if _, err := Code(12, 1); !errors.Is(err, ErrUnknownKey) {
		t.Errorf("Code(12,1) error = %v, want ErrUnknownKey", err)
	}
	if _, err := Code(0, 2); !errors.Is(err, ErrInvalidMode) {
		t.Errorf("Code(0,2) error = %v, want ErrInvalidMode", err)
	}
}

func TestCompatible_MidWheel(t *testing.T) {
	got := Compatible("8A")
	want := []string{"8A", "7A", "9A", "8B"}
	if !sameSet(got, want) {
		t.Errorf("Compatible(8A) = %v, want set %v", got, want)
	}
}

func TestCompatible_WrapsAround(t *testing.T) {
	if !slices.Contains(Compatible("1A"), "12A") {
		t.Errorf("Compatible(1A) = %v, want it to contain 12A", Compatible("1A"))
	}
	if !slices.Contains(Compatible("12B"), "1B") {
		t.Errorf("Compatible(12B) = %v, want it to contain 1B", Compatible("12B"))
	}
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	as := append([]string(nil), a...)
	bs := append([]string(nil), b...)
	sort.Strings(as)
	sort.Strings(bs)
	return slices.Equal(as, bs)
}
