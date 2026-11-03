package proc

import (
	"context"
	"strings"
	"testing"

	"github.com/owenbush/upkeep/internal/security"
)

// The one way a child is handed a credential: named, in its own variable,
// with the inherited one still stripped and the value redacted from every
// stream that comes back.
func TestExecHandsOverOnlyTheNamedCredentialAndRedactsIt(t *testing.T) {
	t.Setenv(security.CredentialVars[0], "glpat-inherited-should-not-pass")
	const secret = "glpat-handed-over-on-purpose"

	out := Exec(context.Background(),
		sh(`echo "out $CHILD_TOKEN"; echo "err $CHILD_TOKEN ${`+security.CredentialVars[0]+`:-ABSENT}" >&2; exit 3`),
		map[string]string{"CHILD_TOKEN": secret},
	)

	if out.ExitCode == nil || *out.ExitCode != 3 {
		t.Fatalf("exit code = %v, want 3", out.ExitCode)
	}
	if got := strings.TrimSpace(string(out.Stdout)); got != "out "+security.Mask {
		t.Errorf("stdout = %q", got)
	}
	if got := strings.TrimSpace(string(out.Stderr)); got != "err "+security.Mask+" ABSENT" {
		t.Errorf("stderr = %q: the inherited credential must stay stripped", got)
	}
}

func TestExecReportsAChildThatNeverRanOrWasCancelled(t *testing.T) {
	never := Exec(context.Background(), []string{"/nonexistent/binary"}, nil)
	if never.Err == nil || never.ExitCode != nil {
		t.Errorf("never ran: %+v", never)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cancelled := Exec(ctx, sh("sleep 5"), nil)
	if cancelled.Err == nil || cancelled.ExitCode != nil {
		t.Errorf("cancelled: %+v", cancelled)
	}

	if empty := Exec(context.Background(), nil, nil); empty.Err == nil {
		t.Error("an empty command ran")
	}
}
