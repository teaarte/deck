#!/usr/bin/env bash
# golden.sh runs the golden flows of the web client in a real browser, then once more each that did not pass;
# DECK_SCREENSHOTS keeps the pictures of each try. A flow is a test of tests/scenarios/browser.
set -euo pipefail
flows=(
  TestRealBrowserSignsInWithAPasskey                    # sign-in by the code of a link, then by a passkey
  TestRealBrowserClientKeepsWhatItShows                 # a module screen, a document, a lost and restored connection
  TestRealBrowserWebClientII                            # forms, a file, a setting
  TestRealBrowserAPressAsksAndTheAnswerComesToTheFeed   # a press, its process, its question, the answer, the feed
  TestRealBrowserPressAnswersItsAsk                     # a press whose call waits for yes over the screen
  TestRealBrowserMasterChat                             # a chat turn with the reply of a fake
  TestRealBrowserARequestInWords                        # the request card, in the language of its topic
  TestRealBrowserRussianTodayAndChat                    # the client in the language of the settings
  TestRealBrowserRightsByModuleAndByRightRevokeAtOneTap # the rights screen
  TestRealBrowserTheHomeIsArrangedLikeAPhone            # the phone width
)
pkg=./tests/scenarios/browser
all=$(IFS='|'; echo "${flows[*]}")
listed=$(go test -list "^($all)\$" "$pkg" | grep -c '^Test' || true)
[ "$listed" = "${#flows[@]}" ] || { echo "golden.sh: $listed of the ${#flows[@]} flows are tests of $pkg" >&2; exit 1; }
shots=${DECK_SCREENSHOTS:-}
log=$(mktemp)
try() {
  [ -z "$shots" ] || export DECK_SCREENSHOTS="$shots/$1"
  DECK_REAL_BROWSER=1 go test -count=1 -timeout 10m -v -run "^($2)\$" "$pkg" 2>&1 | tee "$log"
}
# unpassed prints the flows of $1 with no PASS in the log, as one -run pattern; a skipped flow did not pass.
unpassed() {
  local passed
  passed=$(grep -oE '^--- PASS: Test[A-Za-z0-9_]+' "$log" | awk '{print $3}' || true)
  tr '|' '\n' <<<"$1" | { grep -vxF -f <(echo "${passed:-none}") || true; } | paste -sd '|' -
}
try 1 "$all" && [ -z "$(unpassed "$all")" ] && exit 0
again=$(unpassed "$all")
again=${again:-$all}
echo "golden.sh: once more: $again"
try 2 "$again" && [ -z "$(unpassed "$again")" ] && exit 0
left=$(unpassed "$again"); echo "golden.sh: did not pass: ${left:-the package of the flows}" >&2
exit 1
