#!/usr/bin/env bash
set -euo pipefail
if [[ $EUID -ne 0 ]]; then
  echo "Run with sudo: sudo bash deploy/cron/install.sh" >&2
  exit 1
fi
source_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
if ! id qwish-cron > /dev/null 2>&1; then
  useradd --system --no-create-home --user-group --shell /usr/sbin/nologin qwish-cron
fi
install -d -m 0755 /usr/local/lib/qwish
install -d -m 0700 /etc/qwish
install -m 0755 "$source_dir/run-cron.sh" /usr/local/lib/qwish/run-cron.sh
install -m 0644 "$source_dir/qwish-cron@.service" /etc/systemd/system/
for timer in "$source_dir"/*.timer; do
  install -m 0644 "$timer" /etc/systemd/system/
done
if [[ ! -e /etc/qwish/cron.env ]]; then
  install -m 0600 "$source_dir/cron.env.example" /etc/qwish/cron.env
fi
systemd-analyze verify /etc/systemd/system/qwish-cron@.service /etc/systemd/system/qwish-cron-*.timer
systemctl daemon-reload
echo "Units installed. Set /etc/qwish/cron.env, disable other schedulers, then enable the six timers."
