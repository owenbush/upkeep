package command

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/owenbush/upkeep/internal/adapter"
	"github.com/owenbush/upkeep/internal/check"
	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/drupal"
	"github.com/owenbush/upkeep/internal/report"
	"github.com/owenbush/upkeep/internal/workflow"
)

// jsonCase is one command exercised in JSON mode.
type jsonCase struct {
	// want is the command name the envelope should carry, which is not always
	// the command's own name: `dashboard` emits dashboard-rows when a module
	// narrows it.
	want string
	run  func(t *testing.T) (code int, stdout string)
}

// jsonCases covers every command that offers --json.
//
// Explicit rather than generic because each command needs different fakes —
// an engine, a GitLab stub, an issue source — and a harness general enough to
// drive all nine would be a second command tree to keep right. What makes the
// table honest instead is TestEveryJSONCommandIsExercised below: a command
// that declares the trait and is missing here fails the suite.
func jsonCases() map[string]jsonCase {
	return map[string]jsonCase{
		"issues": {want: "issues", run: func(t *testing.T) (int, string) {
			root := aDashboardCockpit(t)
			server := aQuietGitlab(t)
			code, stdout, _ := runIssuesCommand(t,
				scriptedIssues{issues: aQueue()}, scriptedClients{client: gitlabClientFor(server)},
				"issues", "pathauto", "--json", "--cockpit="+root)

			return code, stdout
		}},

		"patches": {want: "patches", run: func(t *testing.T) (int, string) {
			root := aDashboardCockpit(t)
			server := aQuietGitlab(t)
			code, stdout, _ := runDashboardCommand(t, server,
				scriptedIssues{issues: aQueue()},
				"patches", "--json", "--cockpit="+root)

			return code, stdout
		}},

		"dashboard": {want: "dashboard", run: func(t *testing.T) (int, string) {
			root := aDashboardCockpit(t)
			server := aQuietGitlab(t)
			code, stdout, _ := runDashboardCommand(t, server,
				scriptedIssues{issues: aQueue()},
				"dashboard", "--json", "--cockpit="+root)

			return code, stdout
		}},

		"status": {want: "status", run: func(t *testing.T) (int, string) {
			root := aDashboardCockpit(t)
			code, stdout, _ := invoke(t, "status", "--json", "--cockpit="+root)

			return code, stdout
		}},

		"check": {want: "check", run: func(t *testing.T) (int, string) {
			root := anEnvironmentCockpit(t)
			engine := aCheckingEngine()
			engine.statusOK = true
			code, stdout, _ := runCheckCommand(t, engine, nil,
				"check", "pathauto", "--working-copy", "--json", "--cockpit="+root)

			return code, stdout
		}},

		"review": {want: "review", run: func(t *testing.T) (int, string) {
			root := anEnvironmentCockpit(t)
			engine := aCheckingEngine()
			engine.statusOK = true
			server, client := aReviewableGitlab(t)
			defer server.Close()
			code, stdout, _ := runCheckCommand(t, engine, client,
				"review", "pathauto", "12", "--json", "--cockpit="+root)

			return code, stdout
		}},

		"patch:check": {want: "check", run: func(t *testing.T) (int, string) {
			root := anEnvironmentCockpit(t)
			host, _ := aPatchHost(t, aPatchDiff)
			issues := scriptedIssues{issues: []drupal.Issue{anIssueWith(host.URL, "fix-1.patch")}}

			code, stdout, _ := runPatchCommand(t, aPatchingEngine(), issues,
				scriptedPrompt{}, "patch:check", "pathauto", "3597857",
				"--json", "--cockpit="+root)

			return code, stdout
		}},

		"patch:promote": {want: "patch-promote", run: func(t *testing.T) (int, string) {
			root := anEnvironmentCockpit(t)
			host, _ := aPatchHost(t, aPatchDiff)
			issues := scriptedIssues{issues: []drupal.Issue{anIssueWith(host.URL, "fix-1.patch")}}

			code, stdout, _ := runPatchCommand(t, aPatchingEngine(), issues,
				scriptedPrompt{}, "patch:promote", "pathauto", "3597857",
				"--json", "--cockpit="+root)

			return code, stdout
		}},

		"publish": {want: "publish", run: func(t *testing.T) (int, string) {
			root := anEnvironmentCockpit(t)
			scene := aForkScene(t)
			code, stdout, _ := runIssueLoop(t, aWorkingEngine(), someIssues(),
				scriptedClients{client: scene.client()}, cli.NoBrowser{},
				"publish", "--yes", "pathauto", "3223746", "--json", "--cockpit="+root)

			return code, stdout
		}},
	}
}

// stdout carries exactly one JSON document and nothing else.
//
// **The test this whole surface rests on.** Progress and diagnostics already
// go to stderr, so the split exists; what breaks it is one `cli.Println` added
// to a stdout path months from now, and nothing else would notice. Unmarshal
// is strict about trailing content, so a stray line fails here.
func TestJSONModeWritesOneDocumentAndNothingElse(t *testing.T) {
	for name, each := range jsonCases() {
		t.Run(name, func(t *testing.T) {
			_, stdout := each.run(t)

			if strings.TrimSpace(stdout) == "" {
				t.Fatal("stdout was empty: every run owes a document")
			}

			var envelope report.Envelope
			decoder := json.NewDecoder(strings.NewReader(stdout))
			if err := decoder.Decode(&envelope); err != nil {
				t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
			}
			if decoder.More() {
				t.Errorf("stdout carries more than one document:\n%s", stdout)
			}

			if envelope.Schema != report.Schema {
				t.Errorf("schema %d, want %d", envelope.Schema, report.Schema)
			}
			if envelope.Command != each.want {
				t.Errorf("command %q, want %q", envelope.Command, each.want)
			}
			if envelope.Upkeep == "" {
				t.Error("the envelope names no version")
			}
			if envelope.Warnings == nil {
				t.Error("warnings is null; it should always be an array")
			}
			if envelope.Data == nil {
				t.Error("the envelope carries no data")
			}
		})
	}
}

// Every command declaring the trait is exercised above.
//
// This is what stops the table going stale: adding --json to a tenth command
// without a case here fails, rather than leaving the one test that guards
// stdout purity silently not covering it.
func TestEveryJSONCommandIsExercised(t *testing.T) {
	covered := jsonCases()

	var missing []string
	for _, cmd := range documentedCommands(t) {
		traits, declared := cli.TraitsOf(cmd)
		if !declared || !traits.JSON {
			continue
		}
		if _, found := covered[cmd.Name()]; !found {
			missing = append(missing, cmd.Name())
		}
	}
	sort.Strings(missing)

	if len(missing) > 0 {
		t.Errorf("these declare --json but are not exercised in jsonCases: %v", missing)
	}

	// And nothing is listed that no longer offers the flag.
	byName := map[string]bool{}
	for _, cmd := range documentedCommands(t) {
		byName[cmd.Name()] = true
	}
	for name := range covered {
		if !byName[name] {
			t.Errorf("jsonCases covers %q, which is not a command", name)
		}
	}
}

// A failing check still exits 1 in JSON mode.
//
// The exit code is the contract an agent acts on without parsing anything, so
// a verdict that emitted its payload and returned success would be the worst
// kind of regression: silent, and wrong in the direction of "everything is
// fine".
func TestJSONModeKeepsTheExitCode(t *testing.T) {
	root := anEnvironmentCockpit(t)
	engine := aCheckingEngine()
	engine.statusOK = true
	one := 1
	engine.run = check.RunResult{Results: []check.Result{
		{Type: check.PhpCs, Status: check.Failed, ExitCode: &one, Output: "1 ERROR"},
	}}

	code, stdout, _ := runCheckCommand(t, engine, nil,
		"check", "pathauto", "--working-copy", "--json", "--cockpit="+root)

	if code != workflow.Failed {
		t.Errorf("exit %d, want %d", code, workflow.Failed)
	}

	var envelope struct {
		Data report.CheckPayload `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if envelope.Data.Passed {
		t.Error("the payload says it passed")
	}
	if len(envelope.Data.Checks) != 1 || envelope.Data.Checks[0].Status != string(check.Failed) {
		t.Errorf("checks %+v", envelope.Data.Checks)
	}
}

// A run that found nothing still emits a document.
//
// Placed before every early return for this reason: silence on stdout leaves a
// consumer unable to tell "no open issues" from "the command stopped earlier",
// and those want different reactions.
func TestJSONModeEmitsAnEmptyResultRatherThanSilence(t *testing.T) {
	root := aDashboardCockpit(t)
	server := aQuietGitlab(t)

	code, stdout, _ := runIssuesCommand(t,
		scriptedIssues{}, scriptedClients{client: gitlabClientFor(server)},
		"issues", "pathauto", "--json", "--cockpit="+root)

	if code != workflow.OK {
		t.Fatalf("exit %d", code)
	}

	var envelope struct {
		Data report.ContributionsPayload `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if envelope.Data.Issues == nil {
		t.Error("issues is null; an empty result is an empty array")
	}
	if len(envelope.Data.Issues) != 0 {
		t.Errorf("got %d issues", len(envelope.Data.Issues))
	}
}

// The declared trait and the flag agree, in both directions.
func TestTheJSONTraitMatchesTheFlag(t *testing.T) {
	for _, cmd := range documentedCommands(t) {
		traits, declared := cli.TraitsOf(cmd)
		if !declared {
			continue
		}

		hasFlag := cmd.Flags().Lookup("json") != nil
		if traits.JSON != hasFlag {
			t.Errorf("%s: declared JSON=%t but the --json flag is %s",
				cmd.Name(), traits.JSON,
				map[bool]string{true: "present", false: "absent"}[hasFlag])
		}
	}
}

// The rows tier emits the rows payload, not the overview.
//
// `--json` must answer the question the same arguments ask without it: naming
// a module means rows, and a flag that quietly returned the summary instead
// would be a different command wearing the same name.
func TestNamingAModuleEmitsTheRowsTier(t *testing.T) {
	root := aDashboardCockpit(t)
	server := aQuietGitlab(t)

	_, stdout, _ := runDashboardCommand(t, server,
		scriptedIssues{issues: aQueue()},
		"dashboard", "pathauto", "--json", "--cockpit="+root)

	var envelope struct {
		Command string             `json:"command"`
		Data    report.RowsPayload `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("payload: %v\n%s", err, stdout)
	}
	if envelope.Command != "dashboard-rows" {
		t.Errorf("command %q, want dashboard-rows", envelope.Command)
	}
	if envelope.Data.Rows == nil {
		t.Error("rows is null")
	}
}

// Checking a merge request names it as the subject, with the merge ref SHA the
// verdict is actually about.
func TestCheckingAMergeRequestNamesItAsTheSubject(t *testing.T) {
	root := anEnvironmentCockpit(t)
	engine := aCheckingEngine()
	engine.statusOK = true
	server, client := aReviewableGitlab(t)
	defer server.Close()

	_, stdout, stderr := runCheckCommand(t, engine, client,
		"check", "pathauto", "12", "--json", "--cockpit="+root)

	var envelope struct {
		Data report.CheckPayload `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("payload: %v (%s)", err, stderr)
	}
	if envelope.Data.Subject.Kind != "merge-request" {
		t.Errorf("kind %q", envelope.Data.Subject.Kind)
	}
	if envelope.Data.Subject.IID != 12 {
		t.Errorf("iid %d", envelope.Data.Subject.IID)
	}
}

// Re-running publish after more commits reports the merge request it updated,
// and says it did not create one.
//
// Both are success and they are not the same event: a consumer told only
// "success" cannot tell whether a merge request now exists that did not
// before.
func TestPublishingAnExistingMergeRequestSaysItWasNotCreated(t *testing.T) {
	root := anEnvironmentCockpit(t)
	scene := aForkScene(t)
	scene.openMR = `[{"iid":9,"state":"opened","source_project_id":2,` +
		`"web_url":"https://git.drupalcode.org/project/pathauto/-/merge_requests/9"}]`

	_, stdout, stderr := runIssueLoop(t, aWorkingEngine(), someIssues(),
		scriptedClients{client: scene.client()}, cli.NoBrowser{},
		"publish", "--yes", "pathauto", "3223746", "--json", "--cockpit="+root)

	var envelope struct {
		Data report.PublishPayload `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("payload: %v (%s)", err, stderr)
	}
	if envelope.Data.Created {
		t.Error("it reported creating a merge request that already existed")
	}
	if envelope.Data.MergeRequest == nil || envelope.Data.MergeRequest.IID != 9 {
		t.Errorf("merge request %+v", envelope.Data.MergeRequest)
	}
}

// A partial promotion exits 1 and says nothing was committed.
//
// Exit 1 because the patch did not apply, which is the supervised work
// failing; Committed false because half somebody's patch is not what they
// wrote, and committing it under their name is the misattribution the
// attribution rule exists to prevent.
func TestAPartialPromotionSaysNothingWasCommitted(t *testing.T) {
	root := anEnvironmentCockpit(t)
	host, _ := aPatchHost(t, aPatchDiff)
	issues := scriptedIssues{issues: []drupal.Issue{anIssueWith(host.URL, "fix-1.patch")}}

	engine := aPatchingEngine()
	engine.promotion = adapter.PartialPromotion([]string{"src/Ok.php"}, []string{"src/Stale.php"})

	code, stdout, stderr := runPatchCommand(t, engine, issues, scriptedPrompt{},
		"patch:promote", "pathauto", "3597857", "--json", "--cockpit="+root)

	if code != workflow.Failed {
		t.Errorf("exit %d, want %d", code, workflow.Failed)
	}

	var envelope struct {
		Data report.PromotePayload `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("payload: %v (%s)", err, stderr)
	}
	if envelope.Data.Committed {
		t.Error("a partial promotion claimed to have committed")
	}
	if len(envelope.Data.Rejects) != 1 || envelope.Data.Rejects[0] != "src/Stale.php" {
		t.Errorf("rejects %v", envelope.Data.Rejects)
	}
	if envelope.Data.SHA != "" {
		t.Errorf("it reported a SHA: %q", envelope.Data.SHA)
	}
}
