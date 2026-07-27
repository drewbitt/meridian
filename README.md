# Meridian

[![CI](https://github.com/drewbitt/meridian/actions/workflows/ci.yml/badge.svg)](https://github.com/drewbitt/meridian/actions/workflows/ci.yml)
[![License: AGPL-3.0](https://img.shields.io/github/license/drewbitt/meridian)](LICENSE)

Meridian is a self-hosted sleep tracker that estimates how your energy will change during the day. It stores everything in PocketBase, runs as one Go binary, and can send reminders through [ntfy](https://ntfy.sh).

Meridian can:

- calculate alertness and sleep debt from recent sleep
- show likely focus periods, energy dips, and wind-down times
- import Fitbit, Health Connect, Apple Health, and Gadgetbridge data
- schedule personal habits around your predicted energy
- send caffeine, nap, focus, and bedtime notifications

The model is an estimate, not medical advice or a diagnosis.

## Run Meridian

Docker Compose is the easiest way to keep the database, restart policy, and container hardening together. You only need the deployment file:

```bash
mkdir meridian && cd meridian
curl -O https://raw.githubusercontent.com/drewbitt/meridian/main/compose.yaml
docker compose up -d
```

Open [http://localhost:8090](http://localhost:8090) and create your account. Meridian signs you in, detects your browser time zone, and takes you to Settings. By default, registration closes automatically after the first account.

The compose service runs as an unprivileged user with a read-only root filesystem. Application data is kept in the `meridian-data` volume at `/pb_data`.

### Run without Compose

```bash
docker run -d \
  --name meridian \
  --publish 8090:8090 \
  --volume meridian-data:/pb_data \
  --read-only \
  --tmpfs /tmp \
  --cap-drop ALL \
  --security-opt no-new-privileges:true \
  --restart unless-stopped \
  ghcr.io/drewbitt/meridian:beta
```

The `beta` tag follows the supported prerelease channel. Pin a numbered tag when you want upgrades to be explicit.

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `ALLOW_REGISTRATION` | `first-user` | Creates one bootstrap account and then closes automatically. Set `true` for intentional multi-user signup or `false` to disable signup completely. |
| `TZ` | `UTC` | Server time zone used when a user has not saved one, such as `America/New_York`. |
| `MERIDIAN_PORT` | `8090` | Host port used by `compose.yaml`. This is not read by the application itself. |
| `MERIDIAN_TAG` | `beta` | Image channel or immutable version used by `compose.yaml`. |

User-specific options live on the Settings page:

- time zone and sleep need
- location for sunrise and sunset calculations
- ntfy server, topic, and access token
- Fitbit OAuth credentials
- file imports for Health Connect, Apple Health, and Gadgetbridge

To change container options, create a `.env` file beside `compose.yaml` with only the values you want to override. `.env.example` in the repository documents every Compose option.

PocketBase administration is optional. If you need its superuser panel at `/_/`, create a superuser with the PocketBase setup link printed on first launch.

## Updates and backups

Update the container with:

```bash
docker compose pull
docker compose up -d
```

Back up `/pb_data` before an update. It contains the SQLite database, uploaded files, and PocketBase state. Stop the service while copying the directory so the backup is consistent:

```bash
docker compose stop
docker compose cp meridian:/pb_data ./meridian-backup
docker compose start
```

Keep the backup somewhere outside the host running Meridian. To restore it, stop Meridian and copy the saved contents back into `/pb_data`.

## Data sources

| Source | Import method | Schedule |
|---|---|---|
| Manual | Sleep entry form | On demand |
| Fitbit | OAuth 2.0 | Every 30 minutes |
| Health Connect | JSON upload | On demand |
| Apple Health | ZIP or XML upload | On demand |
| Gadgetbridge | SQLite upload | On demand |

### Fitbit

1. Create a Personal app at [dev.fitbit.com/apps/new](https://dev.fitbit.com/apps/new).
2. Set the callback URL to `https://your-domain/auth/fitbit/callback`. For local use, enter `http://localhost:8090/auth/fitbit/callback`.
3. Choose Read-Only as the default access type.
4. Save the client ID and client secret on Meridian's Settings page, then select Connect.

The first connection imports the previous 30 days. Later syncs run every 30 minutes and reread the last three days to catch delayed Fitbit records.

Fitbit says its Web API will be deprecated in September 2026. Existing Fitbit support may need to change when Google publishes its replacement.

## Development

The repository uses [mise](https://mise.jdx.dev/) to pin Go, templ, Tailwind CSS, Air, golangci-lint, and git-cliff. The bootstrap script downloads mise into the repository, so a global install is optional.

```bash
git clone https://github.com/drewbitt/meridian.git
cd meridian
./bin/mise install
./bin/mise run dev
```

The development server is at [http://localhost:8090](http://localhost:8090). Air reloads the Go process while templ and Tailwind watch their source files.

Useful tasks:

```bash
./bin/mise run generate    # regenerate templ code and CSS
./bin/mise run test        # run tests with the race detector
./bin/mise run lint        # run golangci-lint
./bin/mise run build       # create ./meridian
./bin/mise run check       # run the complete local CI suite
./bin/mise tasks           # list every task
```

The checked-in pre-commit hook runs the same `check` task as CI. Entering the repository through mise configures Git to use `.githooks`.

To build and run the production image locally:

```bash
docker compose -f compose.yaml -f compose.dev.yaml up --build
```

## How it is put together

| Part | Technology |
|---|---|
| Application and database | [PocketBase](https://pocketbase.io) with SQLite |
| Server-rendered UI | [templ](https://templ.guide) |
| Styling | [Tailwind CSS](https://tailwindcss.com) |
| Notifications | [ntfy](https://ntfy.sh) |
| Energy model | FIPS Three Process Model implemented in Go |

Templates and CSS are generated before every build, lint, or test task. The generated files are ignored by Git and embedded into the final binary.

The optional `.agents/skills` submodule contains Go guidance for coding agents. It is not needed to build, test, or run Meridian.

## License

Meridian is licensed under the [GNU Affero General Public License v3.0](LICENSE).
