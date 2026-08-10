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
            // Deliberately uninformative and identical for every unauthorised
            // path: a 404 that varied by route would map the surface for
            // anything probing the port.
            return Response::error('Not found.', 404);
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
     * The page, and the one moment the token moves from the URL into a cookie
     * so it stops living in the address bar, the history, and any screenshot.
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
