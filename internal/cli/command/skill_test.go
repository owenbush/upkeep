package command

import (
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	upkeep "github.com/owenbush/upkeep"
	"github.com/owenbush/upkeep/internal/workflow"
)

// frontmatter is the part of a SKILL.md an agent runtime parses.
type frontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Metadata    struct {
		Author   string `yaml:"author"`
		Internal bool   `yaml:"internal"`
	} `yaml:"metadata"`
}

// skillFrontmatter splits the document the way a reader of it does.
func skillFrontmatter(t *testing.T) (frontmatter, string) {
	t.Helper()

	if !strings.HasPrefix(upkeep.Skill, "---\n") {
		t.Fatal("the skill does not open with YAML frontmatter")
	}

	rest := upkeep.Skill[len("---\n"):]
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		t.Fatal("the frontmatter is not closed")
	}

	var parsed frontmatter
	if err := yaml.Unmarshal([]byte(rest[:end]), &parsed); err != nil {
		t.Fatalf("the frontmatter does not parse: %v", err)
	}

	return parsed, rest[end+len("\n---\n"):]
}

// The frontmatter meets the published standard, so the skill stays installable
// and listable.
//
// name and description are the two required fields; name is lowercase with
// hyphens and at most 64 characters; description is the routing signal an agent
// matches against, capped at 1024. These are not upkeep's rules, so a test is
// the only thing that keeps them true as the file is edited.
func TestTheSkillMeetsTheFrontmatterStandard(t *testing.T) {
	parsed, _ := skillFrontmatter(t)

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

// metadata.internal hides a skill from discovery, which is the opposite of the
// point of publishing one.
func TestTheSkillIsNotMarkedInternal(t *testing.T) {
	parsed, _ := skillFrontmatter(t)

	if parsed.Metadata.Internal {
		t.Error("metadata.internal is set, which hides the skill from discovery")
	}
}

// The body stays under the 500 lines the standard asks for.
func TestTheSkillBodyStaysShortEnoughToBeRead(t *testing.T) {
	if lines := strings.Count(upkeep.Skill, "\n"); lines >= 500 {
		t.Errorf("the skill is %d lines; the standard asks for under 500", lines)
	}
}

// The safety section cannot be quietly deleted.
//
// The whole reason to ship a skill rather than rely on the docs is to tell an
// agent where to stop. A file that lost these three names would still install,
// still look reasonable, and no longer do the one job it exists for.
func TestTheSkillStillNamesWhatNotToRun(t *testing.T) {
	_, body := skillFrontmatter(t)

	// Within the section that says to stop, not anywhere in the file.
	//
	// Checking the whole body was the first version of this and it was too
	// weak: deleting the "needs-work posts a comment" bullet left the word in
	// a later paragraph about --dry-run, so the assertion passed while the
	// instruction was gone. A mutation run found that, not a reading.
	heading := "## Stop before anything that writes to drupal.org"
	start := strings.Index(body, heading)
	if start < 0 {
		t.Fatalf("the skill no longer has a %q section", heading)
	}
	section := body[start+len(heading):]
	if next := strings.Index(section, "\n## "); next >= 0 {
		section = section[:next]
	}

	// The bullet that enumerates it, not merely the name somewhere in the
	// section. Second weakness the mutation run found: the section also
	// explains that publish and needs-work take --yes and that merge refuses
	// outright, so all three names survive deleting the list that tells an
	// agent they are off limits.
	for _, name := range []string{"publish", "needs-work", "merge"} {
		if !strings.Contains(section, "\n- `"+name+"` — ") {
			t.Errorf("the stop section no longer lists %q as one not to run", name)
		}
	}
	if !strings.Contains(section, "unless the person asked") {
		t.Error("the stop section no longer says when it is allowed")
	}
	for _, rule := range []string{
		"--yes",         // that getting past the gate is not the agent's call
		"mr-<iid>",      // the branch a commit would be destroyed on
		"allow_failure", // why a green pipeline is not a clean one
		"--json",        // that it should not parse the tables
	} {
		if !strings.Contains(body, rule) {
			t.Errorf("the skill no longer explains %q", rule)
		}
	}
}

// `upkeep skill` prints the document and nothing else, so the documented
// redirect into a SKILL.md produces a valid one.
func TestSkillPrintsTheDocumentAlone(t *testing.T) {
	code, stdout, stderr := invoke(t, "skill")

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	if !strings.HasPrefix(stdout, "---\nname: upkeep\n") {
		t.Errorf("stdout does not open the way a SKILL.md must:\n%.60s", stdout)
	}
	if strings.TrimSpace(stdout) != strings.TrimSpace(upkeep.Skill) {
		t.Error("stdout is not the embedded skill")
	}
}
