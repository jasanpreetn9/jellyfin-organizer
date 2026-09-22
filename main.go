// jellyfin-organizer: web GUI for fixing anime episode numbering in Jellyfin
// (season-relative → absolute) and tagging filler episodes.
//
// All settings live in DATA_DIR/config.json (edit it directly or via the
// ⚙ Settings panel in the GUI). DATA_DIR defaults to "data"; it is the only
// environment variable, because it tells the app where config.json is.
package main

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"

	"jellyfin-organizer/internal/config"
	"jellyfin-organizer/internal/jellyfin"
	"jellyfin-organizer/internal/server"
)

//go:embed all:web
var webFiles embed.FS

func main() {
	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		dataDir = "data"
	}

	cfg, err := config.Load(dataDir)
	if err != nil {
		log.Fatal(err)
	}
	if cfg.JellyfinURL == "" || cfg.APIKey == "" {
		log.Printf("WARNING: no Jellyfin credentials yet — open the GUI and fill in ⚙ Settings, or edit %s", config.Path(dataDir))
	}

	client := jellyfin.New(cfg.JellyfinURL, cfg.APIKey)
	webRoot, err := fs.Sub(webFiles, "web")
	if err != nil {
		log.Fatal(err)
	}
	srv, err := server.New(client, webRoot, dataDir, cfg)
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("jellyfin-organizer listening on :%s (config: %s)", cfg.Port, config.Path(dataDir))
	log.Printf("Sonarr webhook endpoint: POST /webhook/sonarr")
	log.Fatal(http.ListenAndServe(":"+cfg.Port, srv))
}
