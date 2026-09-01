<?php

declare(strict_types=1);

namespace Upkeep\Ui;

use Upkeep\Security\SecretRedactor;
use Upkeep\Ui\Http\Request;
use Upkeep\Ui\Http\Response;
use Upkeep\Ui\Jobs\Job;
use Upkeep\Ui\Jobs\JobAction;
use Upkeep\Ui\Jobs\JobLauncher;
use Upkeep\Ui\Jobs\JobStore;

/**
 * The entire request surface, as one pure function from Request to Response.
 *
 * Nothing here touches a superglobal, sets a header, or echoes: the front
 * controller does that, once, with what this returns. Which is what makes the
 * whole UI — auth, routing, actions, log streaming — testable without opening
 * a socket.
 *
 * Every request is authenticated before it is routed. There is no public
 * prefix, not even for assets: the page and the API are equally behind the
 * launch token, because a served asset is still evidence that a
 * credential-holding process is listening on this port.
 */
final readonly class Api
{
    public function __construct(
        private LaunchToken $token,
        private StateBuilder $state,
        private JobStore $jobs,
        private JobLauncher $launcher,
        private Assets $assets,
        private SecretRedactor $redactor,
    ) {
    }

    public function handle(Request $request): Response
    {
        if (!$this->token->matches($request->token())) {
            return $this->unauthorised($request);
        }

        return match (true) {
            $request->path === '/' => $this->page($request),
            $request->path === '/app.js' => Response::asset($this->assets->script(), 'text/javascript'),
            $request->path === '/app.css' => Response::asset($this->assets->style(), 'text/css'),
            $request->path === '/api/state' => $this->stateResponse(),
            $request->path === '/api/jobs' => $this->jobsResponse($request),
            $request->segment(0) === 'api' && $request->segment(1) === 'jobs' => $this->jobResponse($request),
            default => Response::error('Not found.', 404),
        };
    }

    /**
     * Every refusal is a flat, identical 404 — a response that varied by route
     * would map the surface for anything probing the port — with exactly one
     * exception.
     *
     * A person navigating to the page itself gets an explanation. The token is
     * minted per run, so a tab left open across a restart — or a bookmark —
     * presents one the server no longer accepts, and a flat refusal tells them
     * nothing about which of many possible things is wrong.
     *
     * It costs nothing to say so. Anything probing this port already knows
     * something answers on it, and the actions stay behind the same flat 404
     * they always were.
     */
    private function unauthorised(Request $request): Response
    {
        if ($request->method === 'GET' && $request->path === '/') {
            return Response::html($this->assets->expiredPage(), 401);
        }

        return Response::error('Not found.', 404);
    }

    /**
     * The page, and the moment the token is also placed in a cookie.
     *
     * The cookie is not a duplicate: the page's own subresources —
     * `/app.js`, `/app.css` — and its API calls carry no query string, so
     * something has to authenticate them, and a cookie does it without
     * threading the token through every request the client makes. It also
     * gets `SameSite=Strict`, which keeps it off cross-site requests entirely.
     *
     * The token deliberately *stays* in the address bar. Removing it read as
     * tidier and made the tool strand people: a restart mints a new token, and
     * a tab whose URL had been cleaned had nothing left to present and no way
     * to find the new link except the terminal it was printed in. Leaving it
     * there means the current link is always recoverable from the address bar
     * or browser history. The cost is that a screenshot of the window shows
     * the token for the life of that run, which is the better trade for a
     * process that exits when you press Ctrl-C.
     */
    private function page(Request $request): Response
    {
        $response = Response::html($this->assets->page());

        return isset($request->query['token'])
            ? $response->withCookie('upkeep_ui', $this->token->value)
            : $response;
    }

    private function stateResponse(): Response
    {
        return Response::json($this->state->build());
    }

    private function jobsResponse(Request $request): Response
    {
        if ($request->method !== 'POST') {
            return Response::json([
                'jobs' => array_map(static fn (Job $j): array => $j->toArray(), $this->jobs->all()),
            ]);
        }

        $action = JobAction::build(
            (string) $request->bodyString('action'),
            array_filter([
                'module' => $request->bodyString('module'),
                'core' => $request->bodyString('core'),
                'mr' => $request->bodyString('mr'),
                'issue' => $request->bodyString('issue'),
            ], static fn (?string $v): bool => $v !== null),
        );

        if ($action === null) {
            return Response::error('That is not something this UI can run.', 422);
        }

        return Response::json(['job' => $this->launcher->launch($action)->toArray()], 202);
    }

    /**
     * GET /api/jobs/<id>?offset=N — the job, plus whatever it has written
     * since byte N.
     *
     * The captured output is redacted on the way out. The job is upkeep, which
     * does not print its own token, but this log is the one artefact of the
     * whole system that is written by a credentialled process and then handed
     * to a browser, so it gets the belt as well as the braces.
     */
    private function jobResponse(Request $request): Response
    {
        $id = $request->segment(2);
        $job = $id === null ? null : $this->jobs->find($id);
        if ($id === null || $job === null) {
            return Response::error('No such job.', 404);
        }

        $offset = $request->query['offset'] ?? '0';
        $chunk = $this->jobs->readFrom($id, ctype_digit($offset) ? (int) $offset : 0);

        return Response::json([
            'job' => $job->toArray(),
            'output' => $this->redactor->redact($chunk['output']),
            'offset' => $chunk['offset'],
            'complete' => $chunk['complete'] && $job->isFinished(),
        ]);
    }
}
