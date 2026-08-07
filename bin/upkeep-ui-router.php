<?php

/**
 * Front controller for `upkeep ui`, run under PHP's built-in server.
 *
 * The only place in the UI that touches a superglobal or emits a byte.
 * Everything above it is Request in, Response out, which is what makes the
 * whole surface testable without a socket — and this file small enough to read
 * in one go, which is the other half of trusting it.
 *
 * It is started by Command\UiCommand and receives its configuration through
 * the environment, because a command line is visible to every process on the
 * machine and the launch token is not for them.
 */

declare(strict_types=1);

use Upkeep\Cockpit\Cockpit;
use Upkeep\Security\SecretRedactor;
use Upkeep\Ui\Api;
use Upkeep\Ui\Assets;
use Upkeep\Ui\Http\Request;
use Upkeep\Ui\Jobs\JobLauncher;
use Upkeep\Ui\Jobs\JobStore;
use Upkeep\Ui\LaunchToken;
use Upkeep\Ui\StateBuilder;

foreach ([__DIR__ . '/../../../autoload.php', __DIR__ . '/../vendor/autoload.php'] as $autoload) {
    if (file_exists($autoload)) {
        require $autoload;
        break;
    }
}

$token = (string) getenv('UPKEEP_UI_TOKEN');
$cockpitPath = (string) getenv('UPKEEP_UI_COCKPIT');
$binary = (string) getenv('UPKEEP_UI_BINARY');

if ($token === '' || $cockpitPath === '' || $binary === '') {
    http_response_code(500);
    header('Content-Type: text/plain');
    echo "The UI server was started without its configuration.\n";

    return true;
}

$cockpit = new Cockpit($cockpitPath);
$store = new JobStore($cockpit->uiJobsPath());

$api = new Api(
    LaunchToken::of($token),
    new StateBuilder($cockpit),
    $store,
    new JobLauncher(
        $store,
        $binary,
        $cockpit->root,
        // Detached: the request that starts a check returns in milliseconds
        // while the check itself runs for minutes. Nothing waits on it — the
        // job's own shell wrapper records the exit status where JobStore
        // reconciles it.
        static function (string $commandLine): void {
            $handle = @popen('(' . $commandLine . ') >/dev/null 2>&1 &', 'r');
            if (\is_resource($handle)) {
                pclose($handle);
            }
        },
        static fn (): string => (new DateTimeImmutable())->format(DateTimeInterface::ATOM),
    ),
    Assets::bundled(),
    SecretRedactor::fromEnvironment(),
);

$response = $api->handle(Request::fromGlobals(
    $_SERVER,
    $_GET,
    $_COOKIE,
    (string) file_get_contents('php://input'),
));

http_response_code($response->status);
foreach ($response->headers as $name => $value) {
    header($name . ': ' . $value);
}
echo $response->body;

return true;
