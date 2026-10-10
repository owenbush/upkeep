package adapter

import (
	"fmt"
	"os"
	"strings"

	"github.com/owenbush/upkeep/internal/filesystem"
)

// RequireUnderHome is the $HOME containment rule for any directory whose
// contents get bind-mounted into the Docker VM.
//
// This is a functional requirement first and a hardening measure second: macOS
// Docker providers (colima, Docker Desktop) share only the home directory by
// default, so a project or scratch tree created outside it can never start.
// Refusing up front turns that into an explicable error instead of an opaque
// mount failure minutes into a provision.
//
// Containment is decided on the canonicalised path, so neither a ".." sequence
// nor a symlink pointing out of $HOME satisfies it.
//
// what is what the path is, for the message ("projects root"); howToSet is how
// the user supplies it ("--projects-root or $UPKEEP_PROJECTS_ROOT").
func RequireUnderHome(candidate, what, howToSet string) (string, error) {
	home, err := Home(what, howToSet)
	if err != nil {
		return "", err
	}

	resolved, err := filesystem.Canonicalize(candidate)
	if err != nil {
		return "", fmt.Errorf("cannot use %q as the %s: %w", candidate, what, err)
	}
	contained, err := filesystem.IsWithin(home, resolved)
	if err != nil {
		return "", fmt.Errorf("cannot use %q as the %s: %w", candidate, what, err)
	}

	if !contained {
		return "", fmt.Errorf(
			"refusing the %s %q (resolves to %q): it is outside your home directory %q. Its contents are "+
				"bind-mounted into the Docker VM, and macOS Docker providers (colima, Docker Desktop) only share "+
				"the home directory by default — an environment created outside it can never start. Point %s at a "+
				"path under %q",
			what, candidate, resolved, home, howToSet, home,
		)
	}

	return resolved, nil
}

// Home is the home directory every mountable path must sit under.
func Home(what, howToSet string) (string, error) {
	home := os.Getenv("HOME")
	if home == "" {
		return "", fmt.Errorf(
			"cannot resolve a %s: $HOME is not set, so there is no default and nothing to verify a given path "+
				"against. Refusing to fall back to a temp dir — Docker providers only mount the home directory, "+
				"so anything created outside it can never start. Set $HOME, or point %s inside it",
			what, howToSet,
		)
	}

	return home, nil
}

// ProjectsRootEnvVar names the environment variable that sets the projects
// root.
const ProjectsRootEnvVar = "UPKEEP_PROJECTS_ROOT"

const projectsRootWhat = "projects root"

// ResolveProjectsRoot resolves the directory that holds all engine-managed
// module environments.
//
// Resolution order: the explicit --projects-root flag, the
// UPKEEP_PROJECTS_ROOT environment variable, a projects/ directory inside the
// cockpit when it exists, then ~/.upkeep/projects.
//
// Every one of those sources is then canonicalised and required to stay under
// $HOME. That applies to the explicit flag and the environment variable
// exactly as it applies to the default: there is no escape hatch, and adding
// one is a decision rather than a patch.
func ResolveProjectsRoot(explicit, cockpitRoot string) (string, error) {
	candidate, err := projectsRootCandidate(explicit, cockpitRoot)
	if err != nil {
		return "", err
	}

	return RequireUnderHome(candidate, projectsRootWhat, projectsRootHowToSet())
}

func projectsRootCandidate(explicit, cockpitRoot string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if fromEnv := os.Getenv(ProjectsRootEnvVar); fromEnv != "" {
		return fromEnv, nil
	}
	if cockpitRoot != "" {
		cockpitProjects := strings.TrimRight(cockpitRoot, "/") + "/projects"
		if info, err := os.Stat(cockpitProjects); err == nil && info.IsDir() {
			return cockpitProjects, nil
		}
	}

	home, err := Home(projectsRootWhat, projectsRootHowToSet())
	if err != nil {
		return "", err
	}

	return home + "/.upkeep/projects", nil
}

func projectsRootHowToSet() string { return "--projects-root or $" + ProjectsRootEnvVar }
