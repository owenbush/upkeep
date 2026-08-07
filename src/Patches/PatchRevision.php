<?php

declare(strict_types=1);

namespace Upkeep\Patches;

/**
 * What identifies the exact patch a cached check result is about.
 *
 * A merge request has a head SHA, and the whole results cache is built on
 * comparing that against what is there now. A patch needs an equivalent, and
 * the constraint that decides which one is where the comparison happens: the
 * dashboard has to judge a cached result stale or current *without*
 * downloading anything, from the attachment list alone.
 *
 * So the identity is the patch's source URL, hashed into the same shape a SHA
 * has. drupal.org mints a distinct URL for every upload — a re-roll posted
 * under an identical filename lands at `..._0.patch`, `..._1.patch` and so on
 * — which makes the URL a faithful stand-in for "which upload was this".
 *
 * Known limitation: a URL whose *content* changes underneath it (a gist edited
 * in place, reachable through `--url`) is not detected. That trade is made
 * deliberately: the alternative, hashing the bytes, cannot be evaluated by a
 * dashboard that has not fetched them, and a LOCAL column that silently
 * omitted patch rows would be worse than one that trusts drupal.org's own
 * upload semantics.
 */
final class PatchRevision
{
    public static function of(string $url): string
    {
        return sha1($url);
    }
}
