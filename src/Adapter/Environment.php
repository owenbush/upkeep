<?php

declare(strict_types=1);

namespace Upkeep\Adapter;

/**
 * A provisioned (module x core-version) maintenance environment, as returned
 * by EngineAdapterInterface::ensureEnv() and consumed by every per-MR
 * operation. Callers treat it as an opaque handle plus display data — the
 * project layout inside it is adapter-private.
 */
final readonly class Environment
{
    public function __construct(
        public string $moduleName,
        public string $coreMajor,
        public string $projectName,
        public string $projectPath,
        public string $primaryUrl,
        /** True when ensure_env reused an existing healthy environment instead of provisioning. */
        public bool $reused,
    ) {
    }
}
