# Meridian stable-release QA

**Date:** 2026-07-27
**Target:** `http://127.0.0.1:18090`
**Session:** `meridian-stable-qa-2`
**Scope:** First-run onboarding, authentication, manual sleep entry, imports, settings, habits, dashboard charts, responsive layout, and accessibility.

## Summary

The browser pass covered first-run flows plus signed-in desktop and 390×844 mobile layouts. The five initial findings were fixed and rechecked against the rebuilt binary. Axe-core reports no violations on Dashboard, Settings, Habits, or Log Sleep; its remaining gradient/background-image and decorative-symbol contrast results were manually reviewed.

An isolated second instance verified the brand-new-account path: the dashboard
shows a clear no-data message without a personalized energy curve or invented key
times, remains fully navigable at 390×844, and has zero axe violations.
[Evidence](screenshots/12-empty-dashboard-final.png)

## Findings

### QA-001 — Settings import controls are not named for assistive technology

- **Severity:** High
- **Area:** Settings / accessibility
- **Reproduction:** Create the first account, land on `/settings?welcome=1`, and run axe-core.
- **Observed:** The file input has no label and the source `<select>` has no accessible name. Axe reports critical `label` and `select-name` violations.
- **Expected:** Every import control has a programmatically associated visible label.
- **Evidence:** [02-welcome-settings.png](screenshots/02-welcome-settings.png)
- **Status:** Resolved — labels and stable `name` attributes were added; axe reports no violation.
- **Fixed evidence:** [10-settings-final.png](screenshots/10-settings-final.png)

### QA-002 — Settings helper text fails contrast requirements

- **Severity:** Medium
- **Area:** Settings / accessibility / visual design
- **Reproduction:** Open `/settings?welcome=1` and run axe-core.
- **Observed:** Fourteen helper-text/link nodes fail WCAG contrast; the guidance is also visibly difficult to read against the dark background.
- **Expected:** Normal text and links meet WCAG AA contrast, and inline links are distinguishable without color alone.
- **Evidence:** [02-welcome-settings.png](screenshots/02-welcome-settings.png)
- **Status:** Resolved — helper colors and inline link treatment now meet the intended contrast and non-color affordance.
- **Fixed evidence:** [10-settings-final.png](screenshots/10-settings-final.png)

### QA-003 — Mobile navigation hides every destination

- **Severity:** Critical
- **Area:** Navigation / responsive layout
- **Reproduction:** Sign in, set the viewport to 390×844, and open the dashboard.
- **Observed:** Dashboard, Habits, Log Sleep, and Settings are all hidden with no menu or alternate navigation. Only the Meridian home link and Sign Out remain.
- **Expected:** All primary destinations remain reachable on small screens.
- **Evidence:** [05-dashboard-mobile.png](screenshots/05-dashboard-mobile.png)
- **Status:** Resolved — a compact second-row navigation remains visible at 390 pixels.
- **Fixed evidence:** [09-dashboard-final.png](screenshots/09-dashboard-final.png)

### QA-004 — Key Times grid renders an empty card-sized block

- **Severity:** Medium
- **Area:** Dashboard / responsive layout
- **Reproduction:** Log one night without a detected nap window and open the dashboard at desktop or mobile width.
- **Observed:** The fixed four-column/two-column grid has only three cards, leaving a large colored empty cell. It is especially prominent on mobile.
- **Expected:** The grid should size itself to the number of available key-time cards.
- **Evidence:** [04-dashboard-one-night.png](screenshots/04-dashboard-one-night.png), [05-dashboard-mobile.png](screenshots/05-dashboard-mobile.png)
- **Status:** Resolved — the grid derives its columns from the available cards.
- **Fixed evidence:** [09-dashboard-final.png](screenshots/09-dashboard-final.png)

### QA-005 — Core pages have no level-one heading

- **Severity:** Low
- **Area:** Semantics / accessibility
- **Reproduction:** Open Dashboard or Log Sleep and run axe-core.
- **Observed:** Axe reports `page-has-heading-one`; page titles are rendered as `<h2>`.
- **Expected:** Each page has one descriptive level-one heading.
- **Evidence:** [03-log-sleep-empty.png](screenshots/03-log-sleep-empty.png), [04-dashboard-one-night.png](screenshots/04-dashboard-one-night.png)
- **Status:** Resolved — each core page now has one descriptive `<h1>`.
- **Fixed evidence:** [09-dashboard-final.png](screenshots/09-dashboard-final.png)
