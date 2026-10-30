#!/usr/bin/env bash
# covered.sh fails unless the shards in ran/ ran every test of tests/scenarios.
set -euo pipefail
go test -list '.*' ./tests/scenarios | grep '^Test' | sort > all.txt
cat ran/*.txt | sort -u > ran.txt
missing=$(comm -23 all.txt ran.txt)
if [ -n "$missing" ]; then
  echo "no shard ran:"
  echo "$missing"
  exit 1
fi
echo "the shards ran all $(wc -l < all.txt) tests"
