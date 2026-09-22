// Package server exposes the web GUI and the JSON API.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"jellyfin-organizer/internal/config"
	"jellyfin-organizer/internal/core"
	"jellyfin-organizer/internal/jellyfin"
)

type Server struct {
	jf      *jellyfin.Client
	dataDir string
	runs    *runStore
	rules   *ruleStore
	mux     *http.ServeMux

	cfgMu      sync.RWMutex
	cfg        config.File
	cfgChanged chan struct{}

	debounceMu sync.Mutex
	debounce   map[string]*debounceEntry
}

// debounceEntry tracks the pending recheck for one series: the timer, plus
// the most recent title/path seen (updated on every event so a burst that
// changes the path, e.g. Sonarr's On Rename, isn't lost to a stale closure).
type debounceEntry struct {
	timer *time.Timer
	title string
	path  string
}

func New(jf *jellyfin.Client, webRoot fs.FS, dataDir string, cfg config.File) (*Server, error) {
	rules, err := newRuleStore(dataDir)
	if err != nil {
		return nil, err
	}
	s := &Server{
		jf:         jf,
		dataDir:    dataDir,
		cfg:        cfg,
		cfgChanged: make(chan struct{}, 1),
		runs:       newRunStore(jf),
		rules:      rules,
		mux:        http.NewServeMux(),
		debounce:   map[string]*debounceEntry{},
	}

	s.mux.HandleFunc("GET /api/health", s.handleHealth)
	s.mux.HandleFunc("GET /api/config", s.handleGetConfig)
	s.mux.HandleFunc("POST /api/config", s.handleSetConfig)
	s.mux.HandleFunc("GET /api/users", s.handleUsers)
	s.mux.HandleFunc("GET /api/users/{userId}/views", s.handleViews)
	s.mux.HandleFunc("GET /api/users/{userId}/views/{viewId}/series", s.handleSeries)
	s.mux.HandleFunc("GET /api/series/{seriesId}/episodes", s.handleEpisodes)
	s.mux.HandleFunc("GET /api/users/{userId}/items/{itemId}", s.handleGetItem)
	s.mux.HandleFunc("POST /api/items/{itemId}/title", s.handleSetTitle)
	s.mux.HandleFunc("POST /api/runs", s.handleCreateRun)
	s.mux.HandleFunc("GET /api/runs", s.handleListRuns)
	s.mux.HandleFunc("GET /api/runs/{runId}", s.handleGetRun)
	s.mux.HandleFunc("GET /api/rules", s.handleListRules)
	s.mux.HandleFunc("POST /api/rules", s.handleUpsertRule)
	s.mux.HandleFunc("DELETE /api/rules/{ruleId}", s.handleDeleteRule)
	s.mux.HandleFunc("POST /api/rules/{ruleId}/run", s.handleRunRule)
	s.mux.HandleFunc("POST /webhook/sonarr", s.handleSonarrWebhook)
	s.mux.Handle("GET /", http.FileServerFS(webRoot))

	go s.scheduleLoop()
	return s, nil
}

// config returns the current effective configuration.
func (s *Server) config() config.File {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.cfg
}

func (s *Server) webhookDelay() time.Duration {
	return time.Duration(s.config().WebhookDelaySeconds) * time.Second
}

// scheduleLoop regularly re-runs every auto rule, so shows stay fixed even
// when a webhook was missed (or Sonarr isn't set up at all). The interval
// can change (or be disabled) at runtime via the settings GUI.
func (s *Server) scheduleLoop() {
	for {
		interval := time.Duration(s.config().ScheduleIntervalMinutes) * time.Minute
		if interval <= 0 {
			<-s.cfgChanged // disabled: sleep until settings change
			continue
		}
		select {
		case <-time.After(interval):
			s.runScheduledRules()
		case <-s.cfgChanged: // interval may have changed: restart the wait
		}
	}
}

func (s *Server) runScheduledRules() {
	var jobs []core.Job
	nfo := false
	for _, r := range s.rules.List() {
		if !r.Auto || r.Skipped {
			continue
		}
		jobs = append(jobs, r.Job())
		nfo = nfo || r.NFORefresh
	}
	if len(jobs) == 0 {
		return
	}
	log.Printf("schedule: running %d auto rule(s)", len(jobs))
	s.runs.Start("schedule", fmt.Sprintf("scheduled (%d rules)", len(jobs)), jobs,
		core.Options{DryRun: false, NFORefresh: nfo})
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return false
	}
	return true
}

// ---- browse handlers ----

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{
		"configured":      s.jf.Configured(),
		"jellyfin":        s.jf.BaseURL(),
		"scheduleMinutes": s.config().ScheduleIntervalMinutes,
	}
	if s.jf.Configured() {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if info, err := s.jf.SystemInfo(ctx); err != nil {
			out["connected"] = false
			out["error"] = err.Error()
		} else {
			out["connected"] = true
			out["serverName"] = info.ServerName
			out["version"] = info.Version
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleGetConfig reports the current settings. Secrets are never echoed
// back — only whether they are set.
func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	cfg := s.config()
	writeJSON(w, http.StatusOK, map[string]any{
		"jellyfinUrl":             cfg.JellyfinURL,
		"apiKeySet":               cfg.APIKey != "",
		"username":                cfg.Username,
		"port":                    cfg.Port,
		"webhookDelaySeconds":     cfg.WebhookDelaySeconds,
		"webhookTokenSet":         cfg.WebhookToken != "",
		"scheduleIntervalMinutes": cfg.ScheduleIntervalMinutes,
		"configPath":              config.Path(s.dataDir),
	})
}

// handleSetConfig updates config.json and applies the settings immediately
// (except port, which needs a restart). Omitted/null fields keep their
// current value, so the GUI never has to resend the API key.
func (s *Server) handleSetConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		JellyfinURL             *string `json:"jellyfinUrl"`
		APIKey                  *string `json:"apiKey"`
		Username                *string `json:"username"`
		Port                    *string `json:"port"`
		WebhookDelaySeconds     *int    `json:"webhookDelaySeconds"`
		WebhookToken            *string `json:"webhookToken"`
		ScheduleIntervalMinutes *int    `json:"scheduleIntervalMinutes"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.JellyfinURL != nil {
		u := strings.TrimSpace(*req.JellyfinURL)
		if u != "" && !strings.Contains(u, "://") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "jellyfinUrl must include http:// or https://"})
			return
		}
		*req.JellyfinURL = u
	}
	if req.Port != nil {
		p := strings.TrimSpace(*req.Port)
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "port must be a number between 1 and 65535"})
			return
		}
		*req.Port = p
	}
	if req.WebhookDelaySeconds != nil && *req.WebhookDelaySeconds < 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "webhookDelaySeconds must be >= 0"})
		return
	}
	if req.ScheduleIntervalMinutes != nil && *req.ScheduleIntervalMinutes < 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "scheduleIntervalMinutes must be >= 0"})
		return
	}

	s.cfgMu.Lock()
	if req.JellyfinURL != nil {
		s.cfg.JellyfinURL = *req.JellyfinURL
	}
	if req.APIKey != nil {
		s.cfg.APIKey = strings.TrimSpace(*req.APIKey)
	}
	if req.Username != nil {
		s.cfg.Username = strings.TrimSpace(*req.Username)
	}
	if req.Port != nil {
		s.cfg.Port = *req.Port
	}
	if req.WebhookDelaySeconds != nil {
		s.cfg.WebhookDelaySeconds = *req.WebhookDelaySeconds
	}
	if req.WebhookToken != nil {
		s.cfg.WebhookToken = strings.TrimSpace(*req.WebhookToken)
	}
	if req.ScheduleIntervalMinutes != nil {
		s.cfg.ScheduleIntervalMinutes = *req.ScheduleIntervalMinutes
	}
	err := s.cfg.Save(s.dataDir)
	cfg := s.cfg
	s.cfgMu.Unlock()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	s.jf.SetCredentials(cfg.JellyfinURL, cfg.APIKey)
	select { // wake the scheduler so a new interval takes effect
	case s.cfgChanged <- struct{}{}:
	default:
	}
	log.Printf("settings updated (%s saved)", config.Path(s.dataDir))
	s.handleGetConfig(w, r)
}

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.jf.Users(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	out := make([]map[string]string, 0, len(users))
	for _, u := range users {
		out = append(out, map[string]string{"id": u.ID, "name": u.Name})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleViews(w http.ResponseWriter, r *http.Request) {
	views, err := s.jf.Views(r.Context(), r.PathValue("userId"))
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	out := make([]map[string]string, 0, len(views))
	for _, v := range views {
		ct := v.CollectionType
		if ct == "" {
			ct = "unknown"
		}
		out = append(out, map[string]string{"id": v.ID, "name": v.Name, "collectionType": ct})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleSeries(w http.ResponseWriter, r *http.Request) {
	series, err := s.jf.Series(r.Context(), r.PathValue("viewId"))
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	if q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("search"))); q != "" {
		filtered := series[:0]
		for _, sr := range series {
			if strings.Contains(strings.ToLower(sr.Name), q) {
				filtered = append(filtered, sr)
			}
		}
		series = filtered
	}
	sort.Slice(series, func(i, j int) bool {
		if series[i].Name != series[j].Name {
			return series[i].Name < series[j].Name
		}
		return series[i].ProductionYear < series[j].ProductionYear
	})
	out := make([]map[string]any, 0, len(series))
	for _, sr := range series {
		out = append(out, map[string]any{"id": sr.ID, "name": sr.Name, "year": sr.ProductionYear})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleEpisodes(w http.ResponseWriter, r *http.Request) {
	eps, err := s.jf.Episodes(r.Context(), r.PathValue("seriesId"))
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	core.SortEpisodes(eps)
	out := make([]map[string]any, 0, len(eps))
	for _, ep := range eps {
		abs := core.ExtractAbsoluteNumber(ep.Path)
		item := map[string]any{
			"id":       ep.ID,
			"title":    ep.Name,
			"season":   ep.Season,
			"episode":  ep.Index,
			"absolute": abs,
			"fileName": core.BaseName(ep.Path),
			"isFiller": strings.Contains(ep.Name, core.FillerMark),
			"indexMismatch": abs > 0 &&
				(ep.Index == nil || *ep.Index != abs),
		}
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleGetItem returns an item's id + name (used by the batch CSV import
// to resolve a series ID into its display name).
func (s *Server) handleGetItem(w http.ResponseWriter, r *http.Request) {
	item, err := s.jf.ItemForEdit(r.Context(), r.PathValue("userId"), r.PathValue("itemId"))
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": item["Id"], "name": item["Name"]})
}

// ---- edit handlers ----

func (s *Server) handleSetTitle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID string `json:"userId"`
		Title  string `json:"title"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.UserID == "" || strings.TrimSpace(req.Title) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "userId and title are required"})
		return
	}
	if err := core.RenameEpisode(r.Context(), s.jf, req.UserID, r.PathValue("itemId"), strings.TrimSpace(req.Title)); err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---- run handlers ----

func (s *Server) handleCreateRun(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DryRun     bool       `json:"dryRun"`
		NFORefresh bool       `json:"nfoRefresh"`
		Label      string     `json:"label"`
		Jobs       []core.Job `json:"jobs"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if len(req.Jobs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no jobs given"})
		return
	}
	for _, j := range req.Jobs {
		if !j.Action.Valid() {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown action: " + string(j.Action)})
			return
		}
		if j.UserID == "" || j.SeriesID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "every job needs userId and seriesId"})
			return
		}
	}
	label := req.Label
	if label == "" {
		if len(req.Jobs) == 1 {
			label = req.Jobs[0].SeriesName
		} else {
			label = "batch"
		}
	}
	run := s.runs.Start("manual", label, req.Jobs, core.Options{DryRun: req.DryRun, NFORefresh: req.NFORefresh})
	writeJSON(w, http.StatusOK, map[string]string{"runId": run.ID})
}

func (s *Server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.runs.List())
}

func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	run := s.runs.Get(r.PathValue("runId"))
	if run == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "run not found"})
		return
	}
	writeJSON(w, http.StatusOK, run)
}

// ---- rule handlers ----

func (s *Server) handleListRules(w http.ResponseWriter, r *http.Request) {
	rules := s.rules.List()
	if rules == nil {
		rules = []Rule{}
	}
	writeJSON(w, http.StatusOK, rules)
}

func (s *Server) handleUpsertRule(w http.ResponseWriter, r *http.Request) {
	var rule Rule
	if !readJSON(w, r, &rule) {
		return
	}
	if !rule.Action.Valid() {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown action: " + string(rule.Action)})
		return
	}
	if rule.UserID == "" || rule.SeriesID == "" || rule.SeriesName == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "userId, seriesId and seriesName are required"})
		return
	}
	if _, err := core.ParseRanges(rule.FillerRanges); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("filler ranges: %w", err))
		return
	}
	if _, err := core.ParseRanges(rule.SkipRanges); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("skip ranges: %w", err))
		return
	}
	saved, err := s.rules.Upsert(rule)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) handleDeleteRule(w http.ResponseWriter, r *http.Request) {
	if err := s.rules.Delete(r.PathValue("ruleId")); err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleRunRule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DryRun bool `json:"dryRun"`
	}
	if r.ContentLength > 0 && !readJSON(w, r, &req) {
		return
	}
	rule, ok := s.rules.Get(r.PathValue("ruleId"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "rule not found"})
		return
	}
	run := s.runs.Start("manual", rule.SeriesName, []core.Job{rule.Job()},
		core.Options{DryRun: req.DryRun, NFORefresh: rule.NFORefresh})
	writeJSON(w, http.StatusOK, map[string]string{"runId": run.ID})
}
