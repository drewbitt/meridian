# Circadian

[![CI](https://github.com/drewbitt/circadian/actions/workflows/ci.yml/badge.svg)](https://github.com/drewbitt/circadian/actions/workflows/ci.yml)
[![License: AGPL-3.0](https://img.shields.io/github/license/drewbitt/circadian)](LICENSE)

Self-hosted circadian rhythm tracker. Predicts energy levels using sleep science, tracks sleep debt, and sends timed notifications via ntfy.

One Go binary. One SQLite file.

<!-- ![Circadian Dashboard](docs/screenshot.png) -->

## Quick Start

### Docker

```bash
docker run -d \
  --name circadian \
  -p 8090:8090 \
  -v circadian_data:/pb_data \
  ghcr.io/drewbitt/circadian:latest
```

Open `http://localhost:8090`, create an account, and log your first night of sleep.

### Docker Compose

```yaml
services:
  circadian:
    image: ghcr.io/drewbitt/circadian:latest
    ports:
      - "8090:8090"
    volumes:
      - circadian_data:/pb_data
    restart: unless-stopped

volumes:
  circadian_data:
```

### Build from source

```bash
docker build -t circadian .
docker run -d -p 8090:8090 -v circadian_data:/pb_data circadian
```

Data lives in `/pb_data` (SQLite database + uploads). Back up this directory.

## Features

- FIPS Three Process Model for alertness prediction (homeostatic pressure, circadian rhythm, post-lunch dip, sleep inertia)
- 14-day weighted sleep debt -- recent nights count more
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

## Configuration

All settings are per-user, managed through the Settings page:

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

Requires [mise](https://mise.jdx.dev/) for tool management.

```bash
mise install          # install Go, templ, air, tailwind
mise run setup        # download dependencies
mise run dev          # start dev servers (templ watch + tailwind watch + hot reload)
```

App runs at `http://localhost:8090`. PocketBase admin at `http://localhost:8090/_/`.

```bash
mise run test         # run all tests
mise run build        # production binary -> ./circadian
mise run fmt          # format code
mise run vet          # lint
mise tasks            # list all commands
```

## License

[AGPL-3.0](LICENSE)
