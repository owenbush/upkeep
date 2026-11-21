package command

import (
	"strings"
	"testing"

	"github.com/owenbush/upkeep/internal/cli"
	"github.com/owenbush/upkeep/internal/workflow"
)

const mrLogsProject = `{"id":11,"path":"pathauto","path_with_namespace":"project/pathauto"}`

func runMrLogsAgainst(t *testing.T, answers map[string]string, args ...string) (int, string, string) {
	t.Helper()

	root := aDashboardCockpit(t)
	_, client := aGitlabStub(t, answers)

	return runIssueLoop(t, aWorkingEngine(), noIssues{}, scriptedClients{client: client}, cli.NoBrowser{},
		append(args, "--cockpit="+root)...)
}

// The pipeline of a merge request from an issue fork ran in the fork, and its
// jobs are only there: the target project answers 404 for the same pipeline,
// which is what broke drupalorg-cli's mr:logs. The stub answers only the fork's
// paths, so reading the target's would fail this test.
func TestMrLogsReadsThePipelineWhereItRan(t *testing.T) {
	code, stdout, stderr := runMrLogsAgainst(t, map[string]string{
		"/api/v4/projects/project%2Fpathauto": mrLogsProject,
		"/api/v4/projects/11/merge_requests/12": `{"iid":12,"source_project_id":501,"target_project_id":11,
			"head_pipeline":{"id":9,"project_id":501,"status":"success","web_url":"https://web.test/p/9"}}`,
		"/api/v4/projects/501/pipelines/9/jobs": `[
			{"id":6,"name":"composer","stage":"build","status":"success"},
			{"id":7,"name":"phpcs","stage":"validate","status":"failed","allow_failure":true,"web_url":"https://web.test/j/7"}]`,
		"/api/v4/projects/501/jobs/7/trace": "section_start:1:step\r\x1b[0K\x1b[31;1mFILE: x.php\x1b[0m\n" +
			"ERROR: Job failed: exit code 2\n\n",
	}, "mr:logs", "pathauto", "12")

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	for _, want := range []string{
		"pipeline #9 (success)",
		"=== phpcs (validate) — failed, allowed to fail — https://web.test/j/7",
		"\nFILE: x.php\nERROR: Job failed: exit code 2\n",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout is missing %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "composer") || strings.Contains(stdout, "\x1b") ||
		strings.Contains(stdout, "section_start") {
		t.Errorf("a passed job or terminal noise was printed:\n%q", stdout)
	}
}

// GitLab not saying where the pipeline ran falls back to where the branch is.
func TestMrLogsFallsBackToTheSourceProject(t *testing.T) {
	code, stdout, stderr := runMrLogsAgainst(t, map[string]string{
		"/api/v4/projects/project%2Fpathauto": mrLogsProject,
		"/api/v4/projects/11/merge_requests/12": `{"iid":12,"source_project_id":501,
			"head_pipeline":{"id":9,"status":"failed"}}`,
		"/api/v4/projects/501/pipelines/9/jobs": `[{"id":7,"name":"phpunit","stage":"test","status":"failed"}]`,
	}, "mr:logs", "pathauto", "12")

	if code != workflow.OK {
		t.Fatalf("exit %d (%s)", code, stderr)
	}
	// The trace path is not scripted, so the job is reported without it
	// rather than ending the run.
	if !strings.Contains(stdout, "=== phpunit (test) — failed —") ||
		!strings.Contains(stdout, "(trace unavailable:") {
		t.Errorf("stdout:\n%s", stdout)
	}
}

// Nothing to show is an answer, not a failure.
func TestMrLogsWithNothingToShow(t *testing.T) {
	for name, tc := range map[string]struct {
		mergeRequest, jobs, want string
	}{
		"no pipeline": {`{"iid":12,"source_project_id":11}`, "", "MR !12 has no pipeline.\n"},
		"no failed jobs": {
			// Nothing says where it ran, so it is the project itself.
			`{"iid":12,"head_pipeline":{"id":9,"status":"success"}}`,
			`[{"id":1,"name":"phpunit","status":"success"}]`,
			"Pipeline #9: no failed jobs.\n",
		},
	} {
		t.Run(name, func(t *testing.T) {
			answers := map[string]string{
				"/api/v4/projects/project%2Fpathauto":   mrLogsProject,
				"/api/v4/projects/11/merge_requests/12": tc.mergeRequest,
			}
			if tc.jobs != "" {
				answers["/api/v4/projects/11/pipelines/9/jobs"] = tc.jobs
			}

			code, stdout, stderr := runMrLogsAgainst(t, answers, "mr:logs", "pathauto", "12")

			if code != workflow.OK || !strings.HasSuffix(stdout, tc.want) {
				t.Errorf("exit %d, stdout %q (%s)", code, stdout, stderr)
			}
		})
	}
}

// A read that fails is upkeep failing to do the job.
func TestMrLogsReportsAnUnreadableMergeRequest(t *testing.T) {
	for _, answers := range []map[string]string{
		{},
		{"/api/v4/projects/project%2Fpathauto": mrLogsProject},
		{
			"/api/v4/projects/project%2Fpathauto":   mrLogsProject,
			"/api/v4/projects/11/merge_requests/12": `{"iid":12,"head_pipeline":{"id":9,"project_id":11}}`,
		},
	} {
		code, stdout, stderr := runMrLogsAgainst(t, answers, "mr:logs", "pathauto", "12")
		if code != workflow.Infrastructure || stdout != "" || !strings.Contains(stderr, "could not be read") {
			t.Errorf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
		}
	}
}

func TestTraceTailKeepsTheEnd(t *testing.T) {
	got := traceTail("a\nb\nc\nd\n\n\n", 2)
	if strings.Join(got, ",") != "c,d" {
		t.Errorf("got %q", got)
	}
}

// The arguments are refused before anything is read.
func TestMrLogsRefusesBadArgumentsFirst(t *testing.T) {
	_, client := aGitlabStub(t, map[string]string{})
	clients := scriptedClients{client: client}

	for args, want := range map[[3]string]string{
		{"pathauto", "0", "--cockpit=" + aDashboardCockpit(t)}: "positive integer",
		{"pathauto", "12", "--cockpit="}:                       "empty --cockpit",
	} {
		code, _, stderr := runIssueLoop(t, aWorkingEngine(), noIssues{}, clients, cli.NoBrowser{},
			"mr:logs", args[0], args[1], args[2])
		if code != workflow.Infrastructure || !strings.Contains(stderr, want) {
			t.Errorf("%v: exit %d (%s)", args, code, stderr)
		}
	}
}
