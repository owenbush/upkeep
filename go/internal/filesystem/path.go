package filesystem

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Canonicalize resolves path to a real location, following symlinks.
//
// A path that does not exist yet is still resolved: the walk finds the deepest
// ancestor that does exist, canonicalises that, and re-appends the tail. The
// walk is bounded by the number of segments and its base case is the
// filesystem root, which is its own canonical spelling, so it cannot run off
// the top of the tree.
func Canonicalize(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("cannot resolve an empty path")
	}

	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real, nil
	}

	absolute, err := absolute(path)
	if err != nil {
		return "", err
	}

	segments := []string{}
	for _, segment := range strings.Split(absolute, string(filepath.Separator)) {
		// "." is dropped as well as "", which the PHP side does not do.
		// A "." resolves to the directory it sits in on every filesystem, so
		// removing it changes nothing about where the path points — where
		// removing a ".." would, which is why that one is left for the
		// ancestor walk below to handle honestly.
		//
		// It matters for containment: a protected root compared against
		// "/root/./child" would otherwise fail to match, and this function's
		// contract is that a differently-spelled path to the same place is the
		// same place. Only reachable for a path that does not exist — an
		// existing one is normalised by EvalSymlinks above — so nothing in
		// upkeep reaches it today.
		if segment != "" && segment != "." {
			segments = append(segments, segment)
		}
	}

	var tail []string
	for len(segments) > 0 {
		tail = append([]string{segments[len(segments)-1]}, tail...)
		segments = segments[:len(segments)-1]
		if len(segments) == 0 {
			break
		}

		// strings.Join, never filepath.Join: Join calls Clean, which folds a
		// ".." into the segment before it. Rebuilt that way, a path like
		// "…/missing/../elsewhere" resolves against a parent that only exists
		// once the traversal has been folded away — which is the escape this
		// function exists to refuse, performed by the function doing the
		// refusing.
		parent := string(filepath.Separator) + strings.Join(segments, string(filepath.Separator))
		if real, err := filepath.EvalSymlinks(parent); err == nil {
			return join(real, tail, path)
		}
	}

	return join(string(filepath.Separator), tail, path)
}

// IsWithin reports whether path resolves to root itself or to something
// beneath it.
//
// Both sides are canonicalised first, so a symlinked or ".."-spelled escape is
// reported as outside even though the raw strings look contained.
func IsWithin(root, path string) (bool, error) {
	realRoot, err := Canonicalize(root)
	if err != nil {
		return false, err
	}
	realPath, err := Canonicalize(path)
	if err != nil {
		return false, err
	}

	realRoot = strings.TrimRight(realRoot, string(filepath.Separator))
	realPath = strings.TrimRight(realPath, string(filepath.Separator))

	return realPath == realRoot || strings.HasPrefix(realPath, realRoot+string(filepath.Separator)), nil
}

// join re-appends the segments that do not exist on disk yet.
//
// A traversal segment below a directory that does not exist cannot be resolved
// to a real location — there is nothing to resolve it against — so it is
// refused rather than folded away textually, which is how a containment check
// gets talked past.
func join(realParent string, tail []string, original string) (string, error) {
	for _, segment := range tail {
		if segment == ".." || segment == "." {
			return "", fmt.Errorf(
				"refusing the path %q: it contains a traversal segment (%q) below a directory that does not "+
					"exist, so it cannot be resolved to a real location",
				original, segment,
			)
		}
	}

	return strings.TrimRight(realParent, string(filepath.Separator)) +
		string(filepath.Separator) + strings.Join(tail, string(filepath.Separator)), nil
}

func absolute(path string) (string, error) {
	if filepath.IsAbs(path) {
		return path, nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf(
			"cannot resolve the relative path %q: the current working directory is unavailable (it may have been "+
				"deleted). Re-run from an existing directory or pass an absolute path",
			path,
		)
	}

	return filepath.Join(cwd, path), nil
}
