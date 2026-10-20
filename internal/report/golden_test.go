package report

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/owenbush/upkeep/internal/check"
	"github.com/owenbush/upkeep/internal/dashboard"
	"github.com/owenbush/upkeep/internal/drupal"
	"github.com/owenbush/upkeep/internal/gate"
	"github.com/owenbush/upkeep/internal/gitlab"
	"github.com/owenbush/upkeep/internal/maintenance"
	"github.com/owenbush/upkeep/internal/patches"
)

// update rewrites the golden files instead of comparing against them.
//
//	go test ./internal/report/ -update
//
// Then read the diff. That is the point of the whole arrangement: a change to
// a published field shows up in review as a line somebody has to agree to,
// rather than as a silently different answer in a consumer's script.
var update = flag.Bool("update", false, "rewrite the golden payloads")

// These payloads are a published contract, so their shape is pinned.
//
// The same arrangement as the corpora the port is held to: committed answers,
// compared byte for byte. The inputs are fixed literals rather than anything
// scanned, so a diff here is always a change to the mapping or to the shape —
// never to the data that happened to be on a machine.
func TestTheWirePayloadsHoldTheirShape(t *testing.T) {
	for name, payload := range map[string]any{
		"dashboard":      Overview(goldenRows(), goldenSnapshots()),
		"dashboard-rows": Rows(goldenRows()),
		"issues":         Contributions(goldenContributions(), func(patches.Contribution) string { return "upkeep start pathauto 3597857" }),
		"check":          CheckRun(goldenRun(), goldenSubject(), "smoke"),
		"status":         Inventory("/c", "/c/projects", 2, goldenItems(), goldenProtection),
		"review":         goldenReview(),
		"publish":        goldenPublish(),
		"patch-promote":  goldenPromote(),
	} {
		t.Run(name, func(t *testing.T) {
			// The same encoder settings EmitDocument uses, so what is pinned
			// is what a consumer receives.
			var rendered bytes.Buffer
			encoder := json.NewEncoder(&rendered)
			encoder.SetIndent("", "  ")
			encoder.SetEscapeHTML(false)
			if err := encoder.Encode(NewEnvelope("1.0.0", name, payload, nil)); err != nil {
				t.Fatalf("encoding: %v", err)
			}

			path := filepath.Join("testdata", name+".json")
			if *update {
				if err := os.WriteFile(path, rendered.Bytes(), 0o644); err != nil {
					t.Fatalf("writing %s: %v", path, err)
				}

				return
			}

			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v — regenerate with: go test ./internal/report/ -update", err)
			}
			if !bytes.Equal(want, rendered.Bytes()) {
				t.Errorf("the %s payload changed shape.\n\nwant:\n%s\n\ngot:\n%s\n\n"+
					"If the change is intended: go test ./internal/report/ -update",
					name, want, rendered.Bytes())
			}
		})
	}
}

func goldenTime() time.Time {
	return time.Date(2026, 10, 19, 9, 30, 0, 0, time.UTC)
}

func goldenIssue() drupal.Issue {
	return drupal.Issue{
		Nid:      3597857,
		Title:    "Config schema for the form display settings",
		Status:   drupal.StatusNeedsReview,
		Priority: 300,
		Version:  "8.x-1.x",
		URL:      "https://www.drupal.org/node/3597857",
		Files: []drupal.IssueFile{
			{Name: "fix-1.patch", URL: "https://www.drupal.org/files/fix-1.patch"},
		},
	}
}

func goldenMergeRequest() gitlab.MergeRequest {
	return gitlab.MergeRequest{
		IID:          40,
		Title:        "Issue #3597857: Config schema",
		SourceBranch: "3597857-config-schema",
		WebURL:       "https://git.drupalcode.org/project/pathauto/-/merge_requests/40",
		HeadSHA:      "a1b2c3d4e5f6",
		HeadPipeline: &gitlab.Pipeline{Status: gitlab.StatusFailed, RawStatus: "failed"},
	}
}

func goldenRows() []dashboard.Row {
	mr := goldenMergeRequest()
	contribution := patches.Contribution{
		Module:        "pathauto",
		Issue:         goldenIssue(),
		MergeRequests: []gitlab.MergeRequest{mr},
	}

	return []dashboard.Row{{
		Module:        "pathauto",
		Branch:        "8.x-1.x",
		IssueNid:      goldenIssue().Nid,
		Contribution:  &contribution,
		MergeRequests: []gitlab.MergeRequest{mr},
		MergeRequest:  &mr,
		Verdict:       &gate.Verdict{Status: gate.ReadyAuto, Reasons: []string{"ci-red"}},
	}}
}

func goldenSnapshots() map[string]dashboard.ModuleSnapshot {
	return map[string]dashboard.ModuleSnapshot{
		"pathauto": {FetchedAt: goldenTime()},
	}
}

func goldenContributions() []patches.Contribution {
	return []patches.Contribution{{
		Module:        "pathauto",
		Issue:         goldenIssue(),
		MergeRequests: []gitlab.MergeRequest{goldenMergeRequest()},
	}}
}

func goldenRun() check.RunResult {
	one := 1
	zero := 0

	return check.RunResult{Results: []check.Result{
		{
			Type: check.PhpUnit, Status: check.Passed,
			ExitCode: &zero, Duration: 91200 * time.Millisecond,
		},
		{
			Type: check.PhpCs, Status: check.Failed, ExitCode: &one,
			Duration: 1200 * time.Millisecond,
			Output:   "FILE: src/Thing.php\n  12 | ERROR | Missing doc comment",
		},
	}}
}

func goldenSubject() Subject {
	return Subject{
		Kind: "merge-request", Module: "pathauto", Core: "11",
		IID: 40, Revision: "a1b2c3d4e5f6",
	}
}

func goldenItems() []maintenance.Item {
	return []maintenance.Item{
		{
			Path: "/c/projects/upkeep-pathauto-d11", Category: maintenance.ProjectTree,
			Size: 1 << 30, Module: "pathauto", CoreMajor: "11",
			ProjectName: "upkeep-pathauto-d11", LastUsedAt: goldenTime(),
		},
		{
			Path: "/c/base-artifacts/11", Category: maintenance.BaseArtifact,
			Size: 512 << 20, CoreMajor: "11",
		},
	}
}

func goldenProtection(item maintenance.Item) string {
	if item.Category == maintenance.BaseArtifact {
		return "canonical"
	}

	return ""
}

func goldenReview() ReviewPayload {
	return ReviewPayload{
		Module: "pathauto", Core: "11", IID: 40,
		Title:    "Issue #3597857: Config schema",
		SiteURL:  "https://upkeep-pathauto-d11.ddev.site",
		LoginURL: "https://upkeep-pathauto-d11.ddev.site/user/reset/1/abc",
		Fixture:  "smoke",
	}
}

func goldenPublish() PublishPayload {
	mr := mergeRequestRef(goldenMergeRequest())

	return PublishPayload{
		Module: "pathauto", Issue: 3597857, Branch: "3597857-config-schema",
		Destination: "issue/pathauto-3597857", SHA: "a1b2c3d4e5f6",
		MergeRequest: mr, Created: true, TargetBranch: "8.x-1.x",
	}
}

func goldenPromote() PromotePayload {
	return PromotePayload{
		Module: "pathauto", Issue: 3597857, Branch: "3597857-config-schema",
		Committed: true, SHA: "a1b2c3d4e5f6", Rejects: []string{},
	}
}
