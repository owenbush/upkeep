package adapter

import (
	"path/filepath"
	"strings"
)

// The engine add-on's per-project snapshot layout.
//
// Owned by the ddev-upkeep add-on and mirrored here as the one place outside
// the add-on that may know it: materialised fixture snapshots live at
// <project>/.ddev/upkeep/materialized/<name>.sql, with a sidecar meta file at
// <project>/.ddev/upkeep/snapshots/<name>.meta recording, among other facts,
// materialized_at=<iso8601>.
const (
	SnapshotMaterializedDir = ".ddev/upkeep/materialized"
	SnapshotMetaDir         = ".ddev/upkeep/snapshots"

	// SnapshotArtifactSuffix is what a materialised snapshot is named.
	SnapshotArtifactSuffix = ".sql"

	// SnapshotMetaSuffix is what its sidecar is named.
	SnapshotMetaSuffix = ".meta"
)

// SnapshotMetaPathFor maps materialized/<name>.sql to the project's
// snapshots/<name>.meta, reporting false when the path is not a materialised
// snapshot artifact at all.
func SnapshotMetaPathFor(artifactPath string) (string, bool) {
	materialized := filepath.Dir(artifactPath)
	if filepath.Base(materialized) != filepath.Base(SnapshotMaterializedDir) {
		return "", false
	}

	name := strings.TrimSuffix(filepath.Base(artifactPath), SnapshotArtifactSuffix)

	return filepath.Join(
		filepath.Dir(materialized), filepath.Base(SnapshotMetaDir), name+SnapshotMetaSuffix,
	), true
}
