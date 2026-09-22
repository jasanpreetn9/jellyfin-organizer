package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"jellyfin-organizer/internal/core"
)

// sonarrPayload is the subset of Sonarr's webhook body we care about.
type sonarrPayload struct {
	EventType string `json:"eventType"`
	Series    struct {
		Title string `json:"title"`
		Path  string `json:"path"`
	} `json:"series"`
}

// handleSonarrWebhook receives Sonarr "Connect → Webhook" notifications.
// Any event that names a series schedules a debounced re-check of that
// series; Test events are acknowledged without doing anything.
func (s *Server) handleSonarrWebhook(w http.ResponseWriter, r *http.Request) {
	if token := s.config().WebhookToken; token != "" &&
		subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("token")), []byte(token)) != 1 {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}
	var p sonarrPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "bad payload: "+err.Error(), http.StatusBadRequest)
		return
	}
	if p.EventType == "Test" {
		log.Printf("webhook: Sonarr test received")
		writeJSON(w, http.StatusOK, map[string]string{"status": "test ok"})
		return
	}
	if p.Series.Title == "" {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored (no series in payload)"})
		return
	}
	delay := s.webhookDelay()
	log.Printf("webhook: %s for %q — re-check in %s", p.EventType, p.Series.Title, delay)
	s.scheduleRecheck(p.Series.Title, p.Series.Path)
	writeJSON(w, http.StatusOK, map[string]string{
		"status": fmt.Sprintf("re-check of %q scheduled in %s", p.Series.Title, delay),
	})
}

// scheduleRecheck debounces per series title: imports arrive in bursts and
// Jellyfin needs time to scan the new files, so each event pushes the timer
// back by WebhookDelay. The title/path are refreshed on every event so a
// burst that changes the path (e.g. Sonarr's On Rename) isn't lost to a
// stale closure from the first event.
func (s *Server) scheduleRecheck(title, path string) {
	key := normalizeTitle(title)
	delay := s.webhookDelay()
	s.debounceMu.Lock()
	defer s.debounceMu.Unlock()
	if e, ok := s.debounce[key]; ok {
		e.title, e.path = title, path
		e.timer.Reset(delay)
		return
	}
	e := &debounceEntry{title: title, path: path}
	e.timer = time.AfterFunc(delay, func() {
		s.debounceMu.Lock()
		delete(s.debounce, key)
		t, p := e.title, e.path
		s.debounceMu.Unlock()
		s.recheckSeries(t, p)
	})
	s.debounce[key] = e
}

// recheckSeries runs the saved rules for a series, or a plain set-absolute
// pass when no rule exists.
func (s *Server) recheckSeries(title, path string) {
	label := fmt.Sprintf("Sonarr: %s", title)
	if rules := s.rules.MatchByName(title); len(rules) > 0 {
		nfo := false
		var jobs []core.Job
		for _, r := range rules {
			if !r.Auto || r.Skipped {
				continue
			}
			jobs = append(jobs, r.Job())
			nfo = nfo || r.NFORefresh
		}
		if len(jobs) == 0 {
			log.Printf("webhook: %q has rules but all are manual-only or skipped — skipping", title)
			return
		}
		log.Printf("webhook: running %d saved rule(s) for %q", len(jobs), title)
		s.runs.Start("webhook", label, jobs, core.Options{DryRun: false, NFORefresh: nfo})
		return
	}

	// No saved rule: fall back to set_absolute with the default user.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	userID, err := s.defaultUserID(ctx)
	if err != nil {
		log.Printf("webhook: %q skipped: %v", title, err)
		return
	}
	series, err := s.findSeries(ctx, title, path)
	if err != nil {
		log.Printf("webhook: %q skipped: %v", title, err)
		return
	}
	log.Printf("webhook: no saved rule for %q — running set_absolute on %q", title, series.Name)
	s.runs.Start("webhook", label, []core.Job{{
		UserID:     userID,
		SeriesID:   series.ID,
		SeriesName: series.Name,
		Action:     core.ActionSetAbsolute,
	}}, core.Options{DryRun: false, NFORefresh: true})
}

// defaultUserID resolves the configured username, or falls back to the
// first user on the server.
func (s *Server) defaultUserID(ctx context.Context) (string, error) {
	users, err := s.jf.Users(ctx)
	if err != nil {
		return "", fmt.Errorf("list users: %w", err)
	}
	if len(users) == 0 {
		return "", fmt.Errorf("jellyfin has no users")
	}
	if want := s.config().Username; want != "" {
		for _, u := range users {
			if u.Name == want {
				return u.ID, nil
			}
		}
		return "", fmt.Errorf("configured username %q not found on server", want)
	}
	return users[0].ID, nil
}

// findSeries maps a Sonarr series (title + disk path) to a Jellyfin series:
// exact folder match wins, then exact title match, then a single fuzzy hit.
func (s *Server) findSeries(ctx context.Context, title, path string) (*jellyfinSeriesRef, error) {
	results, err := s.jf.SearchSeries(ctx, title)
	if err != nil {
		return nil, fmt.Errorf("search series: %w", err)
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("no Jellyfin series matches %q (has Jellyfin scanned it yet?)", title)
	}
	folder := core.BaseName(path)
	for _, r := range results {
		if folder != "" && core.BaseName(r.Path) == folder {
			return &jellyfinSeriesRef{ID: r.ID, Name: r.Name}, nil
		}
	}
	want := normalizeTitle(title)
	for _, r := range results {
		if normalizeTitle(r.Name) == want {
			return &jellyfinSeriesRef{ID: r.ID, Name: r.Name}, nil
		}
	}
	if len(results) == 1 {
		return &jellyfinSeriesRef{ID: results[0].ID, Name: results[0].Name}, nil
	}
	return nil, fmt.Errorf("%d ambiguous Jellyfin matches for %q", len(results), title)
}

type jellyfinSeriesRef struct {
	ID   string
	Name string
}
