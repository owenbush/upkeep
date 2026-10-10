package cli

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/owenbush/upkeep/internal/cockpit"
	"github.com/owenbush/upkeep/internal/workflow"
)

// aCommand is a command wired the way every real one is, with its streams
// captured.
func aCommand(perform Perform) (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd := &cobra.Command{Use: "thing", RunE: Run(perform)}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs(nil)

	return cmd, out, errOut
}

// The contract: three codes, mapped once. A command returns one; a domain
// failure becomes 2 with the failure's own words.
func TestTheExitCodeContractIsMappedInOnePlace(t *testing.T) {
	for name, scripted := range map[string]struct {
		code int
		err  error
		want int
	}{
		"it did what was asked":       {code: workflow.OK, want: workflow.OK},
		"the supervised work failed":  {code: workflow.Failed, want: workflow.Failed},
		"upkeep could not do the job": {err: errors.New("no cockpit"), want: workflow.Infrastructure},
	} {
		cmd, _, errOut := aCommand(func(*cobra.Command, []string) (int, error) {
			return scripted.code, scripted.err
		})

		if got := CodeOf(cmd.Execute()); got != scripted.want {
			t.Errorf("%s: exit %d, want %d", name, got, scripted.want)
		}
		if scripted.err != nil && !strings.Contains(errOut.String(), "no cockpit") {
			t.Errorf("%s: the failure's own words were dropped: %q", name, errOut)
		}
	}
}

// A failure goes to stderr and never to the command's own output: a command
// whose stdout is a path to capture or a table to pipe must stay pipeable even
// while it is complaining.
func TestAFailureNeverLandsInTheCommandsOwnOutput(t *testing.T) {
	cmd, out, errOut := aCommand(func(*cobra.Command, []string) (int, error) {
		return 0, errors.New("something went wrong")
	})
	_ = cmd.Execute()

	if out.Len() != 0 {
		t.Errorf("stdout carried a diagnostic: %q", out)
	}
	if !strings.Contains(errOut.String(), "something went wrong") {
		t.Errorf("stderr: %q", errOut)
	}
	// And a domain failure keeps its synopsis out of the way: the command has
	// already said something more useful than its own flag list.
	if strings.Contains(out.String()+errOut.String(), "Usage:") {
		t.Errorf("the refusal was buried under usage:\n%s%s", out, errOut)
	}
}

// Cobra's own refusals — an unknown flag, a missing argument — are usage
// mistakes, which the contract calls an infrastructure failure.
func TestAUsageMistakeIsAnInfrastructureFailure(t *testing.T) {
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	root := &cobra.Command{Use: "upkeep"}
	root.AddCommand(&cobra.Command{
		Use:  "thing",
		Args: cobra.NoArgs,
		RunE: Run(func(*cobra.Command, []string) (int, error) { return workflow.OK, nil }),
	})
	root.SetOut(out)
	root.SetErr(stderr)
	root.SetArgs([]string{"thing", "--nonsense"})

	if code := Execute(root, stderr); code != workflow.Infrastructure {
		t.Errorf("exit %d", code)
	}
	if !strings.Contains(stderr.String(), "nonsense") {
		t.Errorf("the mistake was not named: %q", stderr)
	}
	// Pointed at --help rather than given forty lines of synopsis, and on
	// stderr either way: cobra's default puts the usage block on stdout, which
	// would land inside `cd $(upkeep env:path …)`.
	if !strings.Contains(stderr.String(), "--help") {
		t.Errorf("the mistake was not pointed anywhere:\n%s", stderr)
	}
	if strings.Contains(out.String(), "Usage:") {
		t.Errorf("a usage block landed in stdout:\n%s", out)
	}
}

// A success prints nothing extra and exits 0.
func TestASuccessIsSilentAndZero(t *testing.T) {
	stderr := &bytes.Buffer{}
	root := &cobra.Command{Use: "upkeep"}
	root.AddCommand(&cobra.Command{
		Use:  "thing",
		RunE: Run(func(*cobra.Command, []string) (int, error) { return workflow.OK, nil }),
	})
	root.SetOut(&bytes.Buffer{})
	root.SetErr(stderr)
	root.SetArgs([]string{"thing"})

	if code := Execute(root, stderr); code != workflow.OK {
		t.Errorf("exit %d", code)
	}
	if stderr.Len() != 0 {
		t.Errorf("a successful command wrote to stderr: %q", stderr)
	}
}

// The one merge-request-IID rule: a positive integer. `!0` is not a merge
// request, so it is refused here rather than turned into a confusing 404.
func TestTheMergeRequestIidRule(t *testing.T) {
	for _, raw := range []string{"1", "12", "3601234"} {
		if _, err := MrIID(raw); err != nil {
			t.Errorf("%q was refused: %v", raw, err)
		}
	}

	for _, raw := range []string{"0", "-1", "+1", "", "abc", "1.5", "12x", " 12"} {
		if iid, err := MrIID(raw); err == nil {
			t.Errorf("%q was accepted as !%d", raw, iid)
		}
	}

	_, err := MrIID("0")
	if err == nil || !strings.Contains(err.Error(), "positive integer") {
		t.Errorf("the refusal does not say the rule: %v", err)
	}
}

// `--cockpit=` is a mistake worth naming, so an empty value given explicitly
// is passed through rather than collapsed into "use the default".
func TestAnExplicitlyEmptyCockpitIsAMistakeRatherThanADefault(t *testing.T) {
	cmd := &cobra.Command{Use: "thing"}
	AddCockpit(cmd)
	cmd.SetArgs([]string{"--cockpit="})
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		if _, err := Cockpit(cmd); err == nil {
			t.Error("an empty --cockpit resolved to something")
		}

		return nil
	}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
}

// Omitted, it falls back the documented way.
func TestAnOmittedCockpitFallsBackToTheEnvironmentThenTheDirectory(t *testing.T) {
	t.Setenv(cockpit.EnvVar, t.TempDir())

	cmd := &cobra.Command{Use: "thing"}
	AddCockpit(cmd)
	cmd.SetArgs(nil)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		where, err := Cockpit(cmd)
		if err != nil {
			t.Fatalf("cockpit: %v", err)
		}
		if where.Root != os.Getenv(cockpit.EnvVar) {
			t.Errorf("root %q", where.Root)
		}

		return nil
	}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
}

// The scan-warning report states the shortfall and names the counts as the
// lower bounds they are: less data is indistinguishable at the call site from
// there being less data.
func TestScanWarningsStateTheShortfallAndCapTheList(t *testing.T) {
	cmd, out, errOut := aCommand(func(*cobra.Command, []string) (int, error) { return 0, nil })

	many := make([]string, 9)
	for i := range many {
		many[i] = "request " + string(rune('a'+i)) + " did not answer"
	}
	ReportScanWarnings(cmd, many)

	report := errOut.String()
	if !strings.Contains(report, "9 drupal.org request(s) did not answer") {
		t.Errorf("the count is missing:\n%s", report)
	}
	if !strings.Contains(report, "... and 4 more.") {
		t.Errorf("a long list was not capped:\n%s", report)
	}
	if !strings.Contains(report, "lower bounds") {
		t.Errorf("the counts were not named as lower bounds:\n%s", report)
	}
	// Diagnostics, so never in the output being piped.
	if out.Len() != 0 {
		t.Errorf("warnings landed in stdout: %q", out)
	}

	// Nothing to report is silent.
	errOut.Reset()
	ReportScanWarnings(cmd, nil)
	if errOut.Len() != 0 {
		t.Errorf("a clean scan still warned: %q", errOut)
	}
}
