#!/usr/bin/env bash
set -euo pipefail
: "${CRON_SECRET:?CRON_SECRET must be set}"
base_url=${CRON_API_URL:-http://127.0.0.1:8080}
base_url=${base_url%/}
case "$CRON_SECRET" in
  *$'\n'*|*$'\r'*) echo "CRON_SECRET must be a single line" >&2; exit 1 ;;
esac

case "${1:-}" in
  hourly) jobs=(close-expired-quizzes abandon-stale-attempts assignment-reminders teacher-notifications) ;;
  nightly) jobs=(expire-points reset-streaks rank-change-alerts recompute-question-difficulty purge-onboarding-sessions purge-attempt-behavior end-inactive-enrollments) ;;
  streak-nudges) jobs=(streak-nudges) ;;
  announcements) jobs=(dispatch-announcements) ;;
  weekly-snapshot) jobs=(snapshot-leaderboard) ;;
  weekly-digests) jobs=(weekly-digests weekly-insights-email) ;;
  *) echo "Unknown cron group: ${1:-missing}" >&2; exit 1 ;;
esac

# Readiness retries are safe GETs. POSTs are deliberately not retried: a timeout
# may occur after a side effect has committed, and a retry could duplicate it.
curl --fail --silent --show-error --max-time 5 --connect-timeout 3 \
  --retry 5 --retry-connrefused --retry-delay 2 "$base_url/ready" > /dev/null
for job in "${jobs[@]}"; do
  echo "Starting $job"
  # Feed the secret via stdin so it does not appear in curl's process arguments.
  printf 'X-Cron-Secret: %s\n' "$CRON_SECRET" | \
    curl --fail --silent --show-error --connect-timeout 5 --max-time 900 \
      --request POST --header @- --output /dev/null \
      "$base_url/api/v1/internal/cron/$job"
  echo "Completed $job"
done
