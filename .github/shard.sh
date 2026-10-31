#!/usr/bin/env bash
# shard.sh AREA... prints the packages of a shard of the tests: the packages of scenarios of each area, and for rest
# every package no area names.
set -euo pipefail
areas=(box chat egress modules owner providers runs)
for a in "$@"; do
  if [ "$a" = rest ]; then
    go list ./... | grep -vE "/tests/scenarios/($(IFS='|'; echo "${areas[*]}"))\$"
  else
    echo "./tests/scenarios/$a"
  fi
done
