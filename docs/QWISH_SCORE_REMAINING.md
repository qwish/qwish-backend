# Qwish Score — Remaining Implementation

This file tracks the work left after the first scoring-model phase.

## Completed

- Score display range is 100–900.
- Accuracy uses a confidence-adjusted 50/50 prior.
- Streak progression uses a smooth exponential curve.
- Activity progression uses a smooth diminishing-return curve.
- Attempt scoring and lifetime Insights share the same component calculator.
- Backend regression tests cover the new confidence and engagement behavior.

## Remaining work

### 1. Validate the new formula with real data

- Export an anonymized sample of completed attempts.
- Compare old and new scores by question count, accuracy, streak, activity, and topic coverage.
- Check that new users do not start too high and that established learners are not unexpectedly reset.
- Choose and document final curve constants and priors based on these results.
- Define score-band labels and product expectations for the 100–900 range.

### 2. Add score confidence to the API

- Expose a confidence/sample-size field alongside `qwish_score`.
- Distinguish a provisional score from a score based on sufficient evidence.
- Add an API contract and client rendering for the provisional state.

### 3. Add curriculum breadth

- Count distinct practiced domains and subdomains from completed responses.
- Require a minimum number of answered questions before a topic contributes to breadth.
- Add a bounded breadth component to the score.
- Return breadth details in the Insights breakdown.
- Add tests for repeated practice in one topic versus balanced practice.

### 4. Add versatility and confidence calibration

- Confirm which question types support confidence measurement.
- Aggregate correct, incorrect, and overconfident responses safely.
- Add a bounded versatility component with a low-sample fallback.
- Ensure missing confidence data does not unfairly lower a learner’s score.

### 5. Improve difficulty calibration

- Verify nightly question-difficulty recalculation and its monitoring.
- Add safeguards against small-sample difficulty swings.
- Version or snapshot the difficulty value used for a completed attempt so historical scores remain explainable.
- Test that difficulty cannot be manipulated by a small group of attempts.

### 6. Make score computation operationally safe

- Avoid repeating the full aggregation on every Insights request as usage grows.
- Recompute after a completed attempt or maintain a cached/materialized learner score.
- Add timing and query-volume metrics.
- Define cache invalidation and backfill behavior.

### 7. Review speed scoring

- Confirm that client/server timing is trustworthy and bounded.
- Reduce sensitivity to device latency, accessibility needs, and interrupted sessions.
- Add an explicit missing-time-data policy.
- Test anti-bot handling without punishing legitimate fast answers.

### 8. Roll out safely

- Add a feature flag for the revised score.
- Shadow-calculate old and new scores before changing the displayed value.
- Monitor score movement, completion rate, repeat attempts, and learner complaints.
- Decide whether historical scores should be backfilled or only new completions should use the new model.
- Update the app’s score explanations, badges, rankings, and documentation together.

## Recommended order

1. Validate constants and score movement with anonymized data.
2. Add confidence/sample-size metadata.
3. Add breadth with low-sample protection.
4. Add versatility and improve difficulty calibration.
5. Move aggregation to an event-driven or cached calculation.
6. Feature-flag and roll out gradually.

Do not ship the full seven-factor model until the shadow calculation shows that score movement is understandable, bounded, and aligned with learner outcomes.
