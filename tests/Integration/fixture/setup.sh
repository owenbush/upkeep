#!/usr/bin/env bash
#
# Builds the ddev project the container contract tests run against.
#
# Deliberately NOT a Drupal site. Everything these tests are about — what
# `ddev exec` does to a command, and whether a vendor-relative sniff path
# resolves — is a fact about the *layout*, not about Drupal. A bare PHP
# project starts in about a minute where installing Drupal takes fifteen, and
# it is the slow, heavy half that makes container CI flaky and distrusted.
#
# What it reproduces from ddev-drupal-contrib:
#   - vendor/ at the project root, NOT inside the module;
#   - the module at $DDEV_DOCROOT/$DRUPAL_PROJECTS_PATH/<name>;
#   - DRUPAL_PROJECTS_PATH exported into the web container.
#
# Usage:  tests/Integration/fixture/setup.sh <dir>
# Then:   UPKEEP_DDEV_PROJECT=<dir> vendor/bin/phpunit --testsuite=integration
set -euo pipefail

PROJECT="${1:?usage: setup.sh <project-dir>}"
NAME="upkeep-contract-$$"

mkdir -p "$PROJECT/web/modules/contrib/widget/src"
cd "$PROJECT"

ddev config \
  --project-name="$NAME" \
  --project-type=php \
  --docroot=web \
  --php-version=8.3 \
  --disable-settings-management \
  --web-environment-add="DRUPAL_PROJECTS_PATH=modules/contrib" \
  --auto

cat > composer.json <<'JSON'
{
    "name": "upkeep/contract-fixture",
    "description": "Layout fixture for upkeep's container contract tests.",
    "require-dev": {
        "drupal/coder": "^8.3",
        "phpstan/phpstan": "^2.0",
        "squizlabs/php_codesniffer": "^3.7"
    },
    "config": {
        "allow-plugins": {
            "dealerdirect/phpcodesniffer-composer-installer": true
        }
    }
}
JSON

# The module's own ruleset, in the long-path form real modules use and the one
# that broke: it resolves only if the check runs where vendor/ actually is.
cat > web/modules/contrib/widget/phpcs.xml.dist <<'XML'
<?xml version="1.0"?>
<ruleset name="upkeep-fixture">
  <description>Deliberately references the Drupal standard by vendor path.</description>
  <arg name="extensions" value="php"/>
  <rule ref="./vendor/drupal/coder/coder_sniffer/Drupal"/>
</ruleset>
XML

# A file the Drupal standard objects to and nothing else would: no file doc
# comment. Naming the sniff in the output is what proves the module's own
# ruleset was loaded rather than some default lying in the working directory.
cat > web/modules/contrib/widget/src/Widget.php <<'PHP'
<?php

namespace Drupal\widget;

class Widget {
  public function name() {
    return 'widget';
  }
}
PHP

cat > web/modules/contrib/widget/phpstan.neon <<'NEON'
parameters:
  level: 0
NEON

# The root config the fallback chain uses. Present so the guarded download is
# skipped: what that test is about is whether the braces and the && chain
# survive the trip, not whether drupalcode is reachable from CI.
cat > phpstan.neon <<'NEON'
parameters:
  level: 0
NEON
touch phpstan-baseline.neon

ddev start
ddev composer install --no-interaction --no-progress

echo
echo "Fixture ready. Run the contract tests with:"
echo "  UPKEEP_DDEV_PROJECT=$(pwd) vendor/bin/phpunit --testsuite=integration --no-coverage"
