package drupalorgcli

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// recordedHelp is `drupalorg help <command>` as drupalorg-cli 0.10.3 printed
// it, captured live.
func recordedHelp(t *testing.T, name string) []byte {
	t.Helper()

	out, err := os.ReadFile(filepath.Join("testdata", "help", name+".txt"))
	if err != nil {
		t.Fatal(err)
	}

	return out
}

// Every positional shape Symfony prints is read, from the recorded help of
// commands upkeep actually wraps: required, optional, nested optional, and
// variadic both ways.
func TestHelpIsParsedIntoASignature(t *testing.T) {
	for _, tc := range []struct {
		file string
		want Signature
	}{
		{"issue_fork", Signature{Command: "issue:fork", Args: []Arg{{"ref", Required}}}},
		{"issue_assign", Signature{Command: "issue:assign",
			Args: []Arg{{"ref", Required}, {"users", Variadic}}}},
		{"issue_relabel", Signature{Command: "issue:relabel",
			Args: []Arg{{"ref", Required}, {"labels", RequiredVariadic}}}},
		{"issue_checkout", Signature{Command: "issue:checkout",
			Args: []Arg{{"nid", Optional}, {"branch", Optional}}}},
		{"issue_setup-remote", Signature{Command: "issue:setup-remote", Args: []Arg{{"nid", Optional}}}},
	} {
		got, err := ParseHelp(recordedHelp(t, tc.file))
		if err != nil {
			t.Fatalf("%s: %v", tc.file, err)
		}
		if got.Command != tc.want.Command || !reflect.DeepEqual(got.Args, tc.want.Args) {
			t.Errorf("%s: got %s %v, want %s %v", tc.file, got.Command, got.Args, tc.want.Command, tc.want.Args)
		}
		// A mismatch is reported in the CLI's own notation, so it can be
		// found in its help.
		if last := got.Args[len(got.Args)-1].String(); !strings.Contains(string(recordedHelp(t, tc.file)), last) {
			t.Errorf("%s: %s is not how the help writes it", tc.file, last)
		}
		for _, option := range []string{"no-interaction", "help", "ansi", "no-ansi", "verbose"} {
			if !got.Accepts(option) {
				t.Errorf("%s: --%s not read from %v", tc.file, option, got.Options)
			}
		}
	}
}

func TestHelpWithoutAUsageIsAnError(t *testing.T) {
	for _, text := range []string{"", "Description:\n  nothing\n", "Usage:\n\n"} {
		if _, err := ParseHelp([]byte(text)); err == nil {
			t.Errorf("%q parsed", text)
		}
	}
}

// What a contract check is for: each difference is named with the command
// and the argument, so the line that reports it says what to fix.
func TestEveryDifferenceIsNamed(t *testing.T) {
	got, err := ParseHelp(recordedHelp(t, "issue_relabel"))
	if err != nil {
		t.Fatal(err)
	}

	if diffs := Compare(Signature{Command: "issue:relabel",
		Args:    []Arg{{"ref", Required}, {"labels", RequiredVariadic}},
		Options: []string{"no-interaction"}}, got); diffs != nil {
		t.Errorf("a matching expectation reported %v", diffs)
	}

	for _, tc := range []struct {
		name string
		want Signature
		says []string
	}{
		{"shape", Signature{Command: "issue:relabel", Args: []Arg{{"ref", Required}, {"labels", Variadic}}},
			[]string{"issue:relabel", `argument 2 "labels"`, "[<labels>...]", "<labels>..."}},
		{"name", Signature{Command: "issue:relabel", Args: []Arg{{"ref", Required}, {"tags", RequiredVariadic}}},
			[]string{"issue:relabel", "argument 2", "<tags>...", "<labels>..."}},
		{"one the CLI lacks", Signature{Command: "issue:relabel",
			Args: []Arg{{"ref", Required}, {"labels", RequiredVariadic}, {"why", Required}}},
			[]string{"issue:relabel", `argument 3 "why"`, "<why>", "no such argument"}},
		{"one upkeep lacks", Signature{Command: "issue:relabel", Args: []Arg{{"ref", Required}}},
			[]string{"issue:relabel", `argument 2 "labels"`, "upkeep does not pass"}},
		{"option", Signature{Command: "issue:relabel",
			Args:    []Arg{{"ref", Required}, {"labels", RequiredVariadic}},
			Options: []string{"dry-run"}},
			[]string{"issue:relabel", "--dry-run", "not accepted"}},
	} {
		diffs := Compare(tc.want, got)
		if len(diffs) != 1 {
			t.Errorf("%s: %d differences, want 1: %v", tc.name, len(diffs), diffs)

			continue
		}
		for _, s := range tc.says {
			if !strings.Contains(diffs[0], s) {
				t.Errorf("%s: %q does not say %q", tc.name, diffs[0], s)
			}
		}
	}
}

// The live half: help is run through the Runner, and a command the CLI does
// not define is a difference, not a crash.
func TestCheckRunsHelpAndReportsAMissingCommand(t *testing.T) {
	fixtures, _ := filepath.Abs(filepath.Join("testdata", "help"))
	stub(t, "0.10.3", `[ "$1" = help ] || exit 9
f="`+fixtures+`/$(echo "$2" | tr : _).txt"
[ -f "$f" ] && exec cat "$f"
cat "`+fixtures+`/not-defined.txt"; exit 1`)
	runner := New(nil, 0)

	ok := Signature{Command: "issue:fork", Args: []Arg{{"ref", Required}}, Options: []string{"no-interaction"}}
	if diffs := Check(context.Background(), runner, ok); diffs != nil {
		t.Errorf("issue:fork: %v", diffs)
	}

	diffs := Check(context.Background(), runner, Signature{Command: "issue:nope"})
	if len(diffs) != 1 || !strings.Contains(diffs[0], "issue:nope") || !strings.Contains(diffs[0], "not defined") {
		t.Errorf("missing command reported as %v", diffs)
	}

	stub(t, "0.10.3", "echo 'no usage here'")
	if diffs := Check(context.Background(), New(nil, 0), ok); len(diffs) != 1 ||
		!strings.Contains(diffs[0], "Usage") {
		t.Errorf("unparsable help reported as %v", diffs)
	}
}
