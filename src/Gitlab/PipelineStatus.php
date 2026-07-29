<?php

declare(strict_types=1);

namespace Upkeep\Gitlab;

/**
 * GitLab pipeline status, mapped from the API's string values. Unrecognized
 * (future) statuses map to Unknown rather than crashing the caller.
 */
enum PipelineStatus: string
{
    case Created = 'created';
    case WaitingForResource = 'waiting_for_resource';
    case Preparing = 'preparing';
    case Pending = 'pending';
    case Running = 'running';
    case Success = 'success';
    case Failed = 'failed';
    case Canceled = 'canceled';
    case Skipped = 'skipped';
    case Manual = 'manual';
    case Scheduled = 'scheduled';
    case Unknown = 'unknown';

    public static function fromApi(string $status): self
    {
        return self::tryFrom($status) ?? self::Unknown;
    }

    /**
     * Whether the pipeline has finished with a green result — the only value
     * the fast-lane gate treats as CI-passing.
     */
    public function isGreen(): bool
    {
        return $this === self::Success;
    }
}
