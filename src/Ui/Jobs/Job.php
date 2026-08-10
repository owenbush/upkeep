<?php

declare(strict_types=1);

namespace Upkeep\Ui\Jobs;

/**
 * One backgrounded CLI invocation, as the browser sees it.
 *
 * A job *is* an `upkeep` command — the UI shells out to the same binary the
 * operator would type. That is the whole reason the web layer cannot drift
 * from the CLI: exit codes, adapter behaviour, and secret redaction are not
 * reimplemented here, they are inherited by running the thing that already
 * does them.
 */
final readonly class Job
{
    public const RUNNING = 'running';
    public const SUCCEEDED = 'succeeded';
    public const FAILED = 'failed';
    public const INFRASTRUCTURE = 'infrastructure';

    /**
     * @param list<string> $argv     the upkeep arguments, without the binary
     * @param ?int         $exitCode null while still running
     */
    public function __construct(
        public string $id,
        public string $label,
        public array $argv,
        public string $state,
        public ?int $exitCode,
        public string $startedAt,
        public ?string $finishedAt,
    ) {
    }

    /**
     * The exit-code contract, read back as a state. Same three meanings the
     * CLI has: 0 did what was asked, 1 the supervised work failed, 2 upkeep
     * could not do the job.
     */
    public static function stateFor(?int $exitCode): string
    {
        return match ($exitCode) {
            null => self::RUNNING,
            0 => self::SUCCEEDED,
            1 => self::FAILED,
            default => self::INFRASTRUCTURE,
        };
    }

    public function isFinished(): bool
    {
        return $this->state !== self::RUNNING;
    }

    /** @return array<string, mixed> */
    public function toArray(): array
    {
        return [
            'id' => $this->id,
            'label' => $this->label,
            'argv' => $this->argv,
            'state' => $this->state,
            'exit_code' => $this->exitCode,
            'started_at' => $this->startedAt,
            'finished_at' => $this->finishedAt,
        ];
    }

    /**
     * @param array<array-key, mixed> $data
     */
    public static function fromArray(array $data): ?self
    {
        $id = $data['id'] ?? null;
        $startedAt = $data['started_at'] ?? null;
        if (!\is_string($id) || $id === '' || !\is_string($startedAt)) {
            return null;
        }

        $argv = [];
        foreach (\is_array($data['argv'] ?? null) ? $data['argv'] : [] as $arg) {
            if (\is_string($arg)) {
                $argv[] = $arg;
            }
        }

        $exitCode = \is_int($data['exit_code'] ?? null) ? $data['exit_code'] : null;

        return new self(
            $id,
            \is_string($data['label'] ?? null) ? $data['label'] : $id,
            $argv,
            \is_string($data['state'] ?? null) ? $data['state'] : self::stateFor($exitCode),
            $exitCode,
            $startedAt,
            \is_string($data['finished_at'] ?? null) ? $data['finished_at'] : null,
        );
    }
}
