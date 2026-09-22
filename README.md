# Jellyfin Anime Organizer

Web GUI for fixing anime episode numbering in Jellyfin and tagging filler episodes.

**The problem:** anime organized into arc-based seasons gets its episodes renumbered
from 1 by the metadata provider. The filename keeps the true release-order (absolute)
number:

```
Bleach (2004) - S02E21 - 041 - Reunion Ichigo and Rukia [SDTV].mkv
```

Jellyfin shows this as S02**E21**; this tool rewrites the episode's IndexNumber to the
absolute number so it shows as S02**E41**.

## Features

- **Set index → absolute**: parses the absolute number from each episode's filename and
  writes it to Jellyfin's `IndexNumber`, optionally triggering the NFO sidecar save
  (requires "Save metadata to NFO" enabled on the library).
- **Filler tagging**: paste absolute episode numbers/ranges (`26, 97, 101-106`) and the
  matching episodes get `(FILLER) ` prefixed to their title. Untag removes the prefix
  (and the legacy ` (FILLER)` suffix from the old Python scripts).
- **Title editing**: click any title in the episode table to edit it inline.
- **Dry-run previews**: every action can be previewed before writing anything.
- **Skip list**: per show, a list of absolute episode numbers that are never modified —
  no index change, no title change (useful for specials/OVAs whose parsed number is wrong).
- **Rules**: save a show's action + filler list + skip list as a rule. "Run all" executes
  every rule as a batch. Rules marked **auto** are also re-applied by the Sonarr webhook
  and by the schedule; manual-only rules run only when you trigger them. Rules persist in
  `DATA_DIR/rules.json`. The old `jellyfin_batch.txt` CSV format can be imported.
- **Schedule**: every `scheduleIntervalMinutes` (default 6 h) all auto rules re-run, so
  shows stay fixed even if a webhook was missed or Sonarr isn't set up.
- **Sonarr webhook**: `POST /webhook/sonarr` — on any Sonarr event for a series, the app
  waits `webhookDelaySeconds` (debounced, so import bursts coalesce and Jellyfin has
  time to scan), then re-runs that show's rules — or a plain set-absolute pass if the
  show has no rule.

## Configuration — where your credentials go

Everything lives in **`data/config.json`** (created with defaults on first run,
file mode 0600). Fill it in either way:

- **GUI**: open the app and click **⚙ Settings** — it opens automatically until
  the Jellyfin URL and API key are set. Changes apply immediately (only `port`
  needs a restart).
- **File**: edit `data/config.json` directly and restart:

```json
{
  "jellyfinUrl": "http://192.168.1.156:8899",
  "apiKey": "<Jellyfin Dashboard → API Keys>",
  "username": "jnagra",
  "port": "8981",
  "webhookDelaySeconds": 60,
  "webhookToken": "",
  "scheduleIntervalMinutes": 360
}
```

| Field | Purpose |
|---|---|
| `jellyfinUrl` | e.g. `http://jellyfin:8096` |
| `apiKey` | Jellyfin Dashboard → API Keys |
| `username` | user context for webhook/scheduled runs on shows without a rule (default: first user) |
| `port` | HTTP port (keep `8981` when using Docker) |
| `webhookDelaySeconds` | debounce before a webhook re-check |
| `webhookToken` | if set, webhook requires `?token=...` |
| `scheduleIntervalMinutes` | how often auto rules re-run (`0` disables) |

The only environment variable is `DATA_DIR` (default `data`, `/data` in
Docker) — it tells the app where `config.json` and `rules.json` live.

## Run

```sh
# local
go run .            # http://localhost:8981

# docker
docker compose up -d --build
```

## Sonarr setup

Sonarr → Settings → Connect → **+** → Webhook:

- URL: `http://<host>:8981/webhook/sonarr` (append `?token=...` if a webhook token is set)
- Method: POST
- Triggers: On Import, On Upgrade, On Rename (others are harmless — any event with a
  series schedules a re-check)

Note: the webhook fires when **Sonarr** changes files; Jellyfin still has to scan them
before this tool can fix the metadata. Keep real-time monitoring on in Jellyfin (or a
Sonarr→Jellyfin library-refresh connection) and raise `webhookDelaySeconds` if the
webhook run keeps arriving before Jellyfin has picked the episodes up.

## API

```
GET  /api/health
GET  /api/config                      (secrets reported as *Set booleans, never echoed)
POST /api/config                      {jellyfinUrl, apiKey, username, port, webhookDelaySeconds, webhookToken, scheduleIntervalMinutes}
GET  /api/users
GET  /api/users/{userId}/views
GET  /api/users/{userId}/views/{viewId}/series?search=
GET  /api/users/{userId}/items/{itemId}
GET  /api/series/{seriesId}/episodes
POST /api/items/{itemId}/title        {userId, title}
POST /api/runs                        {dryRun, nfoRefresh, label, jobs:[{userId, seriesId, seriesName, action, fillerRanges, skipRanges}]}
GET  /api/runs                        (recent runs)
GET  /api/runs/{runId}                (status + log)
GET  /api/rules  /  POST /api/rules  /  DELETE /api/rules/{ruleId}
POST /api/rules/{ruleId}/run          {dryRun}
POST /webhook/sonarr[?token=...]      (Sonarr webhook)
```

Actions: `set_absolute`, `tag_filler`, `untag_filler`, `both`. All fields on a `POST /api/config`
request are optional and omitted/null fields keep their current value — the GUI never has to
resend the API key just to change, say, the schedule interval.

## Development

```sh
go build ./...
go vet ./...
go test ./...
```

`internal/core` (absolute-number parsing, filler tagging, range parsing) has unit test
coverage; the HTTP/Jellyfin-client layers don't, so changes there should be exercised
against a real Jellyfin server (dry-run first).

### Building the image manually

If Docker Desktop isn't installed, [Podman](https://podman.io) builds and saves
Docker-compatible images fine — start its VM first (`podman machine start`), then:

```sh
podman build --platform linux/amd64 -t jellyfin-organizer:latest .
podman save -o jellyfin-organizer-image-amd64.tar jellyfin-organizer:latest --format docker-archive
```

Swap `linux/amd64` for `linux/arm64` for an ARM target. `docker load -i
jellyfin-organizer-image-amd64.tar` on the target host picks it up from there;
`docker compose up -d` recreates the running container against the freshly loaded
`:latest` tag.

## Filler list reference

Bleach (2004), absolute episode numbers:

```
33, 50, 64-108, 128-137, 147-149, 168-189, 204-205, 213-214, 228-266, 287, 298-299, 303-305, 311-341, 355
```
