// Package config handles the JSON settings file (DATA_DIR/config.json),
// the single source of truth for all settings including credentials.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// File is the on-disk JSON configuration.
type File struct {
	JellyfinURL             string `json:"jellyfinUrl"`
	APIKey                  string `json:"apiKey"`
	Username                string `json:"username"` // default user for webhook/schedule runs
	Port                    string `json:"port"`     // HTTP port (restart to apply)
	WebhookDelaySeconds     int    `json:"webhookDelaySeconds"`
	WebhookToken            string `json:"webhookToken"`
	ScheduleIntervalMinutes int    `json:"scheduleIntervalMinutes"`
}

func Defaults() File {
	return File{Port: "8981", WebhookDelaySeconds: 60, ScheduleIntervalMinutes: 360}
}

func Path(dataDir string) string { return filepath.Join(dataDir, "config.json") }

// Load reads config.json from dataDir. When the file is missing, a template
// with defaults is written so the user has something to fill in.
func Load(dataDir string) (File, error) {
	cfg := Defaults()
	data, err := os.ReadFile(Path(dataDir))
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, cfg.Save(dataDir)
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse %s: %w", Path(dataDir), err)
	}
	if cfg.Port == "" {
		cfg.Port = "8981"
	}
	return cfg, nil
}

// Save writes config.json with owner-only permissions (it holds the API key).
func (c File) Save(dataDir string) error {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(Path(dataDir), data, 0o600)
}
