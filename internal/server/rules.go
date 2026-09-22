package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"jellyfin-organizer/internal/core"
)

// Rule is a saved job: what to do for a series. Rules power both the batch
// queue ("run all") and the Sonarr webhook (re-apply on library changes).
type Rule struct {
	ID           string      `json:"id"`
	UserID       string      `json:"userId"`
	SeriesID     string      `json:"seriesId"`
	SeriesName   string      `json:"seriesName"`
	Action       core.Action `json:"action"`
	FillerRanges string      `json:"fillerRanges"`
	SkipRanges   string      `json:"skipRanges"`
	NFORefresh   bool        `json:"nfoRefresh"`
	// Auto rules run on the Sonarr webhook and the regular schedule;
	// non-auto rules only run when triggered manually.
	Auto bool `json:"auto"`
	// Skipped rules are left out of "Run all", the schedule, and the Sonarr
	// webhook — a way to pause a rule without losing its saved settings.
	// Running it individually (the rule's own ▶ button) still works.
	Skipped bool `json:"skipped"`
}

func (r Rule) Job() core.Job {
	return core.Job{
		UserID:       r.UserID,
		SeriesID:     r.SeriesID,
		SeriesName:   r.SeriesName,
		Action:       r.Action,
		FillerRanges: r.FillerRanges,
		SkipRanges:   r.SkipRanges,
	}
}

type ruleStore struct {
	mu    sync.Mutex
	path  string
	rules []Rule
	seq   int
}

func newRuleStore(dataDir string) (*ruleStore, error) {
	s := &ruleStore{path: filepath.Join(dataDir, "rules.json")}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, &s.rules); err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.path, err)
	}
	return s, nil
}

// save persists to disk; caller must hold s.mu.
func (s *ruleStore) save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.rules, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o644)
}

func (s *ruleStore) List() []Rule {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Rule(nil), s.rules...)
}

// Upsert creates (empty ID) or replaces (existing ID) a rule.
func (s *ruleStore) Upsert(r Rule) (Rule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.ID == "" {
		s.seq++
		r.ID = fmt.Sprintf("rule-%d-%d", time.Now().Unix(), s.seq)
		s.rules = append(s.rules, r)
	} else {
		found := false
		for i := range s.rules {
			if s.rules[i].ID == r.ID {
				s.rules[i] = r
				found = true
				break
			}
		}
		if !found {
			s.rules = append(s.rules, r)
		}
	}
	return r, s.save()
}

func (s *ruleStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.rules {
		if s.rules[i].ID == id {
			s.rules = append(s.rules[:i], s.rules[i+1:]...)
			return s.save()
		}
	}
	return fmt.Errorf("rule %s not found", id)
}

func (s *ruleStore) Get(id string) (Rule, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.rules {
		if r.ID == id {
			return r, true
		}
	}
	return Rule{}, false
}

var yearSuffix = regexp.MustCompile(`\s*\(\d{4}\)\s*$`)

// normalizeTitle lowercases and strips a trailing "(2004)" so Sonarr's
// "Bleach" matches Jellyfin's "Bleach (2004)".
func normalizeTitle(t string) string {
	return strings.ToLower(strings.TrimSpace(yearSuffix.ReplaceAllString(t, "")))
}

// MatchByName returns rules whose series name matches the given title.
func (s *ruleStore) MatchByName(title string) []Rule {
	want := normalizeTitle(title)
	if want == "" {
		return nil
	}
	var out []Rule
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.rules {
		if normalizeTitle(r.SeriesName) == want {
			out = append(out, r)
		}
	}
	return out
}
