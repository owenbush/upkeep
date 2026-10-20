package invariant

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The published agent skill, held to the standard that makes it installable.
//
// It lives here rather than beside any command because nothing in the binary
// reads it: the file is the artifact, installed by `npx skills add
// owenbush/upkeep` or copied out of a release archive. So what is worth
// asserting is a property of the source tree, which is what this package is
// for — and none of these are upkeep's rules, so a test is the only thing that
// keeps them true as the file is edited.
const skillPath = "../../skills/upkeep/SKILL.md"

type skillFrontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Metadata    struct {
		Author   string `yaml:"author"`
		Internal bool   `yaml:"internal"`
	} `yaml:"metadata"`
}

// readSkill splits the document the way an agent runtime reading it does.
func readSkill(t *testing.T) (skillFrontmatter, string) {
	t.Helper()

	raw, err := os.ReadFile(filepath.Clean(skillPath))
	if err != nil {
		t.Fatalf("%v — the published skill is the artifact; it cannot go missing", err)
	}

	document := string(raw)
	if !strings.HasPrefix(document, "---\n") {
		t.Fatal("the skill does not open with YAML frontmatter")
	}

	rest := document[len("---\n"):]
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		t.Fatal("the frontmatter is not closed")
	}

	var parsed skillFrontmatter
	if err := yaml.Unmarshal([]byte(rest[:end]), &parsed); err != nil {
		t.Fatalf("the frontmatter does not parse: %v", err)
	}

	return parsed, rest[end+len("\n---\n"):]
}

// The frontmatter meets the skills.sh standard.
//
// name and description are its two required fields: name lowercase with
// hyphens and at most 64 characters, description non-empty and at most 1024.
// The description is the *routing signal* an agent matches a task against, so
// an empty one does not fail to install — it installs and never fires.
func TestTheSkillMeetsTheFrontmatterStandard(t *testing.T) {
	parsed, _ := readSkill(t)

	if !regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`).MatchString(parsed.Name) {
		t.Errorf("name %q is not lowercase-with-hyphens", parsed.Name)
	}
	if len(parsed.Name) > 64 {
		t.Errorf("name is %d characters, over the 64 limit", len(parsed.Name))
	}
	if strings.TrimSpace(parsed.Description) == "" {
		t.Error("description is empty, and it is the routing signal")
	}
	if len(parsed.Description) > 1024 {
		t.Errorf("description is %d characters, over the 1024 limit", len(parsed.Description))
	}
}

// metadata.internal hides a skill from discovery, which is the opposite of
// the point of publishing one.
func TestTheSkillIsNotMarkedInternal(t *testing.T) {
	parsed, _ := readSkill(t)

	if parsed.Metadata.Internal {
		t.Error("metadata.internal is set, which hides the skill from discovery")
	}
}

// The body stays under the 500 lines the standard asks for.
func TestTheSkillBodyStaysShortEnoughToBeRead(t *testing.T) {
	_, body := readSkill(t)

	if lines := strings.Count(body, "\n"); lines >= 500 {
		t.Errorf("the body is %d lines; the standard asks for under 500", lines)
	}
}

// It names nothing agent-specific.
//
// The skill is installed into 70-odd different harnesses by the skills CLI,
// and upkeep has no business assuming which. An earlier draft of the docs
// around it hardcoded one agent's directory throughout, which is the mistake
// this guards against coming back.
func TestTheSkillNamesNoParticularAgent(t *testing.T) {
	_, body := readSkill(t)
	lowered := strings.ToLower(body)

	for _, assumed := range []string{
		"~/.claude", ".claude/skills", "claude code", "cursor", "copilot", "openhands",
	} {
		if strings.Contains(lowered, assumed) {
			t.Errorf("the skill names %q; it must work for any agent", assumed)
		}
	}
}

// The section telling an agent where to stop still lists all three outward
// commands.
//
// A file that lost this list would still install, still read well, and no
// longer do the one job it exists for. Pinned to the bullets rather than to
// the names: the section also explains that publish and needs-work take --yes
// and that merge refuses outright, so every name survives deleting the list
// itself — which a mutation run found, twice.
func TestTheSkillStillListsWhatNotToRun(t *testing.T) {
	_, body := readSkill(t)

	heading := "## Stop before anything that writes to drupal.org"
	start := strings.Index(body, heading)
	if start < 0 {
		t.Fatalf("the skill no longer has a %q section", heading)
	}
	section := body[start+len(heading):]
	if next := strings.Index(section, "\n## "); next >= 0 {
		section = section[:next]
	}

	for _, name := range []string{"publish", "needs-work", "merge"} {
		if !strings.Contains(section, "\n- `"+name+"` — ") {
			t.Errorf("the stop section no longer lists %q as one not to run", name)
		}
	}
	if !strings.Contains(section, "unless the person asked") {
		t.Error("the stop section no longer says when it is allowed")
	}
}

// And the rules an agent would otherwise get wrong are still explained.
func TestTheSkillStillExplainsTheTraps(t *testing.T) {
	_, body := readSkill(t)

	for _, rule := range []string{
		"--yes",         // that getting past the gate is not the agent's call
		"mr-<iid>",      // the branch a commit would be destroyed on
		"allow_failure", // why a green pipeline is not a clean one
		"--json",        // that it should not parse the tables
		"merge ref",     // that the branch passing is not CI passing
	} {
		if !strings.Contains(body, rule) {
			t.Errorf("the skill no longer explains %q", rule)
		}
	}
}
