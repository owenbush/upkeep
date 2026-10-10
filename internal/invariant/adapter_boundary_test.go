package invariant

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Engine specifics live only in internal/adapter.
//
// The rest of the orchestrator talks to adapter.Engine, obtained from an
// injected factory, and must stay engine-agnostic. If a change needs engine
// knowledge outside the adapter, the interface grows a method instead.
//
// The PHP does this with `grep -ri "ddev" src/ --exclude-dir=Adapter`, and the
// `-i` there is load-bearing: the guard was once written case-sensitively and
// passed only because five command classes said `DdevContribAdapter` with a
// capital D — nominally satisfied, substantively breached.
//
// This checks **code** — string literals, identifiers and imports — and not
// comments. That is a deliberate narrowing of the grep, for a reason the grep
// demonstrates itself: on the PHP side it currently fires on a docblock in
// Command/LiveStatus.php that names `ddev start` as the example of a wedged
// child, and Maintenance/Category.php calls a project volume "a docker named
// volume" because that is what one is. Neither is the orchestrator reaching
// past the boundary; both are prose explaining why something exists. What
// would be a breach is a command *running* the engine, naming its binary, or
// typing its concrete adapter — and all three of those are code.
//
// Tests are out of scope, which is the PHP guard's scope too: it reads src/
// and not tests/. A test may reproduce real engine output as scenery —
// internal/proc has one standing in for a wedged `ddev start` stalled on
// Mutagen, and the string being the real one is what makes it recognisable.
// The property that would actually matter for a test, that it does not
// *invoke* the engine, is covered by the check that only internal/proc starts
// a child process at all.
func TestOnlyTheAdapterKnowsAboutTheEngine(t *testing.T) {
	// Every spelling of an engine detail that has no business in code outside
	// the adapter. Lower-case: the comparison folds case, so a capitalised
	// type name cannot slip past the way it did in PHP.
	forbidden := []string{"ddev", "docker", "colima", "mutagen", "drush", "podman"}

	var offenders []string
	fileSet := token.NewFileSet()

	err := filepath.WalkDir("..", func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}

		slashed := filepath.ToSlash(path)
		// The adapter is where this knowledge belongs, and this file names the
		// engine in order to look for it.
		if strings.Contains(slashed, "/adapter/") || strings.HasSuffix(slashed, "_test.go") {
			return nil
		}

		// Parsed without comments, which is the whole point: prose about the
		// engine is documentation, code about it is coupling.
		file, parseErr := parser.ParseFile(fileSet, path, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			return parseErr
		}

		ast.Inspect(file, func(node ast.Node) bool {
			var text string
			switch typed := node.(type) {
			case *ast.BasicLit:
				if typed.Kind == token.STRING {
					text = typed.Value
				}
			case *ast.Ident:
				text = typed.Name
			case *ast.ImportSpec:
				text = typed.Path.Value
			}
			if text == "" {
				return true
			}

			lowered := strings.ToLower(text)
			for _, word := range forbidden {
				if strings.Contains(lowered, word) {
					offenders = append(offenders, slashed+":"+
						fileSet.Position(node.Pos()).String()[len(path)+1:]+" — "+text)
				}
			}

			return true
		})

		return nil
	})
	if err != nil {
		t.Fatalf("walking the source: %v", err)
	}

	if len(offenders) > 0 {
		t.Errorf(
			"engine knowledge in code outside internal/adapter:\n  %s\n"+
				"Grow adapter.Engine instead of reaching past it.",
			strings.Join(offenders, "\n  "),
		)
	}
}
