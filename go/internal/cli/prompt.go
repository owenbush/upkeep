package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// Prompt asks the operator a question and reads the answer.
//
// A seam rather than a direct read of standard input, so the one place a human
// approves something can be exercised — and so a command cannot acquire a
// second way of asking.
type Prompt interface {
	// Interactive reports whether an explicit answer can be had at all. False
	// means no terminal, and a command that requires approval must then do
	// nothing rather than take a default.
	Interactive() bool

	// Choose asks for one of the given answers and returns it. The first
	// answer is the default, taken on an empty reply — so it must always be
	// the harmless one.
	Choose(question string, answers []string) string

	// ChooseMany asks for any number of the given options and returns those
	// chosen, in the order they were offered.
	//
	// Separate from Choose because the defaults point opposite ways: Choose
	// takes its first answer when nobody replies, while an empty reply here
	// means *nothing* was chosen — a list of things to opt into must never
	// opt somebody into the first one by their saying nothing.
	ChooseMany(question string, options []string) []string
}

// TerminalPrompt reads answers from a stream.
type TerminalPrompt struct {
	in       *bufio.Reader
	out      io.Writer
	terminal bool
}

// NewTerminalPrompt builds one over a command's streams.
//
// Whether it is interactive is asked of standard input rather than assumed:
// the question is whether a human is there to answer, and a piped stdin means
// there is not.
func NewTerminalPrompt(cmd *cobra.Command) *TerminalPrompt {
	in := cmd.InOrStdin()

	return &TerminalPrompt{
		in:       bufio.NewReader(in),
		out:      cmd.ErrOrStderr(),
		terminal: isTerminalReader(in),
	}
}

// Interactive reports whether a human can answer.
func (p *TerminalPrompt) Interactive() bool { return p.terminal }

// Choose asks and reads one answer.
//
// The reply is matched case-insensitively against the whole answer, and
// anything unrecognised falls to the default. That direction is deliberate: a
// typo must never be read as the more destructive choice, and here the default
// is always the one that does nothing.
func (p *TerminalPrompt) Choose(question string, answers []string) string {
	if len(answers) == 0 {
		return ""
	}

	fmt.Fprintf(p.out, "%s [%s] (default %s): ", question, strings.Join(answers, "/"), answers[0])

	line, err := p.in.ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		// Standard input ended mid-question. Nobody answered, so the default
		// stands — which is the answer that does nothing.
		fmt.Fprintln(p.out)

		return answers[0]
	}

	reply := strings.ToLower(strings.TrimSpace(line))
	for _, answer := range answers {
		if reply == strings.ToLower(answer) {
			return answer
		}
	}

	return answers[0]
}

// ChooseMany asks and reads any number of answers.
//
// Answers are given by name or by the number shown beside them, separated by
// commas — a list of module names is tedious to type correctly and a typo that
// silently registers nothing is worse than one that is refused. Anything
// unrecognised is reported and skipped rather than guessed at: this writes to
// the registry, and a near-match chosen on the operator's behalf is an entry
// they did not ask for.
func (p *TerminalPrompt) ChooseMany(question string, options []string) []string {
	if len(options) == 0 {
		return nil
	}

	fmt.Fprintln(p.out, question)
	for i, option := range options {
		fmt.Fprintf(p.out, "  %d) %s\n", i+1, option)
	}
	fmt.Fprint(p.out, "Answer (comma-separated, blank for none): ")

	line, err := p.in.ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		fmt.Fprintln(p.out)

		return nil
	}

	chosen := map[string]bool{}
	for _, reply := range strings.Split(line, ",") {
		reply = strings.TrimSpace(reply)
		if reply == "" {
			continue
		}
		if option, matched := matchOption(reply, options); matched {
			chosen[option] = true

			continue
		}
		fmt.Fprintf(p.out, "  (no option %q — skipped)\n", reply)
	}

	// In the order they were offered, so the run reads the same way the list
	// did however the answer was typed.
	picked := make([]string, 0, len(chosen))
	for _, option := range options {
		if chosen[option] {
			picked = append(picked, option)
		}
	}

	return picked
}

// matchOption resolves one reply to an option, by its number or its name.
func matchOption(reply string, options []string) (string, bool) {
	if at, err := strconv.Atoi(reply); err == nil {
		if at >= 1 && at <= len(options) {
			return options[at-1], true
		}

		return "", false
	}

	for _, option := range options {
		if strings.EqualFold(reply, option) {
			return option, true
		}
	}

	return "", false
}

// isTerminalReader reports whether a stream is a terminal a human is typing
// at.
func isTerminalReader(stream io.Reader) bool {
	file, isFile := stream.(*os.File)
	if !isFile {
		return false
	}

	info, err := file.Stat()

	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
