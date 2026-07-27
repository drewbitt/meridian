# First stable release criteria

Meridian reaches `v1.0.0` when the supported paths below are reliable for a
self-hosted user upgrading from the beta channel. A numeric coverage target alone
does not define readiness: the release must have representative fixtures,
integration coverage, and a completed browser pass for the workflows that can
lose or misrepresent sleep data.

## Required before `v1.0.0`

### Sleep-data correctness

- Google Health fixtures cover reconciled main sleep, naps, pagination,
  daylight-saving transitions, token refresh, insufficient scopes, rate limits,
  and partial save failure.
- Apple Health fixtures cover ZIP and XML input, multiple sources, exact
  duplicates, naps, and malformed records.
- Health Connect fixtures cover all supported stage values, awake-in-bed time,
  overlapping/out-of-session stages, naps, and invalid intervals.
- Gadgetbridge fixtures cover Xiaomi sleep-time summaries and the supported
  Mi Band/Huami activity schemas. Unsupported device schemas fail clearly instead
  of interpreting generic low activity as sleep.
- Imports are idempotent. Distinct same-day sleeps survive, while an identical
  re-import does not create another record.
- Time-zone, after-midnight, daylight-saving, future-time, and over-24-hour
  boundaries have regression tests.
- Documentation and the Settings UI name the exact supported formats. Native
  Health Connect backup ZIP support is not implied until an end-to-end fixture
  proves it.

### Scheduling and persistence

- Database upgrade tests start from the latest beta schema and verify that
  startup migrations preserve user data.
- Schedule caching uses one canonical date representation and does not create a
  second row for the same user/day.
- Morning summaries, habit reminders, and other notifications are idempotent and
  are not suppressed by an earlier dashboard visit or sleep sync.
- Cached and freshly calculated dashboards resolve the same zones, nap recovery,
  sunrise, sunset, and habit times.
- Backup and restore instructions are exercised against the release candidate,
  including an upgrade rollback to the pre-upgrade data directory.

### Accounts and isolation

- First-user registration, closed registration, sign-in, sign-out, and session
  expiry have route-level tests.
- A multi-user test proves sleep records, settings, schedules, habits, Google Health
  credentials, and API responses cannot cross account boundaries.
- State-changing browser routes have a documented CSRF threat model and tests for
  the chosen protection.
- Production cookie and forwarded-protocol behavior is tested behind the
  documented reverse-proxy setup.

### Product and browser quality

- A new account sees an honest empty state, not a personalized-looking forecast
  before any sleep exists.
- Dashboard, chart, sleep entry, imports, habits, and settings pass an end-to-end
  browser run at desktop and 390-pixel mobile widths.
- The chart remains readable for empty, single-night, nap, DST, and extreme-debt
  inputs, with no clipping or misleading gaps.
- Automated accessibility checks have no known critical or serious violations on
  the core signed-in pages. Any tool-incomplete result is manually reviewed.
- Primary navigation remains keyboard- and touch-reachable at every supported
  width.
- Core charts and time formatting work without third-party runtime JavaScript;
  optional web fonts may fall back safely.

### Release and operations

- Local generation, lint, race tests, module-tidy check, production build, and the
  container build pass from a clean checkout.
- Hosted CI passes on the release commit, and release automation produces both
  architecture images and immutable digests.
- The release notes call out schema changes, supported import formats, known
  limitations, backup steps, and rollback steps.
- There are no open critical/high correctness, security, data-loss, or
  account-isolation bugs. Lower-severity accepted issues are listed in the release
  notes.
- The release candidate is run for at least several days with real or
  privacy-safe representative data before the stable tag is created.

## Known beta limitations

- Native/encrypted Health Connect backup ZIP files are not supported.
- Gadgetbridge support is schema-specific; new device families need a captured,
  privacy-safe fixture before they are advertised as supported.
- Exact Apple Health duplicates are removed, but partially overlapping records
  from different sources still need a defined merge policy and regression
  fixtures.
- Broader route/service integration coverage, multi-user isolation coverage,
  CSRF hardening, and an exercised upgrade/rollback drill remain stable-release
  work.
