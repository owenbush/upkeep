package drupal

import "testing"

// The two statuses that are the maintainer's turn, and no others.
func TestOnlyReviewAndRtbcAreTheMaintainersTurn(t *testing.T) {
	for _, status := range allStatuses {
		mine := status.NeedsMaintainer()
		want := status == StatusNeedsReview || status == StatusRtbc
		if mine != want {
			t.Errorf("%s: NeedsMaintainer %v, want %v", status.ShortLabel(), mine, want)
		}
		// A closed issue is never anybody's turn.
		if mine && !status.IsOpen() {
			t.Errorf("%s is closed and still asks for a maintainer", status.ShortLabel())
		}
	}
}
