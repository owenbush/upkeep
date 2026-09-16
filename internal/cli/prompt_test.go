package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// aPrompt reads from a scripted reply.
func aPrompt(reply string) (*TerminalPrompt, *bytes.Buffer) {
	asked := &bytes.Buffer{}
	cmd := &cobra.Command{Use: "thing"}
	cmd.SetIn(strings.NewReader(reply))
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(asked)

	return NewTerminalPrompt(cmd), asked
}

// The first answer is the default, and it is taken on anything that is not an
// explicit match.
//
// The direction is the whole point: a typo must never be read as the more
// destructive choice. Where this is used, the first answer is always the one
// that does nothing.
func TestAnythingButAnExplicitAnswerTakesTheDefault(t *testing.T) {
	answers := []string{"skip", "merge", "quit"}

	for name, reply := range map[string]string{
		"an empty line":         "\n",
		"whitespace":            "   \n",
		"an unknown word":       "yes\n",
		"a near miss":           "merg\n",
		"something longer":      "merge it please\n",
		"end of input":          "",
		"input with no newline": "merge",
	} {
		prompt, _ := aPrompt(reply)

		got := prompt.Choose("Fast-lane action", answers)
		if name == "input with no newline" {
			// A reply that arrived without its newline is still a reply: a
			// pipe that ends after the word is somebody answering.
			if got != "merge" {
				t.Errorf("%s gave %q", name, got)
			}

			continue
		}
		if got != "skip" {
			t.Errorf("%s gave %q, want the default", name, got)
		}
	}
}

// An explicit answer is taken, and case is not part of it: nobody types a
// prompt answer in capitals on purpose.
func TestAnExplicitAnswerIsTaken(t *testing.T) {
	for _, reply := range []string{"merge\n", "MERGE\n", "  merge  \n", "Merge\n"} {
		prompt, _ := aPrompt(reply)

		if got := prompt.Choose("Fast-lane action", []string{"skip", "merge", "quit"}); got != "merge" {
			t.Errorf("%q gave %q", reply, got)
		}
	}
}

// The question says what the answers are and which one is assumed, because a
// prompt that hides its default is a prompt somebody answers by accident.
func TestThePromptSaysItsAnswersAndItsDefault(t *testing.T) {
	prompt, asked := aPrompt("\n")

	prompt.Choose("Fast-lane action for pathauto !12", []string{"skip", "merge", "quit"})

	question := asked.String()
	for _, expected := range []string{"pathauto !12", "skip/merge/quit", "default skip"} {
		if !strings.Contains(question, expected) {
			t.Errorf("the prompt does not say %q: %q", expected, question)
		}
	}
}

// Asking with nothing to choose from returns nothing rather than reaching past
// the end of the answers.
func TestAPromptWithNoAnswersAsksNothing(t *testing.T) {
	prompt, asked := aPrompt("merge\n")

	if got := prompt.Choose("Nothing to decide", nil); got != "" {
		t.Errorf("got %q", got)
	}
	if asked.Len() != 0 {
		t.Errorf("it asked anyway: %q", asked)
	}
}

// Whether a human is there to answer is asked of standard input, not assumed:
// a piped stdin means there is not, and a command that requires approval must
// then do nothing rather than take a default.
func TestInteractivityIsAskedOfStandardInput(t *testing.T) {
	prompt, _ := aPrompt("merge\n")
	if prompt.Interactive() {
		t.Error("a scripted reader reported itself as a terminal")
	}

	file, err := os.CreateTemp(t.TempDir(), "piped")
	if err != nil {
		t.Fatalf("temp: %v", err)
	}
	defer file.Close()

	cmd := &cobra.Command{Use: "thing"}
	cmd.SetIn(file)
	cmd.SetErr(&bytes.Buffer{})
	if NewTerminalPrompt(cmd).Interactive() {
		t.Error("a redirected file reported itself as a terminal")
	}
}

// Several questions in a row each read their own answer, so a run that walks a
// list does not consume one reply for two rows.
func TestEachQuestionReadsItsOwnAnswer(t *testing.T) {
	prompt, _ := aPrompt("merge\nskip\nquit\n")
	answers := []string{"skip", "merge", "quit"}

	got := []string{
		prompt.Choose("one", answers),
		prompt.Choose("two", answers),
		prompt.Choose("three", answers),
	}

	want := []string{"merge", "skip", "quit"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("question %d answered %q, want %q", i+1, got[i], want[i])
		}
	}
}

// Input ending part-way through a list leaves the rest on the default, rather
// than repeating the last answer.
func TestInputEndingMidListFallsToTheDefault(t *testing.T) {
	prompt, _ := aPrompt("merge\n")
	answers := []string{"skip", "merge", "quit"}

	if got := prompt.Choose("one", answers); got != "merge" {
		t.Errorf("first answer %q", got)
	}
	if got := prompt.Choose("two", answers); got != "skip" {
		t.Errorf("after input ended, answer %q — want the default", got)
	}
}

// A multi-select answer takes several, by name or by the number shown.
//
// Numbers because a list of Drupal machine names is tedious to type correctly,
// and names because a number is meaningless once the list has scrolled away.
func TestChooseManyTakesNamesAndNumbers(t *testing.T) {
	options := []string{"field_helper", "pathauto", "token"}

	for name, run := range map[string]struct {
		reply string
		want  string
	}{
		"by name":               {"token\n", "token"},
		"by number":             {"2\n", "pathauto"},
		"several":               {"token, field_helper\n", "field_helper,token"},
		"mixed":                 {"3,1\n", "field_helper,token"},
		"untidy spacing":        {"  token ,, pathauto  \n", "pathauto,token"},
		"different case":        {"TOKEN\n", "token"},
		"the same one twice":    {"token,3\n", "token"},
		"nothing":               {"\n", ""},
		"end of input":          {"", ""},
		"input with no newline": {"token", "token"},
	} {
		prompt, _ := aPrompt(run.reply)

		chosen := prompt.ChooseMany("Which?", options)

		if strings.Join(chosen, ",") != run.want {
			t.Errorf("%s: chose %v, want %q", name, chosen, run.want)
		}
	}
}

// Whatever order the answer is typed in, the result is the order the options
// were offered — so the run reads the same way the list did.
func TestChooseManyAnswersInTheOrderOffered(t *testing.T) {
	prompt, _ := aPrompt("token,field_helper\n")

	chosen := prompt.ChooseMany("Which?", []string{"field_helper", "pathauto", "token"})

	if strings.Join(chosen, ",") != "field_helper,token" {
		t.Errorf("chose %v", chosen)
	}
}

// An answer that matches nothing is reported and skipped, never guessed at:
// this writes to a registry, and a near-match chosen on somebody's behalf is
// an entry they did not ask for.
func TestChooseManySkipsWhatItCannotMatch(t *testing.T) {
	options := []string{"field_helper", "token"}

	for name, reply := range map[string]string{
		"a word that is not an option": "tokens,token\n",
		// A strict prefix of a real option. Prefix matching would be a
		// convenience here and a hazard: "to" would silently pick whichever
		// module happened to sort first.
		"a prefix of an option": "fie,token\n",
		"a number past the end": "9,token\n",
		"zero":                  "0,token\n",
		"a negative number":     "-1,token\n",
	} {
		prompt, asked := aPrompt(reply)

		chosen := prompt.ChooseMany("Which?", options)

		if strings.Join(chosen, ",") != "token" {
			t.Errorf("%s: chose %v", name, chosen)
		}
		if !strings.Contains(asked.String(), "skipped") {
			t.Errorf("%s: it was skipped silently: %q", name, asked)
		}
	}
}

// The options are shown, numbered, before the question is answerable — a
// prompt that asked for names without listing them would be a guessing game.
func TestChooseManyShowsWhatThereIsToChoose(t *testing.T) {
	prompt, asked := aPrompt("\n")

	prompt.ChooseMany("Which modules?", []string{"field_helper", "token"})

	shown := asked.String()
	for _, expected := range []string{"Which modules?", "1) field_helper", "2) token", "blank for none"} {
		if !strings.Contains(shown, expected) {
			t.Errorf("the prompt is missing %q:\n%s", expected, shown)
		}
	}
}

// Nothing to choose from asks nothing.
func TestChooseManyWithNoOptionsAsksNothing(t *testing.T) {
	prompt, asked := aPrompt("token\n")

	if chosen := prompt.ChooseMany("Which?", nil); len(chosen) != 0 {
		t.Errorf("chose %v from nothing", chosen)
	}
	if asked.String() != "" {
		t.Errorf("it asked anyway: %q", asked)
	}
}
