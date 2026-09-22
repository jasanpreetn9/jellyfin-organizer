package core

import "testing"

func TestExtractAbsoluteNumber(t *testing.T) {
	cases := []struct {
		path string
		want int
	}{
		{
			path: "/media/anime/Bleach (2004)/Season 02/Bleach (2004) - S02E21 - 041 - Reunion Ichigo and Rukia [SDTV] [AnimeRG] [EN+JA].mkv",
			want: 41,
		},
		{
			path: `D:\anime\Bleach (2004)\Season 02\Bleach (2004) - S02E21 - 041 - Reunion [SDTV].mkv`,
			want: 41,
		},
		{
			// Generic fallback: no SxxExx block before the number.
			path: "/media/anime/Naruto/Naruto - 026 - Special Report.mkv",
			want: 26,
		},
		{
			// SxxExx pattern wins over the generic one.
			path: "Show - S01E05 - 005 - Title - 999 - extra.mkv",
			want: 5,
		},
		{
			// Bracketed ranges have no trailing dash, so no match —
			// and the "23 - " in the directory name must not leak in.
			path: "/media/One Pace/23 - Amazon Lily/[One Pace][514-515] Amazon Lily 01 [720p][06BF070D].mp4",
			want: 0,
		},
		{path: "no numbers here.mkv", want: 0},
		{path: "", want: 0},
	}
	for _, c := range cases {
		if got := ExtractAbsoluteNumber(c.path); got != c.want {
			t.Errorf("ExtractAbsoluteNumber(%q) = %d, want %d", c.path, got, c.want)
		}
	}
}

func TestParseRanges(t *testing.T) {
	set, err := ParseRanges("26, 97, 101-106")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, n := range []int{26, 97, 101, 102, 103, 104, 105, 106} {
		if !set[n] {
			t.Errorf("expected %d in set", n)
		}
	}
	if len(set) != 8 {
		t.Errorf("expected 8 entries, got %d", len(set))
	}
	if set, err := ParseRanges(""); err != nil || len(set) != 0 {
		t.Errorf("empty input should give empty set, got %v, %v", set, err)
	}
	if _, err := ParseRanges("5-1"); err == nil {
		t.Error("descending range should error")
	}
	if _, err := ParseRanges("abc"); err == nil {
		t.Error("non-numeric should error")
	}
}

func TestFillerTag(t *testing.T) {
	if got := AddFillerPrefix("Reunion"); got != "(FILLER) Reunion" {
		t.Errorf("AddFillerPrefix = %q", got)
	}
	if got := AddFillerPrefix("(FILLER) Reunion"); got != "(FILLER) Reunion" {
		t.Errorf("AddFillerPrefix should be idempotent, got %q", got)
	}
	if got := AddFillerPrefix("Reunion (FILLER)"); got != "Reunion (FILLER)" {
		t.Errorf("AddFillerPrefix should not double-tag legacy titles, got %q", got)
	}
	if got := RemoveFillerTag("(FILLER) Reunion"); got != "Reunion" {
		t.Errorf("RemoveFillerTag prefix = %q", got)
	}
	if got := RemoveFillerTag("Reunion (FILLER)"); got != "Reunion" {
		t.Errorf("RemoveFillerTag legacy suffix = %q", got)
	}
	if got := RemoveFillerTag("Reunion"); got != "Reunion" {
		t.Errorf("RemoveFillerTag untagged = %q", got)
	}
}
