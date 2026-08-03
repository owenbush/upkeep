<?php

declare(strict_types=1);

namespace Upkeep\Gitlab;

/**
 * Base type for every non-success API outcome.
 *
 * GitlabClient methods never throw for HTTP-level outcomes; they return one of
 * the concrete subtypes so callers can pattern-match on the condition. The
 * hierarchy is sealed — every subtype is final and listed below — so a `match`
 * over it needs no `default` arm.
 *
 * The trigger conditions are exhaustive and mutually exclusive:
 *
 * | Type                | Trigger                                                      |
 * |---------------------|--------------------------------------------------------------|
 * | `Unauthorized`      | HTTP 401 — the PAT is missing, invalid, expired or unscoped.  |
 * | `EndpointClosed`    | HTTP 403 — the instance has not opened this endpoint to PATs. |
 * | `NotFound`          | HTTP 404 — the resource does not exist or is hidden from us.  |
 * | `RateLimited`       | HTTP 429.                                                     |
 * | `RequestRejected`   | Any other HTTP status >= 400.                                 |
 * | `MalformedResponse` | HTTP < 400 whose body is not a usable JSON array/object.      |
 * | `TransportError`    | No usable HTTP response at all (network, TLS, timeout, …).    |
 * | `ResourceMissing`   | A call SUCCEEDED but the asked-for item is not in its result. |
 *
 * Two accessors exist so consumers never re-derive the taxonomy:
 *
 * - `shortCode()` — the compact discriminator the dashboard and the command
 *   layer print; replaces reflection-based class-name sniffing.
 * - `isTransient()` — whether retrying could plausibly succeed. GitlabClient's
 *   per-run GET memoization keeps only non-transient outcomes, so one blip
 *   cannot poison a resource for the rest of the invocation.
 *
 * No subtype ever carries credential material: messages are built from the
 * status, the server's own error text and the browser URL only.
 */
abstract readonly class ApiFailure
{
    /**
     * @param string $message operator-facing description, never containing a token
     * @param ?int $status the HTTP status, when one was actually received
     * @param ?string $browserUrl where the maintainer can do this by hand, when known
     */
    protected function __construct(
        public string $message,
        public ?int $status = null,
        public ?string $browserUrl = null,
    ) {
    }

    /** Compact discriminator, e.g. "403", "rate-limited", "transport". */
    abstract public function shortCode(): string;

    /** Whether the same request could plausibly succeed if retried. */
    abstract public function isTransient(): bool;
}
