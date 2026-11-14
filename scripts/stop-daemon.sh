#!/usr/bin/env bash
set -euo pipefail
home=${1:?usage: stop-daemon.sh <kapi home>}
pids=$(lsof -t "$home/kapi.lock" 2>/dev/null) || exit 0
kill -TERM $pids
for _ in $(seq 100); do
  lsof -t "$home/kapi.lock" >/dev/null 2>&1 || exit 0
  sleep 0.1
done
echo "daemon $pids did not stop within 10s" >&2
exit 1
