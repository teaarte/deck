#!/usr/bin/env bash
# race.sh N I runs shard I of N under the race detector: every N-th test of tests/scenarios by name, and in shard 0
# the other packages; ran-I.txt lists the tests of tests/scenarios it ran.
set -euo pipefail
n=$1 i=$2
go test -list '.*' ./tests/scenarios | grep '^Test' | sort | awk -v n="$n" -v i="$i" 'NR % n == i' > "mine-$i.txt"
if [ "$i" = 0 ]; then
  go test -race -count=1 $(go list ./... | grep -v '/tests/scenarios$')
fi
go test -race -count=1 -timeout 15m -v -run "^($(paste -sd'|' "mine-$i.txt"))\$" ./tests/scenarios 2>&1 | tee "out-$i.txt" |
  grep -E '^(--- FAIL|FAIL|ok|panic)|_test\.go:[0-9]+:' || true
grep -oE '^--- (PASS|FAIL|SKIP): Test[A-Za-z0-9_]+' "out-$i.txt" | awk '{print $3}' | sort -u > "ran-$i.txt"
grep -qE '^ok[[:space:]]+deck2/tests/scenarios' "out-$i.txt"
