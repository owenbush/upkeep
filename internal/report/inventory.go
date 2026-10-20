package report

import (
	"time"

	"github.com/owenbush/upkeep/internal/maintenance"
)

// InventoryPayload is `status`.
type InventoryPayload struct {
	Cockpit      string          `json:"cockpit"`
	ProjectsRoot string          `json:"projects_root"`
	Modules      int             `json:"registered_modules"`
	Items        []InventoryItem `json:"items"`
	TotalBytes   int64           `json:"total_bytes"`
}

// InventoryItem is one thing on disk.
type InventoryItem struct {
	Path     string `json:"path"`
	Category string `json:"category"`
	Bytes    int64  `json:"bytes"`
	Module   string `json:"module,omitempty"`
	Core     string `json:"core,omitempty"`
	Project  string `json:"project,omitempty"`
	// LastUsedAt is null when nothing records it, which the prune selector
	// reads as "cannot tell" rather than "old". A zero timestamp would read as
	// 1970 and make it the first thing deleted.
	LastUsedAt *time.Time `json:"last_used_at"`
	// Protection is why a prune would refuse to delete this — "keep" for an
	// explicit mark, "canonical" for a base artifact or a fixture dump — and
	// empty when nothing protects it. The reason rather than a boolean,
	// because the two protections are lifted in different ways.
	Protection string `json:"protection,omitempty"`
}

// Inventory builds the disk payload.
func Inventory(
	cockpit, projectsRoot string, modules int, items []maintenance.Item,
	protection func(maintenance.Item) string,
) InventoryPayload {
	lines := make([]InventoryItem, 0, len(items))
	var total int64

	for _, item := range items {
		total += item.Size

		line := InventoryItem{
			Path:     item.Path,
			Category: item.Category.Label(),
			Bytes:    item.Size,
			Module:   item.Module,
			Core:     item.CoreMajor,
			Project:  item.ProjectName,
		}
		if !item.LastUsedAt.IsZero() {
			at := item.LastUsedAt
			line.LastUsedAt = &at
		}
		if protection != nil {
			line.Protection = protection(item)
		}

		lines = append(lines, line)
	}

	return InventoryPayload{
		Cockpit:      cockpit,
		ProjectsRoot: projectsRoot,
		Modules:      modules,
		Items:        lines,
		TotalBytes:   total,
	}
}
