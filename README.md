# Meridian

[![CI](https://github.com/drewbitt/meridian/actions/workflows/ci.yml/badge.svg)](https://github.com/drewbitt/meridian/actions/workflows/ci.yml)
[![License: AGPL-3.0](https://img.shields.io/github/license/drewbitt/meridian)](LICENSE)

Meridian is a self-hosted sleep tracker that estimates how your energy will change during the day. It stores everything in PocketBase, runs as one Go binary, and can send reminders through [ntfy](https://ntfy.sh).

Meridian can:

- calculate a weighted sleep-debt planning index from recent sleep
- show a modeled high-energy period and, only when supported, a distinct dip
- sync Fitbit and Pixel Watch sleep through Google Health
- import Health Connect, Apple Health, and Gadgetbridge data
- schedule personal habits around observed or modeled daily anchors
- send confidence-gated caffeine, nap, energy, and wind-down notifications

The model is an estimate, not medical advice, a diagnosis, or a measurement of
circadian phase. See the [model validation and limitations](docs/model-validation.md).

## Run Meridian

Docker Compose is the easiest way to keep the database, restart policy, and container hardening together. You only need the deployment file:

```bash
mkdir meridian && cd meridian
curl -O https://raw.githubusercontent.com/drewbitt/meridian/main/compose.yaml
docker compose up -d
```

Open [http://127.0.0.1:8090](http://127.0.0.1:8090) and create your account. Meridian signs you in, detects your browser time zone, and takes you to Settings. By default, registration closes automatically after the first account.

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
| `GOOGLE_HEALTH_CLIENT_ID` | empty | Optional instance-wide Google OAuth Web client ID. Set it together with `GOOGLE_HEALTH_CLIENT_SECRET` so users only need to select Connect. |
| `GOOGLE_HEALTH_CLIENT_SECRET` | empty | Optional instance-wide Google OAuth Web client secret. Prefer `.env` over storing the OAuth client in each user's settings. |
| `TZ` | `UTC` | Server time zone used when a user has not saved one, such as `America/New_York`. |
| `MERIDIAN_PORT` | `8090` | Host port used by `compose.yaml`. This is not read by the application itself. |
| `MERIDIAN_TAG` | `beta` | Image channel or immutable version used by `compose.yaml`. |

User-specific options live on the Settings page:

- time zone and sleep need
- location for sunrise and sunset calculations
- ntfy server, topic, and access token
- Google Health connection
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
| Google Health | OAuth 2.0 | Every 30 minutes |
| Health Connect | Compatible JSON export | On demand |
| Apple Health | ZIP or XML upload | On demand |
| Gadgetbridge | SQLite upload | On demand |

Health Connect imports currently accept JSON whose records contain session start/end
times and sleep stages. The encrypted/native Health Connect backup ZIP is not yet
supported; use a compatible JSON exporter instead.

Gadgetbridge imports support current Xiaomi sleep-time samples, the legacy
`SLEEP_SESSION` compatibility table, and Mi Band/Huami activity-sample sleep data.
Other device-specific Gadgetbridge schemas may not contain a compatible sleep
representation.

### Google Health

Google Health uses a Google Cloud OAuth client that belongs to your Meridian
installation. The shortest setup is:

1. In a Google Cloud project, [enable the Google Health API](https://developers.google.com/health/setup).
2. In Google Auth Platform, configure an External audience and add only
   `https://www.googleapis.com/auth/googlehealth.sleep.readonly` under Data
   Access.
3. Create an OAuth 2.0 client with application type **Web application**. Add the
   exact redirect URI shown on Meridian's Settings page. Authorized JavaScript
   origins can remain empty because Meridian completes OAuth on the server:

   ```text
   http://127.0.0.1:8090/auth/google-health/callback
   ```

   For local development, plain HTTP is valid only for `localhost` and loopback
   IPs. The scheme, host, port, path, and trailing slash must match exactly, so
   `localhost` and `127.0.0.1` are not interchangeable.

   Use `https://your-domain/auth/google-health/callback` for a deployed instance.
   The public Site URL must be an HTTPS origin with no subpath. A reverse proxy
   may terminate TLS and forward plain HTTP to Meridian on the private network;
   Google only sees the public HTTPS callback.
4. Put the client ID and secret in `.env`, then restart Meridian:

   ```dotenv
   GOOGLE_HEALTH_CLIENT_ID=your-client-id
   GOOGLE_HEALTH_CLIENT_SECRET=your-client-secret
   ```

5. In Meridian Settings, select **Connect** and approve read-only sleep access.

For a single-user personal install, you can enter the client ID and secret
directly in Settings instead. Instance-wide environment variables are preferred:
the secret is not rendered in the page and every Meridian user gets a one-click
connection.

While the OAuth app is in Testing, add your Google account as a test user.
Testing refresh tokens expire after seven days, so publish the OAuth app for a
durable personal connection. An unverified published app shows Google's warning
and is limited to 100 lifetime user grants. The read-only sleep scope is
restricted; public distribution requires OAuth verification and Google's
third-party security assessment. Keep the OAuth secret, Meridian `.env`, and
`/pb_data` backup private.

This web-server flow is the simplest fit for Meridian:

- one Web application client can serve every Meridian user, with credentials
  supplied once through environment variables;
- a Desktop application client and arbitrary loopback port are useful for a
  local CLI such as `ghealth`, but do not match Meridian's hosted callback;
- service accounts cannot replace end-user consent for personal Google Health
  data; and
- Google Auth Platform consumer OAuth clients are configured in Cloud Console.
  `gcloud` can enable the API, but it does not create or update this Web
  application client or its consent-screen scopes.

The first connection imports today plus the previous 30 calendar days. Later
syncs run every 30 minutes and reread today plus the previous three days to
catch delayed device records. If
Google Health is still processing a newly detected sleep, Meridian shows a
waiting state and retries automatically before treating it as a completed
night. Polling is deliberate for local and self-hosted installations because it
does not require a public HTTPS subscriber or Google Cloud IAM setup. Google
Health webhooks are a better alternative for a larger public deployment that
needs prompt updates and can operate that additional infrastructure.

## Development

The repository uses [mise](https://mise.jdx.dev/) to pin Go, templ, Tailwind CSS, Air, golangci-lint, and git-cliff. The bootstrap script downloads mise into the repository, so a global install is optional.

```bash
git clone https://github.com/drewbitt/meridian.git
cd meridian
./bin/mise install
./bin/mise run dev
```

The development server is at [http://127.0.0.1:8090](http://127.0.0.1:8090). Air reloads the Go process while templ and Tailwind watch their source files.

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
