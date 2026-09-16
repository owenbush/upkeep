package adapter

import (
	"encoding/json"
	"fmt"
	"regexp"
)

// Wires the module working copy into a seeded project via a Composer path
// repository.
//
// Chosen over the engine's symlink-project mechanism because upkeep seeds the
// full project tree from the canonical base artifact, and the engine's
// mechanism assumes the module IS the project root — which cannot host the
// per-(module x core) matrix from one checkout. A path repository with
// symlink:true makes Composer install the module as a symlink into
// web/modules/contrib/<module> and resolve its dependencies properly, while
// Composer never owns or writes into the checkout itself: applying a merge
// request can mutate the working copy freely and no composer operation will
// clobber it.

// versionLikeBranch is Composer's own branch normalisation test.
var versionLikeBranch = regexp.MustCompile(`^v?\d+(\.(\d+|[xX*]))*$`)

// DevConstraintForBranch is the dev version Composer assigns to a checked-out
// branch of a path repository.
//
// Per Composer's own branch normalisation: version-like branches ("1.0.x",
// "2.x", "11.1") become "<branch>-dev", anything else ("main", "8.x-1.x")
// becomes "dev-<branch>". Requiring exactly this constraint pins the working
// copy's branch and can never drift to a different dev branch published on
// packages.drupal.org.
func DevConstraintForBranch(branch string) string {
	if versionLikeBranch.MatchString(branch) {
		return branch + "-dev"
	}

	return "dev-" + branch
}

// WithPathRepository returns composer.json content with the path repository
// prepended, so it outranks packages.drupal.org — Composer honours repository
// order, and the first repository providing a package wins.
func WithPathRepository(composerJSON, url string) (string, error) {
	// Ordered, because composer.json is a file a person reads and a Go map
	// would reorder every key in it.
	var document orderedJSON
	if err := json.Unmarshal([]byte(composerJSON), &document); err != nil {
		return "", fmt.Errorf("project composer.json is not parseable: %w", err)
	}
	if !document.isObject {
		return "", fmt.Errorf("project composer.json must decode to an object")
	}

	existing, present := document.get("repositories")
	repositories := []any{}
	if present {
		list, isList := existing.([]any)
		if !isList {
			return "", fmt.Errorf(
				"project composer.json \"repositories\" must be a list; refusing to rewrite it",
			)
		}
		// Decoded JSON is untrusted: a repository entry that is not an object
		// is not one of ours, so it is kept rather than dropped.
		for _, repo := range list {
			if !isOurPathRepository(repo, url) {
				repositories = append(repositories, repo)
			}
		}
	}

	ours := map[string]any{
		"type": "path", "url": url, "options": map[string]any{"symlink": true},
	}
	document.set("repositories", append([]any{ours}, repositories...))

	encoded, err := document.marshalIndent()
	if err != nil {
		return "", fmt.Errorf("cannot render composer.json: %w", err)
	}

	return string(encoded) + "\n", nil
}

func isOurPathRepository(repo any, url string) bool {
	fields, isObject := repo.(map[string]any)
	if !isObject {
		return false
	}

	return fields["type"] == "path" && fields["url"] == url
}
