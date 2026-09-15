#!/usr/bin/env bash
#
# The coverage floor, per package.
#
# The PHP side enforces 100% lines through a PHPUnit extension registered in
# phpunit.xml.dist, so a bare `vendor/bin/phpunit` enforces exactly what CI
# does. This is the port of that gate, and it is deliberately not a single
# number: statement coverage is not line coverage, and one overall figure lets
# a well-covered package pay for a bare one.
#
# Each package's floor is where it actually stands, so the only direction it
# can move is up. Raising a floor after improving a package is part of the
# change; lowering one is a decision, and it has to be written here where
# somebody reviewing the diff will see it.
#
# A package with no test files is not exempt — it is listed at 0 with a reason,
# or it gets tests.
set -euo pipefail

cd "$(dirname "$0")"

# package                     floor  why it is not higher
FLOORS=$(
	cat <<'EOF'
internal/adapter              95.4
internal/baseartifact         96.2
internal/check               100.0
internal/cockpit              93.3
internal/config              100.0
internal/dashboard            96.7
internal/drupal               78.5
internal/filesystem           87.2
internal/gate                 97.3
internal/gitlab               88.5
internal/maintenance         100.0
internal/naming              100.0
internal/notes               100.0
internal/patches              96.0
internal/proc                 92.3
internal/results              95.8
internal/security             93.9
internal/workflow            100.0
EOF
)

# cmd/livecheck is excluded: it is a live-verification tool that talks to
# drupal.org and git.drupalcode.org, so the only thing a test could cover is a
# mock of the thing it exists to not mock.
# internal/invariant holds checks over the source itself and has no code of its
# own to cover.

status=0
while read -r package floor; do
	[ -z "$package" ] && continue

	actual=$(go test -cover "./$package" 2>/dev/null |
		sed -n 's/.*coverage: \([0-9.]*\)% of statements.*/\1/p')

	if [ -z "$actual" ]; then
		printf 'FAIL  %-28s no coverage measured — does it have tests?\n' "$package"
		status=1
		continue
	fi

	if awk "BEGIN { exit !($actual < $floor) }"; then
		printf 'FAIL  %-28s %5s%%  below its floor of %s%%\n' "$package" "$actual" "$floor"
		status=1
	elif awk "BEGIN { exit !($actual > $floor + 0.05) }"; then
		printf 'RAISE %-28s %5s%%  above its floor of %s%% — raise it in coverage.sh\n' \
			"$package" "$actual" "$floor"
		status=1
	else
		printf 'ok    %-28s %5s%%\n' "$package" "$actual"
	fi
done <<<"$FLOORS"

# A package nobody listed is a package nobody is holding to anything.
for package in $(go list ./... | sed 's#github.com/owenbush/upkeep/##' |
	grep -v '^cmd/livecheck$' | grep -v '^internal/invariant$'); do
	if ! grep -q "^$package  *[0-9]" <<<"$FLOORS"; then
		printf 'FAIL  %-28s is not listed in coverage.sh\n' "$package"
		status=1
	fi
done

exit "$status"
