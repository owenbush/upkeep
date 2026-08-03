<?php

declare(strict_types=1);

namespace Upkeep\Filesystem;

/**
 * Path canonicalisation and containment.
 *
 * Containment is decided on canonicalised paths, never by pattern-matching
 * for `..`: a lexical check passes a symlink whose target is outside the root,
 * which is precisely the escape worth catching. `realpath()` resolves both
 * traversal segments and symlinks, so one comparison covers both.
 *
 * `realpath()` returns false for a path that does not exist, so a path being
 * created is canonicalised through its deepest existing ancestor and the
 * not-yet-existing tail re-appended. A `..` inside that tail cannot be
 * resolved against anything real and is refused rather than collapsed
 * lexically.
 */
final class PathGuard
{
    /**
     * The absolute, symlink-free spelling of a path, which need not exist yet.
     *
     * @throws FilesystemException when the path is empty or carries an
     *                             unresolvable traversal segment
     */
    public static function canonicalize(string $path): string
    {
        if ($path === '') {
            throw new FilesystemException('Cannot resolve an empty path.');
        }

        $real = realpath($path);
        if ($real !== false) {
            return $real;
        }

        // Walk up to the deepest ancestor realpath() can resolve, then
        // re-append the tail. The walk is bounded by the number of path
        // segments and its base case is the filesystem root, which is its own
        // canonical spelling and so needs no resolving — there is no way for
        // this to run off the top of the tree.
        $segments = array_values(array_filter(explode('/', self::absolute($path)), static fn (string $s) => $s !== ''));
        $tail = [];
        while ($segments !== []) {
            array_unshift($tail, (string) array_pop($segments));
            if ($segments === []) {
                break;
            }

            $realParent = realpath('/' . implode('/', $segments));
            if ($realParent !== false) {
                return self::join($realParent, $tail, $path);
            }
        }

        return self::join('/', $tail, $path);
    }

    /**
     * Whether $path resolves to $root itself or to something beneath it. Both
     * sides are canonicalised first, so a symlinked or `..`-spelled escape is
     * reported as outside even though the raw strings look contained.
     */
    public static function isWithin(string $root, string $path): bool
    {
        $realRoot = rtrim(self::canonicalize($root), '/');
        $realPath = rtrim(self::canonicalize($path), '/');

        return $realPath === $realRoot || str_starts_with($realPath, $realRoot . '/');
    }

    /**
     * @param list<string> $tail segments that do not exist on disk yet
     */
    private static function join(string $realParent, array $tail, string $original): string
    {
        foreach ($tail as $segment) {
            if ($segment === '..' || $segment === '.') {
                throw new FilesystemException(sprintf(
                    'Refusing the path "%s": it contains a traversal segment ("%s") below a directory that does '
                    . 'not exist, so it cannot be resolved to a real location.',
                    $original,
                    $segment,
                ));
            }
        }

        return rtrim($realParent, '/') . '/' . implode('/', $tail);
    }

    private static function absolute(string $path): string
    {
        if (str_starts_with($path, '/')) {
            return $path;
        }

        $cwd = getcwd();
        if ($cwd === false) {
            throw new FilesystemException(sprintf(
                'Cannot resolve the relative path "%s": the current working directory is unavailable (it may have '
                . 'been deleted). Re-run from an existing directory or pass an absolute path.',
                $path,
            ));
        }

        return rtrim($cwd, '/') . '/' . $path;
    }
}
