#!/usr/bin/env bash
#
# Runs an upkeep command in the nightly full check and decides whether its
# outcome is a problem.
#
# The exit-code contract is 0 did what was asked / 1 the supervised work failed
# / 2 upkeep could not do the job. For a nightly, 0 and 1 are both fine: a
# module whose tests fail is not a broken tool.
#
# 2 is where it gets interesting, and where this job got it wrong. Two very
# different things share that code. upkeep could not do the job because upkeep
# is broken — the thing this nightly exists to catch. Or upkeep could not do
# the job because the world moved: a pinned patch stopped applying to a branch
# that has since changed, a pinned merge request got merged. Those are correct
# refusals, they are what the tool is *for*, and they will happen to any step
# that names live drupal.org state — eventually, and then permanently.
#
# So a step may declare which refusals are expected. An expected one passes
# with a warning, because the step did stop exercising what it was there for
# and that should be visible without waking anybody. Anything else fails.
#
# usage: expect-upkeep.sh <label> <expected-refusal-regex|-> -- <command...>
set -uo pipefail

label="$1"
expected="$2"
shift 2
[ "${1:-}" = "--" ] && shift

output="$("$@" 2>&1)"
status=$?
printf '%s\n' "$output"
echo "exit=$status"

if [ "$status" -ne 2 ]; then
  exit 0
fi

# Matched against the output with its line breaks flattened. Symfony wraps an
# error block to the terminal width, so the real refusal arrives as
# "... does not\n  apply to ..." — a line-based match on the phrase would never
# see it, and the tolerance would silently never apply.
flattened="$(tr '\n' ' ' <<<"$output" | tr -s '[:space:]' ' ')"

if [ "$expected" != "-" ] && grep -qiE "$expected" <<<"$flattened"; then
  echo "::warning::${label}: upkeep refused, correctly — the pinned drupal.org state has moved on."
  echo "::warning::${label}: everything after the refusal went unexercised tonight. Re-point the step to keep covering it."
  exit 0
fi

echo "::error::${label}: exit 2, and not a refusal this step expects. upkeep could not do the job."
exit 1
