package results

import (
	"sort"
	"strconv"
	"strings"
	"time"
)

// state is what one core's evidence amounts to.
type state string

const (
	statePass      state = "pass"
	stateFail      state = "fail"
	stateStale     state = "stale"
	stateUnchecked state = "unchecked"
)

// LocalEvidence is what is known locally about one contribution, across every
// core it was checked on.
//
// Core stopped being part of a row's identity and became part of its evidence
// — a module *branch* supports several cores at once (pathauto's single
// 8.x-1.x declares ^10.2 || ^11 || ^12), so multiplying rows by a tracked core
// list produced duplicates that said nothing new.
//
// That leaves one question this answers: several cores can disagree, and the
// cell is one string. **Worst case wins, and names the core it came from.**
// The actionable fact is that something is broken and where; a cell reading
// "pass" because two of three cores were green would be worse than useless.
// Every core is listed individually under -v.
type LocalEvidence struct {
	// byCore is keyed by core major. A nil value is a core that applies but
	// was never checked.
	//
	// The PHP side has to declare this key as array-key rather than string,
	// because PHP stores "10" => x as 10 => x and no caller can hand it string
	// keys however they write them. Go has no such coercion, so the key is
	// what it says it is.
	byCore map[string]*CachedResult

	// currentRevision is what the evidence must match to be fresh — a merge
	// request's head SHA, or a patch's revision. Empty makes everything stale,
	// which is the safe direction.
	currentRevision string
}

// Evidence builds what is known across a set of cores.
func Evidence(byCore map[string]*CachedResult, currentRevision string) LocalEvidence {
	return LocalEvidence{byCore: byCore, currentRevision: currentRevision}
}

// NoEvidence is nothing applicable, nothing known — a row with no evidence at
// all.
func NoEvidence() LocalEvidence { return LocalEvidence{} }

// Cores is the cores this evidence covers, in numeric order.
func (e LocalEvidence) Cores() []string {
	cores := make([]string, 0, len(e.byCore))
	for core := range e.byCore {
		cores = append(cores, core)
	}
	sort.Slice(cores, func(a, b int) bool {
		left, _ := strconv.Atoi(cores[a])
		right, _ := strconv.Atoi(cores[b])
		if left != right {
			return left < right
		}

		return cores[a] < cores[b]
	})

	return cores
}

// Covers reports whether this evidence has an entry for every core given.
//
// The gate is handed the applicable cores and the evidence as two arguments,
// and nothing in either says they describe the same set. They agree when the
// row factory builds both from one list — but a caller that gets it wrong
// produces exactly the failure the row model exists to prevent: a merge
// request green on 11, silently unasked about 10, reading as fully green. So
// the gate checks rather than trusts.
func (e LocalEvidence) Covers(cores []string) bool {
	for _, core := range cores {
		if _, known := e.byCore[core]; !known {
			return false
		}
	}

	return true
}

// AllGreen reports whether every applicable core is green against the current
// revision.
//
// The fast lane's question, and stricter than what it used to ask. A row per
// (subject x core) meant a merge request green on 11 and unchecked on 10
// produced one READY-AUTO row and one ordinary one, and the fast lane saw the
// ready one — partial evidence, silently. One row cannot do that: a core that
// applies and has no fresh pass denies it.
func (e LocalEvidence) AllGreen() bool {
	if len(e.byCore) == 0 {
		return false
	}
	for _, core := range e.Cores() {
		if e.stateOf(core) != statePass {
			return false
		}
	}

	return true
}

// AnyFailed reports whether any applicable core has a fresh failing result.
func (e LocalEvidence) AnyFailed() bool { return len(e.coresIn(stateFail)) > 0 }

// AnyStale reports whether any applicable core's evidence is out of date.
func (e LocalEvidence) AnyStale() bool { return len(e.coresIn(stateStale)) > 0 }

// AnyUnchecked reports whether any applicable core has never been checked.
func (e LocalEvidence) AnyUnchecked() bool { return len(e.coresIn(stateUnchecked)) > 0 }

// AttentionCore is the core a maintainer should act on first, or "" when every
// applicable core is green.
//
// The same ranking the cell uses, so "fail 10" and
// `upkeep check widget 14 --version=10` cannot name different cores. Under the
// old model the row *was* a core and the suffix was trivial; now the row spans
// several and the command has to pick the one worth running.
func (e LocalEvidence) AttentionCore() string {
	for _, want := range []state{stateFail, stateStale, stateUnchecked} {
		if cores := e.coresIn(want); len(cores) > 0 {
			return cores[0]
		}
	}

	return ""
}

// FailedChecks is the distinct checks that failed on any applicable core, in
// the order they were recorded.
//
// Named rather than counted because the gate turns each into a
// local-failed:<check> reason and the guidance renders the name — "phpcs
// failed" is actionable in a way "1 check failed" is not. Deduplicated across
// cores: phpcs failing on 10 and on 11 is one thing wrong with the branch, not
// two.
func (e LocalEvidence) FailedChecks() []string {
	failed := []string{}
	seen := map[string]bool{}

	for _, core := range e.Cores() {
		result := e.byCore[core]
		if result == nil || e.stateOf(core) != stateFail {
			continue
		}
		for _, failure := range result.Result.Failures() {
			name := string(failure.Type)
			if !seen[name] {
				seen[name] = true
				failed = append(failed, name)
			}
		}
	}

	return failed
}

// LatestRecordedAt is when the newest of these results was recorded, for the
// merge command's context block. Zero when nothing has been checked.
func (e LocalEvidence) LatestRecordedAt() time.Time {
	var latest time.Time
	for _, result := range e.byCore {
		if result != nil && result.RecordedAt.After(latest) {
			latest = result.RecordedAt
		}
	}

	return latest
}

// Cell is the rendered summary: worst case, naming the core it came from.
//
// Ordered by how much it should worry a maintainer — a failure outranks stale
// evidence, which outranks a gap, which outranks a pass. Only the worst
// state's cores are named, because listing the passing ones beside a failure
// buries the fact that matters.
func (e LocalEvidence) Cell() string {
	if len(e.byCore) == 0 {
		return "–"
	}

	for _, worst := range []state{stateFail, stateStale} {
		if cores := e.coresIn(worst); len(cores) > 0 {
			return string(worst) + " " + strings.Join(cores, ",")
		}
	}

	passed := e.coresIn(statePass)
	unchecked := e.coresIn(stateUnchecked)

	// Nothing checked anywhere reads as the plain dash it always did.
	if len(passed) == 0 {
		return "–"
	}

	// Green where checked, with the gap named — a pass that covers only half
	// the applicable cores must not read like a full one.
	if len(unchecked) == 0 {
		return "pass " + strings.Join(passed, ",")
	}

	return "pass " + strings.Join(passed, ",") + " · ? " + strings.Join(unchecked, ",")
}

// Describe is every core with its own state, for -v. The detail half of
// worst-case-plus-detail: the cell says what is wrong, this says where.
func (e LocalEvidence) Describe() string {
	if len(e.byCore) == 0 {
		return "–"
	}

	parts := make([]string, 0, len(e.byCore))
	for _, core := range e.Cores() {
		parts = append(parts, core+":"+string(e.stateOf(core)))
	}

	return strings.Join(parts, " ")
}

func (e LocalEvidence) coresIn(want state) []string {
	cores := []string{}
	for _, core := range e.Cores() {
		if e.stateOf(core) == want {
			cores = append(cores, core)
		}
	}

	return cores
}

func (e LocalEvidence) stateOf(core string) state {
	result := e.byCore[core]
	if result == nil {
		return stateUnchecked
	}
	if e.currentRevision == "" || result.SHA != e.currentRevision {
		return stateStale
	}
	if result.Result.AllPassed() {
		return statePass
	}

	return stateFail
}
