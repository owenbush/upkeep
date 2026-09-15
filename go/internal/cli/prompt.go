package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
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
