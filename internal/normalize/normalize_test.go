package normalize

import "testing"

func TestField(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"  The  POSTAL  Service ", "the postal service"},
		{"Song\t\nName", "song name"},
		{"Such Great Heights", "such great heights"},
		{"Song (Live)", "song (live)"},                         // version qualifier preserved
		{"Track - Remastered 2011", "track - remastered 2011"}, // preserved
		{"", ""},
	}
	for _, c := range cases {
		if got := Field(c.in); got != c.want {
			t.Errorf("Field(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestKey_CaseAndWhitespaceInsensitive(t *testing.T) {
	if Key("Song", "Artist") != Key("  song ", "ARTIST") {
		t.Errorf("keys differ for case/whitespace variants")
	}
}

func TestKey_DistinguishesVersionQualifiers(t *testing.T) {
	if Key("Song", "Artist") == Key("Song (Live)", "Artist") {
		t.Errorf("a version qualifier must produce a distinct key")
	}
}

func TestKey_DistinguishesTitleVsArtist(t *testing.T) {
	// Swapping title/artist must not collide (separator guards against this).
	if Key("a", "b") == Key("b", "a") {
		t.Errorf("title/artist boundary is ambiguous")
	}
}
