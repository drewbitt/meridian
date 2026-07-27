# Circadian model and sleep-data validation

This document defines what Meridian's daily curve can and cannot support. The
curve is a planning estimate from recent sleep. It is not a measurement of
circadian phase, melatonin onset, cognitive performance, or medical risk.

## Decision summary

Meridian currently uses the published FIPS Three Process Model parameters rather
than tuning the curve to resemble another product. It can display one clear high,
two clear highs with an intervening dip, or an unclear pattern. It does not add a
second peak solely to make the chart look like RISE.

The current work adds scenario tests and numerical sensitivity analysis, but it
is not an external outcome backtest. Meridian has no timestamped subjective
sleepiness, psychomotor-vigilance, DLMO, or continuous personal light-exposure
labels against which to estimate real peak-time error. Therefore:

- the published parameter set is a defensible baseline, not a proven personal
  truth;
- the 0.5 KSS dip-prominence rule is a conservative display policy, not a
  biological threshold;
- the five-, seven-, and ten-night boundaries control product uncertainty and
  missing-data behavior, not claims that physiology becomes known on those
  exact nights;
- exact model-timed notifications require moderate or high forecast confidence;
- unsupported phase patterns and stale current-day sleep produce an abstention,
  not a generic-looking personalized curve.

## What RISE contributes as a product reference

The supplied RISE material consistently presents a simple daily story: sleep
inertia, a morning peak, an afternoon dip, a later peak, wind-down, and a sleep
window. That presentation is useful because it gives a person an immediate plan
without asking them to interpret model internals.

RISE's current help material says its Energy Schedule uses recent sleep plus
actual light exposure and activity, updates daily, and can show muted peaks or
deeper dips with high sleep debt. Its sleep-debt help describes a weighted
14-night calculation and says naps can reduce debt:

- <https://help.risescience.com/hc/en-us/articles/40672503374871-How-does-RISE-predict-my-Energy-Schedule>
- <https://help.risescience.com/hc/en-us/articles/40621334445335-How-is-my-sleep-debt-calculated>
- <https://help.risescience.com/hc/en-us/articles/40621805049495-How-do-naps-affect-my-sleep-debt>
- <https://help.risescience.com/hc/en-us/articles/40610919159575-How-does-RISE-Calendar-Integration-work>

The benefit of RISE's canonical two-peak presentation is actionability and
consistency. The cost is that a stable visual grammar can be mistaken for an
individually observed rhythm. Its model is proprietary, so screenshots and help
copy cannot establish the equation, parameters, or personal peak-time accuracy.
Meridian should borrow the clear planning narrative, confidence recovery, and
daily update behavior—not assert that RISE validates a forced two-peak curve.

## Meridian model history

The repository history explains why the current model needed another pass:

| Version | Change | Assessment |
|---|---|---|
| `d0c44db` (2026-03-29) | Aligned the Go implementation with published FIPS parameters. | Useful provider-neutral baseline. |
| `5521ab6` (2026-04-01) | Added chronotype, debt, seasonal, notification, and peak/dip product logic; raised ultradian amplitude from 0.5 to 0.8 to make the curve more RISE-like. | Added useful personalization infrastructure, but several adjustments exceeded their evidence. The code also incorrectly described RISE's proprietary model as SAFTE/multiplicative. |
| Current validation pass | Restores published ultradian amplitude 0.5, removes the unmeasured seasonal phase shift, bounds chronotype adjustment, handles missing/current sleep explicitly, and separates measured events from planning estimates. | More honest and robust, but still awaiting external outcome validation. |

The underlying reference is Ingre et al.'s FIPS extension of the Three Process
Model. The paper reports that at least two sleep episodes are needed for model
initialization, that observed sleep improves predictions, and that complex sleep
histories remain difficult to initialize accurately:

<https://journals.plos.org/plosone/article?id=10.1371/journal.pone.0108679>

That is not evidence that two nights are enough for reliable personal phase
estimation. Meridian requires more history before calling its phase personalized.

## Numerical validation performed

The executable study is in
`internal/engine/model_validation_test.go`. It is deterministic and runs in CI.

### Peak-shape sensitivity

The study evaluates 72 combinations per ultradian amplitude:

- wake at 05:00, 07:00, or 09:00;
- 4, 6, 8, or 10 hours of main sleep;
- circadian phase shift of -2, 0, or +2 hours;
- 0 or 10 weighted hours of debt.

Across six amplitude values this is 432 modeled days.

| Ultradian amplitude | Two-peak days meeting the 0.5 KSS display rule | Largest raw dip | Mean modeled maximum after wake |
|---:|---:|---:|---:|
| 0.0 | 0/72 | 0.09 KSS | 7.8 h |
| 0.3 | 0/72 | 0.01 KSS | 8.7 h |
| **0.5, published** | **0/72** | **0.00 KSS** | **9.5 h** |
| 0.8, previous Meridian | 0/72 | 0.22 KSS | 10.3 h |
| 1.0 | 0/72 | 0.44 KSS | 10.6 h |
| 1.5 | 40/72 (56%) | 1.01 KSS | 11.2 h |

This answers a narrow but important question: merely changing ultradian
amplitude enough to manufacture a visible second peak materially changes model
shape and timing. The old 0.8 adjustment did not even create an actionable dip
under the current conservative classifier. Meridian therefore uses 0.5 and
accepts that ordinary FIPS days are usually one-peak days.

### Chronotype sensitivity

With an otherwise fixed 07:00 wake and eight-hour sleep, phase shifts from -2 to
+2 hours moved the modeled maximum monotonically:

| Phase shift | Maximum after wake |
|---:|---:|
| -2 h | 7.25 h |
| -1 h | 8.50 h |
| 0 h | 9.42 h |
| +1 h | 10.58 h |
| +2 h | 11.75 h |

This shows that phase adjustment has the intended direction and a large product
effect. It does not prove that habitual sleep midpoint estimates true circadian
phase for a particular person.

### Missing-data and minimum-history sensitivity

Fifty thousand synthetic 14-night histories compare complete weighted debt with
median-filled recent gaps. Results below are absolute error at the 90th
percentile:

| Observed nights | Regular sleep, SD 0.5 h | Irregular sleep, SD 1.5 h |
|---:|---:|---:|
| 3/14 | 3.46 h | 9.11 h |
| 5/14 | 2.72 h | 7.40 h |
| 7/14 | 2.21 h | 6.38 h |
| 10/14 | 1.67 h | 4.87 h |
| 14/14 | 0.00 h | 0.00 h |

A non-stationary failure case—an observed normal week followed by an unobserved
restricted week—had 13.6 weighted hours of true shortfall and a median-filled
estimate of 0.0. No threshold can recover an unobserved behavior change.

Consequently, Meridian imputes missing debt days only with at least seven
observed main-sleep nights and either no more than two gaps or a duration median
absolute deviation no greater than one hour. Otherwise it reports an observed
lower bound. Irregular histories remain low confidence even when a number can be
calculated.

### Invariants and scenario coverage

CI also checks:

- more modeled debt never improves mean KSS;
- longer main sleep does not materially worsen mean KSS;
- all curve values are finite and KSS remains in 1–9;
- phase shifts move the peak in the expected direction;
- broken nights, naps, isolated short sleep, long sleep, DST boundaries,
  late-day wakes, and stale wakes retain stable anchors;
- old multi-day gaps are not simulated as continuous wakefulness.

These are correctness and robustness tests. They cannot determine real-world
peak error, optimal display prominence, or personal planning benefit.

## Data classification and daily behavior

### Main sleep, fragments, and naps

Classification follows source provenance first:

- an explicit source classification such as Google Health `metadata.nap`, or a
  manual nap choice, wins;
- overlapping imports are merged rather than double counted;
- main-sleep fragments separated by no more than four hours form one episode;
- the final fragment's end is the wake anchor;
- a short episode near a clearly dominant four-hour-or-longer episode is a nap;
- an isolated sleep shorter than three hours with a local midpoint from 10:00
  through 20:59 is inferred as a nap;
- a lone short nighttime sleep remains main sleep so an insomnia night is not
  silently erased;
- naps repay weighted debt but never create a full-night deficit.

The four-hour fragment gap and three-hour daytime rule are transparent product
heuristics. Ambiguous records should remain editable; they are not universal
clinical definitions.

### Google Health delayed-processing lifecycle

Google Health can expose a detected sleep before its processing is complete:

| State | Meridian behavior |
|---|---|
| User has woken but today's main sleep is absent | Show “waiting for today's completed Google Health sleep”; do not reuse yesterday's wake; do not consume the daily-summary deduplication key. |
| Google Health reports `metadata.processed=false` | Leave that session out until complete, record that Google Health was checked and is still processing, then retry on the normal 30-minute cycle. |
| Completed sleep appears | Upsert today plus the previous three days, recompute the schedule, replace the waiting state, and reconcile the daily summary immediately. |
| Sleep arrives within four hours of wake | Send one “Good morning!” summary. |
| Sleep arrives later on the same local date | Send one “Today's sleep synced” summary rather than a belated greeting. |
| Google Health revises the same sleep later | Upsert by source and start time and recompute the dashboard; the summary remains deduplicated. |
| No current main sleep within 20 hours | Withhold the curve instead of anchoring a new day to an old wake. |

Periodic Google Health sync is therefore the primary wake-driven trigger. The 08:07
local job is a backup for manual and file-import users and is offset from the
Google Health cron to avoid a top-of-hour race. A manual “Sync now” either
refreshes the day immediately or explains that Google Health is still processing
it.

### Confidence and recovery behavior

| Data state | Curve behavior |
|---|---|
| No sleep | No personalized curve. |
| 1–4 observed main-sleep nights | Preliminary curve only when today's main sleep is present; no exact model-timed alerts. |
| 5–6 nights | Timing can begin using a robust midpoint, but missing debt remains a lower bound and confidence remains low. |
| 7–9 stable nights | Missing days may be labeled estimates; moderate confidence is possible. |
| 10+ nights, no more than two gaps, timing SD no greater than 1.5 h | Higher confidence. |
| Timing SD over 2.5 h or unsafe missing data | Low confidence; exact model-timed alerts are paused. |
| Current or habitual phase far outside the static model's supported range | Curve withheld; sleep/debt data remains visible. |

The dashboard says whether a curve is preliminary, low, moderate, or higher
confidence and whether it found one or two highs. It labels `wake + planning
window` as estimated wind-down, not a melatonin window. Caffeine cutoff is a
conservative cue about ten hours before target sleep, not a claim of a ten-hour
caffeine half-life.

## Why not replace FIPS with a RISE-like two-peak model now?

There are three distinct choices:

1. **Static FIPS curve.** Simple, published, and testable, but usually one-peaked
   and weak for shift work or changing light schedules.
2. **Canonical RISE-like planning template.** Very actionable and stable, but it
   should be labeled “typical day plan,” not an individualized prediction.
3. **Dynamic light/sleep model.** Best scientific direction for phase-changing
   schedules, but it requires timestamped light/activity inputs, calibration,
   and outcome validation.

Research supports the third direction more than arbitrary peak amplification.
Individualized models using actual light and sleep outperform endogenous-only or
light-only variants in older adults:

<https://journals.plos.org/ploscompbiol/article?id=10.1371/journal.pcbi.1011743>

Real-time shift-worker work likewise shows that dynamic phase and actual sleep
matter as schedules diverge:

<https://academic.oup.com/sleep/article/46/9/zsad179/7221723>

Wearable light/activity modeling under rotating shifts reported about 0.95-hour
mean absolute phase error in its study population:

<https://pmc.ncbi.nlm.nih.gov/articles/PMC6667480/>

The 2024 mechanistic melatonin/SCN paper supplied for review is useful model
development, but it does not validate an individual's melatonin onset from
`wake + 14 hours`:

<https://pubmed.ncbi.nlm.nih.gov/39243938/>

CircaLog is especially relevant for Non-24 and drift visualization. It groups by
sleep-wake cycles rather than calendar days and recommends at least 14 days for
drift, but its own short-sleep nap threshold is described as needing refinement:

<https://github.com/sobhy0101/CircaLog>

Circadian analysis guidance also emphasizes visual inspection, individual
analysis, noise, and avoiding forced rhythms:

<https://pmc.ncbi.nlm.nih.gov/articles/PMC3663600/>

## Required external validation before claiming better peaks

A model replacement or parameter optimization should use a preregistered,
rolling-origin evaluation:

1. Collect at least 2–4 weeks per participant across regular, irregular, older,
   long-sleep, insomnia, nap, travel, and shift-work strata.
2. Capture timestamped KSS several times daily; optionally add short PVT tasks.
   For a smaller calibration cohort, collect a circadian phase marker such as
   DLMO. Record actual personal light exposure and activity if testing a dynamic
   model.
3. Compare the published FIPS baseline, a clearly labeled canonical two-peak
   planning template, and candidate dynamic models on held-out future days.
4. Measure KSS MAE, calibration, peak-time error, dip-detection precision/recall,
   abstention coverage, stability across missing uploads, and whether users
   successfully plan demanding tasks—not just visual similarity.
5. Select thresholds on training folds only. Report uncertainty and results by
   subgroup; do not use the same users/days to tune and declare improvement.
6. Ship a candidate only if it improves held-out outcomes without increasing
   confidently wrong days or notification churn.

Until that study exists, Meridian's best behavior is a stable, useful estimate
that abstains when its inputs or model domain do not support precision.
