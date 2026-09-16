package adapter

import (
	"github.com/owenbush/upkeep/internal/baseartifact"
	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/proc"
	"github.com/owenbush/upkeep/internal/security"
)

// DdevContribFactory is the production engine factory.
//
// The only thing that assembles the engine, and it lives inside this package
// where engine specifics belong. It also owns the one decision that was
// otherwise made separately in every command that starts a child: the redactor
// each line is filtered through before it reaches a log, a failure message, or
// a persisted result.
type DdevContribFactory struct {
	redactor *security.Redactor
}

// NewDdevContribFactory builds the factory. A nil redactor masks nothing,
// which is only ever right in a test.
func NewDdevContribFactory(redactor *security.Redactor) *DdevContribFactory {
	return &DdevContribFactory{redactor: redactor}
}

// Build is the engine for one cockpit.
//
// stageLog receives upkeep's own progress; processLog receives a child's
// output, and onIdle fires as each child exits — the one moment nothing can be
// mid-print, so a status line never ends up glued to the front of a results
// table.
func (f *DdevContribFactory) Build(
	where *cockpit.Cockpit,
	projectsRootOption string,
	stageLog Log,
	processLog func(string),
	onIdle func(),
) (Engine, error) {
	projectsRoot, err := ResolveProjectsRoot(projectsRootOption, where.Root)
	if err != nil {
		return nil, err
	}

	return NewDdevContrib(
		baseartifact.NewLayout(where.BaseArtifactsPath()),
		projectsRoot,
		proc.New(processLog, onIdle, f.redactor),
		stageLog,
	), nil
}

// BuildArtifacts is the base-artifact builder for one cockpit.
//
// On the factory for the same reason Build is: assembling it needs a
// ThrowawaySite, which is engine-specific (it configures and starts a
// disposable project to run the install in), and nothing outside this package
// may name one.
//
// A second method on the same interface rather than a second factory, because
// the two share the decision that matters — the redactor every child's output
// is filtered through — and a separate seam is a second place to forget it.
func (f *DdevContribFactory) BuildArtifacts(
	where *cockpit.Cockpit,
	scratchDir string,
	stageLog Log,
	processLog func(string),
	onIdle func(),
) *baseartifact.Builder {
	runner := proc.New(processLog, onIdle, f.redactor)

	return baseartifact.NewBuilder(
		baseartifact.NewLayout(where.BaseArtifactsPath()),
		NewThrowawaySite(runner, stageLog),
		scratchDir,
		runner,
		stageLog,
	)
}
