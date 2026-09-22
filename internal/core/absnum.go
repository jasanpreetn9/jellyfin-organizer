package core

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// FillerMark is the tag inserted at the start of filler episode titles.
const FillerMark = "(FILLER)"

var (
	// Matches " - S02E21 - 041 - " and captures 041 (absolute number
	// between the SxxExx block and the following dash).
	absFromSxxExx = regexp.MustCompile(`(?i)-\s*S\d{1,3}E\d{1,4}\s*-\s*(\d{1,4})\s*-`)
	// Fallback: any " - 041 - " pattern.
	absGeneric = regexp.MustCompile(`-\s*(\d{1,4})\s*-`)
)

// BaseName returns the final path element, splitting on both / and \
// so paths reported by Linux or Windows Jellyfin servers both work.
func BaseName(path string) string {
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		return path[i+1:]
	}
	return path
}

// ExtractAbsoluteNumber parses the absolute (release-order) episode number
// from a media file path. Returns 0 when no number can be found.
func ExtractAbsoluteNumber(path string) int {
	name := BaseName(path)
	if name == "" {
		return 0
	}
	if m := absFromSxxExx.FindStringSubmatch(name); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	if m := absGeneric.FindStringSubmatch(name); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 0
}

// ParseRanges converts a string like "26, 97, 101-106" into a set of ints.
func ParseRanges(s string) (map[int]bool, error) {
	set := map[int]bool{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if a, b, found := strings.Cut(part, "-"); found {
			start, err1 := strconv.Atoi(strings.TrimSpace(a))
			end, err2 := strconv.Atoi(strings.TrimSpace(b))
			if err1 != nil || err2 != nil || start > end {
				return nil, fmt.Errorf("invalid range %q", part)
			}
			for n := start; n <= end; n++ {
				set[n] = true
			}
		} else {
			n, err := strconv.Atoi(part)
			if err != nil {
				return nil, fmt.Errorf("invalid number %q", part)
			}
			set[n] = true
		}
	}
	return set, nil
}

// AddFillerPrefix puts "(FILLER) " at the start of a title.
// Titles that already carry the mark anywhere are returned unchanged.
func AddFillerPrefix(title string) string {
	if strings.Contains(title, FillerMark) {
		return title
	}
	return FillerMark + " " + title
}

// RemoveFillerTag strips the "(FILLER) " prefix as well as the legacy
// " (FILLER)" suffix written by the older Python scripts.
func RemoveFillerTag(title string) string {
	t := strings.TrimSpace(title)
	t = strings.TrimPrefix(t, FillerMark)
	t = strings.TrimSuffix(t, FillerMark)
	return strings.TrimSpace(t)
}
