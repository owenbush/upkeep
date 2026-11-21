package drupalorgcli

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// The contract upkeep holds drupalorg-cli to, now that it runs the CLI only
// to act and relays its text: the commands it wraps exist, take
// --no-interaction, and take the positional arguments upkeep passes in the
// order and shape it passes them. A rename or a reshaped argument in a CLI
// release would otherwise surface as an action failing on somebody's issue.
//
// Read from `drupalorg help <command>`, which posts nothing, never from
// running the command.

// ArgShape is how a positional argument may be given, in Symfony's terms.
type ArgShape int

const (
	// Required is <name>.
	Required ArgShape = iota
	// Optional is [<name>].
	Optional
	// Variadic is [<name>...]: none or more.
	Variadic
	// RequiredVariadic is <name>...: one or more.
	RequiredVariadic
)

// Arg is one positional argument.
type Arg struct {
	Name  string
	Shape ArgShape
}

// String is the argument as Symfony's usage line prints it.
func (a Arg) String() string {
	switch a.Shape {
	case Optional:
		return "[<" + a.Name + ">]"
	case Variadic:
		return "[<" + a.Name + ">...]"
	case RequiredVariadic:
		return "<" + a.Name + ">..."
	default:
		return "<" + a.Name + ">"
	}
}

// Signature is a command's name, its positional arguments in order, and the
// long names of the options it accepts (or, for an expectation, the ones
// upkeep passes).
type Signature struct {
	Command string
	Args    []Arg
	Options []string
}

// Accepts reports whether the command takes --option.
func (s Signature) Accepts(option string) bool { return slices.Contains(s.Options, option) }

var (
	usageArg   = regexp.MustCompile(`<([^>]+)>(\.\.\.)?`)
	longOption = regexp.MustCompile(`--([a-z][a-z0-9-]*)`)
	optionGap  = regexp.MustCompile(`\s{2,}`)
)

// ParseHelp reads the first Usage line and the Options section of
// `drupalorg help <command>` output.
func ParseHelp(text []byte) (Signature, error) {
	var (
		sig     Signature
		section string
	)
	for line := range strings.Lines(string(text)) {
		line = strings.TrimRight(line, "\r\n")
		if !strings.HasPrefix(line, " ") && strings.HasSuffix(line, ":") {
			section = strings.TrimSuffix(line, ":")

			continue
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		switch {
		case section == "Usage" && sig.Command == "":
			sig.Command, sig.Args = parseUsage(trimmed)
		case section == "Options":
			// The first column is the option's names; the rest is prose,
			// which can mention other options.
			names := optionGap.Split(trimmed, 2)[0]
			for _, m := range longOption.FindAllStringSubmatch(names, -1) {
				sig.Options = append(sig.Options, m[1])
			}
		}
	}
	if sig.Command == "" {
		return sig, errors.New("help has no Usage line")
	}

	return sig, nil
}

// parseUsage reads `name [options] [--] <a> [<b> [<c>]] [<d>...]`: an
// argument inside any bracket is optional, and a trailing ... is variadic.
func parseUsage(line string) (string, []Arg) {
	name, rest, _ := strings.Cut(line, " ")
	var args []Arg
	for _, at := range usageArg.FindAllStringSubmatchIndex(rest, -1) {
		depth := strings.Count(rest[:at[0]], "[") - strings.Count(rest[:at[0]], "]")
		optional, variadic := depth > 0, at[4] >= 0
		shape := Required
		switch {
		case optional && variadic:
			shape = Variadic
		case variadic:
			shape = RequiredVariadic
		case optional:
			shape = Optional
		}
		args = append(args, Arg{Name: rest[at[2]:at[3]], Shape: shape})
	}

	return name, args
}

// Compare lists every way got (what the CLI says) differs from want (what
// upkeep relies on), each naming the command and the argument or option;
// nil when they agree.
func Compare(want, got Signature) []string {
	var diffs []string
	say := func(format string, args ...any) {
		diffs = append(diffs, want.Command+": "+fmt.Sprintf(format, args...))
	}
	for _, option := range want.Options {
		if !got.Accepts(option) {
			say("option --%s not accepted", option)
		}
	}
	for i := range max(len(want.Args), len(got.Args)) {
		switch {
		case i >= len(got.Args):
			say("argument %d %q: upkeep passes %s, the CLI has no such argument", i+1, want.Args[i].Name, want.Args[i])
		case i >= len(want.Args):
			say("argument %d %q: the CLI takes %s, upkeep does not pass it", i+1, got.Args[i].Name, got.Args[i])
		case want.Args[i] != got.Args[i]:
			say("argument %d %q: upkeep passes %s, the CLI takes %s", i+1, want.Args[i].Name, want.Args[i], got.Args[i])
		}
	}

	return diffs
}

// Check runs `drupalorg help <command>` and compares what it says with want.
// A command the CLI does not define, or help it cannot read, is one
// difference saying so.
func Check(ctx context.Context, r *Runner, want Signature) []string {
	out, failure := r.RunRaw(ctx, []string{"help", want.Command})
	if failure != nil {
		return []string{want.Command + ": " + failure.Message}
	}
	got, err := ParseHelp(out)
	if err != nil {
		return []string{want.Command + ": " + err.Error()}
	}

	return Compare(want, got)
}
