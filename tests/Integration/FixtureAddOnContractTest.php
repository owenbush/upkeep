<?php

declare(strict_types=1);

namespace Upkeep\Tests\Integration;

use PHPUnit\Framework\TestCase;
use Symfony\Component\HttpClient\HttpClient;
use Upkeep\Adapter\FixtureAddOn;

/**
 * That the fixture commands upkeep invokes are the ones the add-on publishes.
 *
 * They were not. The adapter ran `ddev upkeep-fixture-load` and probed for
 * `.ddev/commands/host/upkeep-fixture-load`; owenbush/ddev-upkeep has always
 * shipped `fixture-load`, under that name, in its README, its bats tests and
 * its recorded end-to-end run — the `upkeep-` prefix appears there only on
 * snapshot names and environment variables. So `--fixture=NAME` could not have
 * worked, and the installed-probe looked for a file that is never written,
 * which re-fetched the add-on on every single call.
 *
 * The unit suite was green on it and could not have been anything else: the
 * engine fake records whatever string it is handed, and the marker fixture is
 * built from the same constant under test, so both halves agreed with each
 * other and with nothing real. One fact, two strings, two repositories, and
 * nothing comparing them.
 *
 * This compares them. It needs the add-on rather than docker, so it lives in
 * the integration suite without extending IntegrationTestCase — there is no
 * ddev project for it to skip on.
 */
final class FixtureAddOnContractTest extends TestCase
{
    /** Tokens that can read a private repository, in the order tried. */
    private const TOKEN_ENV = ['UPKEEP_GITHUB_TOKEN', 'GH_TOKEN', 'GITHUB_TOKEN'];

    /**
     * The invocation and the probe are one constant now, so they cannot drift
     * from each other. This is the half they can still drift from.
     */
    public function testUpkeepInvokesCommandsTheAddOnActuallyPublishes(): void
    {
        $manifest = $this->manifest();

        self::assertStringContainsString(
            FixtureAddOn::MARKER,
            $manifest,
            sprintf(
                'The add-on does not install "%s", so `ddev %s` does not exist and the installed-probe '
                . "never matches.\nWhat it does install:\n%s",
                FixtureAddOn::MARKER,
                FixtureAddOn::LOAD_COMMAND,
                $manifest,
            ),
        );
    }

    /**
     * And that the probe really is derivable from the command name: ddev names
     * a host command after the file it came from, which is the only reason one
     * constant can serve both.
     */
    public function testTheProbeIsTheCommandFile(): void
    {
        self::assertSame('commands/host/' . FixtureAddOn::LOAD_COMMAND, FixtureAddOn::MARKER);
    }

    /**
     * The add-on's install manifest, from a local checkout if there is one and
     * from the API otherwise.
     *
     * The local path first because `UPKEEP_ADDON_SOURCE` already exists for
     * exactly this — it is how the add-on is developed against upkeep — and
     * because a checkout needs no credential. Both repositories are private
     * while upkeep is unreleased, so the API read needs a token and the test
     * says so rather than reporting a 404 as a contract violation.
     */
    private function manifest(): string
    {
        $source = getenv(FixtureAddOn::SOURCE_ENV);
        if (\is_string($source) && is_file($source . '/install.yaml')) {
            return (string) file_get_contents($source . '/install.yaml');
        }

        $token = '';
        foreach (self::TOKEN_ENV as $name) {
            $value = getenv($name);
            if (\is_string($value) && $value !== '') {
                $token = $value;
                break;
            }
        }

        // Tried with or without a credential. A token is needed only while the
        // add-on is private, so asking for one up front would outlive the
        // reason for it: publication is supposed to make the secret
        // unnecessary, not leave a test skipping itself until somebody
        // notices. The request decides, not a guess about access.
        $headers = [
            'Accept' => 'application/vnd.github.raw+json',
            'User-Agent' => 'upkeep-contract-test',
        ];
        if ($token !== '') {
            $headers['Authorization'] = 'Bearer ' . $token;
        }

        $url = sprintf('https://api.github.com/repos/%s/contents/install.yaml', FixtureAddOn::NAME);

        try {
            return HttpClient::create()->request('GET', $url, [
                'timeout' => 10,
                'max_duration' => 30,
                'headers' => $headers,
            ])->getContent();
        } catch (\Throwable $e) {
            self::markTestSkipped(sprintf(
                "Could not read %s: %s\n%s",
                $url,
                $e->getMessage(),
                $token === ''
                    ? sprintf(
                        'The add-on is private, so this needs a credential: point %s at a checkout, or set one '
                        . 'of %s.',
                        FixtureAddOn::SOURCE_ENV,
                        implode(', ', self::TOKEN_ENV),
                    )
                    : 'A token was supplied and the read still failed, so it may not cover that repository.',
            ));
        }
    }
}
