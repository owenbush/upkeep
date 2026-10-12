package drupal

import "testing"

func boolPtr(value bool) *bool { return &value }

// drupal.org states on the project node whether the project still keeps its
// issues there, and that is what upkeep reads to decide which queue to scan.
//
// Measured: false for `helper`, whose queue has moved to GitLab, and true for
// pathauto, token and jumplinks.
func TestTheProjectNodeSaysWhetherTheQueueIsStillHere(t *testing.T) {
	for _, tc := range []struct {
		name  string
		flag  *bool
		uses  bool
		known bool
	}{
		{"a project that has moved", boolPtr(false), false, true},
		{"a project that has not", boolPtr(true), true, true},
		{"a payload that does not say", nil, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := &fakeAPI{issueQueue: tc.flag}
			client := api.serve(t)

			uses, known := client.UsesDrupalIssueQueue("helper")

			if uses != tc.uses || known != tc.known {
				t.Errorf("got (%t, %t), want (%t, %t)", uses, known, tc.uses, tc.known)
			}
		})
	}
}

// Not knowing is not the same as having moved.
//
// The caller reads drupal.org when the answer is unknown, which is what every
// release so far has done; reading it as "moved" would scan an empty GitLab
// queue and report a module as having no open issues at all.
func TestAnUnreadableProjectNodeIsNotAMovedQueue(t *testing.T) {
	api := &fakeAPI{issueQueue: nil}
	client := api.serve(t)

	if _, known := client.UsesDrupalIssueQueue("helper"); known {
		t.Error("it claimed to know where the issues are")
	}
}

// The flag and the node id come from one request, because they come from one
// node — asking twice would double every scan's project lookup.
func TestTheQueueFlagCostsNoExtraRequest(t *testing.T) {
	api := &fakeAPI{issueQueue: boolPtr(false)}
	client := api.serve(t)

	if _, ok := client.ProjectNid("helper"); !ok {
		t.Fatal("no nid")
	}
	before := len(api.requests)

	if uses, known := client.UsesDrupalIssueQueue("helper"); uses || !known {
		t.Errorf("got (%t, %t)", uses, known)
	}

	if after := len(api.requests); after != before {
		t.Errorf("the flag cost %d extra request(s)", after-before)
	}
}

// And the other way round: the flag is read first, and the nid is still there.
func TestTheNidSurvivesAQueueFlagRead(t *testing.T) {
	api := &fakeAPI{issueQueue: boolPtr(true)}
	client := api.serve(t)

	client.UsesDrupalIssueQueue("helper")

	nid, ok := client.ProjectNid("helper")
	if !ok || nid != 3060 {
		t.Errorf("nid %d, ok %t", nid, ok)
	}
	if len(api.requests) != 1 {
		t.Errorf("%d requests for one node: %v", len(api.requests), api.requests)
	}
}
