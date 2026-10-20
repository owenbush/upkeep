// Package upkeep embeds the published agent skill.
//
// The file has to be readable two ways, and that is why this package exists at
// the module root rather than under internal/.
//
// As a **repository file**, so `npx skills add owenbush/upkeep` finds it: the
// skills CLI behind skills.sh searches standard locations for a directory
// holding a SKILL.md, and `skills/<name>/SKILL.md` is the documented flat
// layout. Only one such directory lives here — adding a second under
// `.claude/skills/` would publish two skills from one repository.
//
// And from **inside the binary**, so `upkeep skill` cannot describe commands
// the binary does not have. Somebody on a release installing the skill from
// main would otherwise get instructions for a newer tool than the one they
// are running.
//
// go:embed cannot reach above its own package and there was no Go file at the
// module root, so the embed lives here where the skill directory is. A symlink
// from skills/ into internal/ was the alternative and is worse twice over:
// go:embed ignores symlinks, and a symlink in a release archive is the bug
// that nearly shipped the LICENSE as a dangling link.
package upkeep

import _ "embed"

// Skill is the agent skill, verbatim.
//
// Including its YAML frontmatter, because what `upkeep skill` writes has to be
// a valid SKILL.md on its own — the installation is a redirect into a file the
// agent runtime then parses.
//
//go:embed skills/upkeep/SKILL.md
var Skill string
