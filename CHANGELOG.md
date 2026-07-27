# Changelog

## Unreleased

### Changed

- Replaced Fitbit Web API OAuth and sync with the Google Health API. Existing
  sleep history is preserved, and an overlapping first Google Health sync
  updates the legacy row instead of creating a duplicate. Users must connect
  Google Health once to resume automatic sleep sync.

### Security

- Google Health requests use the single read-only sleep scope, PKCE, and
  short-lived one-time OAuth state. Installations can keep the OAuth client
  secret out of user settings by supplying it through environment variables.
  Stored OAuth secrets and tokens are hidden from PocketBase API responses, and
  deployment callback URLs fail closed unless they use HTTPS.
