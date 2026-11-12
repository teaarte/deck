#!/usr/bin/env bash
# shard.sh AREA... prints the packages of a shard of the tests: the packages of scenarios of each area, for rest
# every package no area names, for AREA/K/N the K-th of N parts of the tests of an area (every N-th by name).
set -euo pipefail
areas=(box chat egress modules owner providers runs)
for a in "$@"; do
  if [ "$a" = rest ]; then
    go list ./... | grep -vE "/tests/scenarios/($(IFS='|'; echo "${areas[*]}"))\$"
  elif [[ "$a" == */*/* ]]; then
    IFS=/ read -r area k n <<<"$a"
    names=$(grep -hoE '^func Test[A-Za-z0-9_]+\(t \*testing\.T\)' "tests/scenarios/$area"/*_test.go | sed -E 's/^func (Test[A-Za-z0-9_]+).*/\1/' | sort |
      awk -v k="$k" -v n="$n" 'NR % n == k % n' | paste -sd '|' -)
    [ -n "$names" ] || { echo "shard.sh: no tests in part $k of $n of $area" >&2; exit 1; }
    echo "-run=^($names)\$ ./tests/scenarios/$area"
  else
    echo "./tests/scenarios/$area"
  fi
done
