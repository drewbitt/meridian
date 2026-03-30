# Meridian

[![CI](https://github.com/drewbitt/meridian/actions/workflows/ci.yml/badge.svg)](https://github.com/drewbitt/meridian/actions/workflows/ci.yml)
[![License: AGPL-3.0](https://img.shields.io/github/license/drewbitt/meridian)](LICENSE)

Self-hosted sleep and energy tracker. Meridian turns sleep data into alertness forecasts, tracks sleep debt, and sends timed notifications via ntfy.

One Go binary. One SQLite file.

## Quick Start

### Docker

```bash
docker run -d \
  --name meridian \
  -p 8090:8090 \
  -v meridian_data:/pb_data \
  ghcr.io/drewbitt/meridian:latest
```

Then open `http://localhost:8090`.

On first launch, PocketBase will print a one-time setup URL to create a superuser account — open it in your browser. The log also suggests a CLI command, but that won't work because the image is distroless (no shell). Use the browser URL instead.

After that, register a regular user account at `/register` and log your first night of sleep.

Once you've registered, disable public signups:

```bash
docker run -d \
  --name meridian \
  -p 8090:8090 \
  -e ALLOW_REGISTRATION=false \
  -v meridian_data:/pb_data \
  ghcr.io/drewbitt/meridian:latest
```

### Docker Compose

```yaml
services:
  meridian:
    image: ghcr.io/drewbitt/meridian:latest
    ports:
      - "8090:8090"
    volumes:
      - meridian_data:/pb_data
    environment:
      - ALLOW_REGISTRATION=false  # set to true for first run
      - TZ=America/New_York       # your timezone
    restart: unless-stopped

volumes:
  meridian_data:
```

### Build from source

```bash
docker build -t meridian .
docker run -d -p 8090:8090 -v meridian_data:/pb_data meridian
```

Data lives in `/pb_data` (SQLite database + uploads). Back up this directory.

## Features

- FIPS Three Process Model for alertness prediction (homeostatic pressure, circadian rhythm, post-lunch dip, sleep inertia)
- 14-day weighted sleep debt -- recent nights count more, accuracy improves with more data
- Energy zones: morning peak, afternoon dip, evening peak, wind-down, melatonin window
- Notifications via [ntfy](https://ntfy.sh) -- caffeine cutoff, focus windows, energy dips, bedtime
- 5 data sources: manual entry, Fitbit (OAuth2, auto-sync every 4h), Health Connect, Apple Health, Gadgetbridge
- Dark-themed dashboard with real-time energy curve (Chart.js + Datastar SSE)

## Data Sources

| Source | Method | Sync |
|---|---|---|
| Manual | Web form | -- |
| Fitbit | OAuth2 | Every 4 hours |
| Health Connect | JSON upload | Manual |
| Apple Health | ZIP/XML upload | Manual |
| Gadgetbridge | SQLite upload | Manual |

## Fitbit Setup

1. Create a Personal app at [dev.fitbit.com/apps/new](https://dev.fitbit.com/apps/new)
2. Callback URL: `https://your-domain/auth/fitbit/callback` (or `http://localhost:8090/auth/fitbit/callback` locally)
3. Default Access Type: Read-Only (other URL fields can be placeholders)
4. Copy Client ID and Client Secret into Settings, save, click Connect

Backfills the last 30 days on first connect.

> Fitbit's Web API is [scheduled for deprecation in September 2026](https://dev.fitbit.com/build/reference/web-api/).

## Configuration

| Variable | Default | Description |
|---|---|---|
| `ALLOW_REGISTRATION` | `true` | Set to `false` (or `no`, `off`, `0`) to disable new account creation |
| `TZ` | `UTC` | Timezone for cron jobs (e.g. `America/New_York`) |

Per-user settings are on the Settings page:

- Timezone (e.g. `America/New_York`) — auto-detected from Fitbit or browser, falls back to server `TZ`
- Sleep need (default 8 hours)
- ntfy server, topic, and access token
- Fitbit OAuth2 credentials
- File imports for Health Connect, Apple Health, and Gadgetbridge

PocketBase admin panel is at `/_/` for superuser operations.

## Stack

| Layer | Technology |
|---|---|
| Backend | [PocketBase](https://pocketbase.io) (Go) -- auth, cron, SQLite, admin |
| Frontend | [Datastar](https://data-star.dev) + [Templ](https://templ.guide) + Tailwind CSS |
| Engine | FIPS Three Process Model in Go (~250 lines) |
| Notifications | [ntfy](https://ntfy.sh) (single HTTP POST) |
| Database | SQLite (embedded) |

## Development

```bash
./bin/mise install    # installs Go, templ, air, tailwind
./bin/mise run dev    # templ watch + tailwind watch + hot reload
```

No setup beyond cloning. The [mise](https://mise.jdx.dev/) bootstrap script in `bin/` handles everything. Git hooks auto-configure on directory entry.

`http://localhost:8090` for the app, `/_/` for the PocketBase admin.

```bash
./bin/mise run test
./bin/mise run build   # -> ./meridian
./bin/mise tasks       # list all commands
```

## License

[AGPL-3.0](LICENSE)
