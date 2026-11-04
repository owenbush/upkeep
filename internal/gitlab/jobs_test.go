package gitlab

import (
	"strings"
	"testing"
)

// A merge request's pipeline runs where its branch is — on drupal.org that is
// the issue fork, not the project the merge request targets — and GitLab says
// so in the pipeline's project_id. Asking the target project for the jobs is
// the 404 drupalorg-cli's mr:logs gets.
func TestThePipelineSaysWhichProjectItRanIn(t *testing.T) {
	client, _ := clientWith("", ok(`{"iid":22,"source_project_id":250962,"target_project_id":55211,
		"head_pipeline":{"id":1013844,"project_id":250962,"status":"success"}}`))

	pipeline, failure := client.HeadPipeline(pathauto(t), 22)
	if failure != nil {
		t.Fatalf("unexpected failure: %v", failure)
	}
	if pipeline.ProjectID != 250962 {
		t.Errorf("pipeline project %d, want the fork's 250962", pipeline.ProjectID)
	}
}

func TestPipelineJobsAreReadFromTheProjectNamedAndPaginated(t *testing.T) {
	full := "[" + strings.TrimSuffix(strings.Repeat(`{"id":1,"name":"phpcs","status":"success"},`, 100), ",") + "]"
	client, transport := clientWith("",
		ok(full),
		ok(`[{"id":7,"name":"phpunit","stage":"test","status":"failed","allow_failure":true,
			"web_url":"https://web.test/issue/x/-/jobs/7"}]`),
	)

	jobs, failure := client.PipelineJobs(Pipeline{ID: 1013844, ProjectID: 250962})
	if failure != nil {
		t.Fatalf("unexpected failure: %v", failure)
	}
	if len(jobs) != 101 {
		t.Fatalf("got %d jobs, want 101", len(jobs))
	}
	last := jobs[100]
	if last.ID != 7 || last.Name != "phpunit" || last.Stage != "test" || !last.Failed() ||
		!last.AllowFailure || last.WebURL != "https://web.test/issue/x/-/jobs/7" {
		t.Errorf("job %+v", last)
	}
	if jobs[0].Failed() {
		t.Error("a passed job reads as failed")
	}
	urls := transport.urls()
	if len(urls) != 2 || !strings.Contains(urls[0], "/projects/250962/pipelines/1013844/jobs?") ||
		!strings.Contains(urls[1], "page=2") {
		t.Errorf("requests %v", urls)
	}
}

// A trace is plain text, not JSON, and is returned as GitLab sent it.
func TestAJobTraceIsReadAsText(t *testing.T) {
	client, transport := clientWith("", reply{status: 200, body: "line one\nline two\n"})

	trace, failure := client.JobTrace(250962, Job{ID: 7})
	if failure != nil {
		t.Fatalf("unexpected failure: %v", failure)
	}
	if trace != "line one\nline two\n" {
		t.Errorf("trace %q", trace)
	}
	if urls := transport.urls(); len(urls) != 1 || !strings.HasSuffix(urls[0], "/projects/250962/jobs/7/trace") {
		t.Errorf("requests %v", urls)
	}
}

func TestAJobTraceFailureIsTyped(t *testing.T) {
	client, _ := clientWith("", reply{status: 404, body: `{"message":"404 Not found"}`})

	if _, failure := client.JobTrace(1, Job{ID: 2}); failure == nil || failure.Kind != NotFound {
		t.Errorf("failure %+v, want NotFound", failure)
	}

	broken, _ := clientWith("")
	if _, failure := broken.JobTrace(1, Job{ID: 2}); failure == nil || failure.Kind != TransportError {
		t.Errorf("failure %+v, want TransportError", failure)
	}
}
