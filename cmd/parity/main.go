// Command parity is a TEMPORARY live harness (plan 1, task 3): it runs the
// native gitlab.Client and the drupalorg-cli-backed MergeRequestReader side by
// side against git.drupalcode.org and diffs only the fields upkeep's callers
// read. Read-only. Task 4 deletes it.
package main

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/owenbush/upkeep/internal/drupalorgcli"
	"github.com/owenbush/upkeep/internal/gitlab"
)

var modules = []string{"content_sync", "pathauto", "token", "jumplinks"}

// mergedLimit is what both callers pass (dashboard.mergedLimit,
// command.mergedPatchLimit).
const mergedLimit = 100

type field struct {
	name string
	get  func(gitlab.MergeRequest) string
	// why is the call site that reads it.
	why string
}

var space = regexp.MustCompile(`\s+`)

func norm(s string) string { return space.ReplaceAllString(strings.TrimSpace(s), " ") }

func ts(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return s
	}

	return t.UTC().Truncate(time.Second).Format(time.RFC3339)
}

func tri(t gitlab.Tristate) string {
	switch t {
	case gitlab.Yes:
		return "yes"
	case gitlab.No:
		return "no"
	}

	return ""
}

func num(n int) string {
	if n == 0 {
		return ""
	}

	return strconv.Itoa(n)
}

// Fields read off list results (OpenMergeRequests / MergedMergeRequests feed
// patches.Pair, issues, notes, dashboard snapshot fallback, api:probe).
var listFields = []field{
	{"IID", func(m gitlab.MergeRequest) string { return num(m.IID) }, "everywhere (row identity, detail refetch)"},
	{"Title", func(m gitlab.MergeRequest) string { return norm(m.Title) }, "drupal.ExtractOwningIssue, report, notes, issues table"},
	{"State", func(m gitlab.MergeRequest) string { return m.State }, "patches.Contribution landed(), rowfactory, checkout"},
	{"AuthorUsername", func(m gitlab.MergeRequest) string { return m.AuthorUsername }, "config.BotPattern.MatchesAuthor (gate, notes)"},
	{"AuthorID", func(m gitlab.MergeRequest) string { return num(m.AuthorID) }, "config.BotPattern.MatchesAuthor (gate, notes)"},
	{"SourceBranch", func(m gitlab.MergeRequest) string { return m.SourceBranch }, "ExtractOwningIssue, BotPattern, checkout"},
	{"TargetBranch", func(m gitlab.MergeRequest) string { return m.TargetBranch }, "dashboard.FetchModule branch set, rowfactory"},
	{"IsDraft", func(m gitlab.MergeRequest) string { return strconv.FormatBool(m.IsDraft()) }, "gate draft reason, report.Draft"},
	{"WebURL", func(m gitlab.MergeRequest) string { return m.WebURL }, "report, notes, contributions"},
	{"Description", func(m gitlab.MergeRequest) string { return norm(m.Description) }, "drupal.ExtractOwningIssue in patches.Pair, rowfactory"},
	{"SourceProjectID", func(m gitlab.MergeRequest) string { return num(m.SourceProjectID) }, "patches.Pair fork-nid pairing"},
	{"UpdatedAt", func(m gitlab.MergeRequest) string { return ts(m.UpdatedAt) }, "patches hasWorkNewerThanLanding, row.go"},
	{"MergedAt", func(m gitlab.MergeRequest) string { return ts(m.MergedAt) }, "gitlab.LatestMerged, patches landed(), guidance"},
}

// Fields additionally read off a single merge request (MergeRequest).
var detailFields = append(slices.Clone(listFields),
	field{"DetailedMergeStatus", func(m gitlab.MergeRequest) string { return m.DetailedMergeStatus }, "api:probe"},
	field{"HeadSHA", func(m gitlab.MergeRequest) string { return m.HeadSHA }, "MergeRevision evidence keying, merge guard, report"},
	field{"CarriesChanges", func(m gitlab.MergeRequest) string { return tri(m.CarriesChanges()) }, "gate no-changes, patches emptiness, guidance"},
)

// The head pipeline is compared field by field, so a missing ProjectID is
// not reported as a value difference of the whole pipeline.
func init() {
	for _, f := range pipelineFields {
		get := f.get
		detailFields = append(detailFields, field{"HeadPipeline." + f.name,
			func(m gitlab.MergeRequest) string { return get(m.HeadPipeline) }, f.why})
	}
}

type pfield struct {
	name string
	get  func(*gitlab.Pipeline) string
	why  string
}

var pipelineFields = []pfield{
	{"present", func(p *gitlab.Pipeline) string { return strconv.FormatBool(p != nil) }, "gate ci-missing, CICell"},
	{"ID", func(p *gitlab.Pipeline) string { return pf(p, func(p *gitlab.Pipeline) string { return num(p.ID) }) }, "mr:logs, api:probe"},
	{"Status", func(p *gitlab.Pipeline) string {
		return pf(p, func(p *gitlab.Pipeline) string { return string(p.Status) })
	}, "gate ci-red/IsGreen, CICell"},
	{"RawStatus", func(p *gitlab.Pipeline) string { return pf(p, func(p *gitlab.Pipeline) string { return p.RawStatus }) }, "gate ci-not-green:<raw>, CICell, report, mr:logs"},
	{"WebURL", func(p *gitlab.Pipeline) string { return pf(p, func(p *gitlab.Pipeline) string { return p.WebURL }) }, "merge, api:probe, mr:logs, report"},
	{"ProjectID", func(p *gitlab.Pipeline) string {
		return pf(p, func(p *gitlab.Pipeline) string { return num(p.ProjectID) })
	}, "mr:logs pipelineLocation (jobs live in the fork)"},
}

func pf(p *gitlab.Pipeline, f func(*gitlab.Pipeline) string) string {
	if p == nil {
		return ""
	}

	return f(p)
}

func pipelineString(p *gitlab.Pipeline) string {
	if p == nil {
		return ""
	}
	parts := []string{}
	for _, f := range pipelineFields[1:] {
		parts = append(parts, f.name+"="+f.get(p))
	}

	return strings.Join(parts, " ")
}

// diff is one field difference.
type diff struct {
	where, field, native, cli string
}

func (d diff) missing() bool { return d.cli == "" && d.native != "" }

type candidate struct {
	name                  string
	nativeTimes, cliTimes []time.Duration
	diffs                 []diff
	notes                 []string
	failures              []string
	compared              int
	fieldsCompared        []string
}

func (c *candidate) time(native, cli time.Duration) {
	c.nativeTimes = append(c.nativeTimes, native)
	c.cliTimes = append(c.cliTimes, cli)
}

func median(ds []time.Duration) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	s := slices.Clone(ds)
	slices.Sort(s)

	return s[len(s)/2]
}

func total(ds []time.Duration) time.Duration {
	var t time.Duration
	for _, d := range ds {
		t += d
	}

	return t
}

func ms(d time.Duration) string { return fmt.Sprintf("%dms", d.Milliseconds()) }

func timed[T any](f func() T) (T, time.Duration) {
	start := time.Now()
	out := f()

	return out, time.Since(start)
}

type listResult struct {
	list    []gitlab.MergeRequest
	failure *gitlab.Failure
}

type mrResult struct {
	mr      *gitlab.MergeRequest
	failure *gitlab.Failure
}

type plResult struct {
	p       *gitlab.Pipeline
	failure *gitlab.Failure
}

func iids(list []gitlab.MergeRequest) []int {
	out := make([]int, 0, len(list))
	for _, m := range list {
		out = append(out, m.IID)
	}

	return out
}

func compareMR(c *candidate, where string, fields []field, native, cli gitlab.MergeRequest) {
	for _, f := range fields {
		n, l := f.get(native), f.get(cli)
		if n != l {
			c.diffs = append(c.diffs, diff{where, f.name, n, l})
		}
	}
}

func compareList(c *candidate, module string, fields []field, native, cli []gitlab.MergeRequest) {
	ni, ci := iids(native), iids(cli)
	if !slices.Equal(ni, ci) {
		onlyN, onlyC := setDiff(ni, ci), setDiff(ci, ni)
		if len(onlyN) > 0 || len(onlyC) > 0 {
			c.diffs = append(c.diffs, diff{module, "IID set",
				fmt.Sprintf("%d MRs; only native: %v", len(ni), clip(onlyN)),
				fmt.Sprintf("%d MRs; only CLI: %v", len(ci), clip(onlyC))})
		} else {
			c.diffs = append(c.diffs, diff{module, "order", fmt.Sprint(clip(ni)), fmt.Sprint(clip(ci))})
		}
	}
	byIID := map[int]gitlab.MergeRequest{}
	for _, m := range cli {
		byIID[m.IID] = m
	}
	for _, n := range native {
		l, ok := byIID[n.IID]
		if !ok {
			continue
		}
		c.compared++
		compareMR(c, fmt.Sprintf("%s !%d", module, n.IID), fields, n, l)
	}
}

func setDiff(a, b []int) []int {
	out := []int{}
	for _, x := range a {
		if !slices.Contains(b, x) {
			out = append(out, x)
		}
	}

	return out
}

func clip(xs []int) []int {
	if len(xs) > 12 {
		return xs[:12]
	}

	return xs
}

func main() {
	resolver := gitlab.NewTokenResolver("", "", nil)
	token := resolver.Resolve()
	if token == "" {
		fmt.Fprintln(os.Stderr, "no GitLab token resolved; refusing to run an anonymous comparison")
		os.Exit(2)
	}
	base := gitlab.NewClient(nil, token, "", "")
	if failure := drupalorgcli.Check(); failure != nil {
		fmt.Fprintln(os.Stderr, failure.Message)
		os.Exit(2)
	}
	cli := drupalorgcli.NewMergeRequestReader(drupalorgcli.New(resolver, 0))

	open := &candidate{name: "OpenMergeRequests"}
	merged := &candidate{name: "MergedMergeRequests"}
	single := &candidate{name: "MergeRequest"}
	head := &candidate{name: "HeadPipeline"}
	for _, f := range listFields {
		if f.name != "MergedAt" {
			open.fieldsCompared = append(open.fieldsCompared, f.name)
		}
		merged.fieldsCompared = append(merged.fieldsCompared, f.name)
	}
	for _, f := range detailFields {
		single.fieldsCompared = append(single.fieldsCompared, f.name)
	}
	for _, f := range pipelineFields {
		head.fieldsCompared = append(head.fieldsCompared, f.name)
	}
	openFields := slices.DeleteFunc(slices.Clone(listFields), func(f field) bool { return f.name == "MergedAt" })

	var log strings.Builder
	usedIIDs := map[string][]string{}

	for _, module := range modules {
		project, failure := base.Fresh().Project(module)
		if failure != nil {
			fmt.Fprintf(os.Stderr, "%s: project: %s\n", module, failure.Message)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "== %s (%s, id %d)\n", module, project.PathWithNamespace, project.ID)

		// OpenMergeRequests
		nOpen, nt := timed(func() listResult { l, f := base.Fresh().OpenMergeRequests(*project); return listResult{l, f} })
		cOpen, ct := timed(func() listResult { l, f := cli.OpenMergeRequests(*project); return listResult{l, f} })
		open.time(nt, ct)
		fmt.Fprintf(&log, "- OpenMergeRequests %s: native %d MRs in %s, CLI %d MRs in %s\n", module, len(nOpen.list), ms(nt), len(cOpen.list), ms(ct))
		if nOpen.failure != nil || cOpen.failure != nil {
			open.failures = append(open.failures, fmt.Sprintf("%s: native=%v cli=%v", module, nOpen.failure, cOpen.failure))
		} else {
			compareList(open, module, openFields, nOpen.list, cOpen.list)
		}

		// MergedMergeRequests
		nMerged, nt := timed(func() listResult {
			l, f := base.Fresh().MergedMergeRequests(*project, mergedLimit)
			return listResult{l, f}
		})
		cMerged, ct := timed(func() listResult { l, f := cli.MergedMergeRequests(*project, mergedLimit); return listResult{l, f} })
		merged.time(nt, ct)
		fmt.Fprintf(&log, "- MergedMergeRequests(%d) %s: native %d MRs in %s, CLI %d MRs in %s\n", mergedLimit, module, len(nMerged.list), ms(nt), len(cMerged.list), ms(ct))
		if nMerged.failure != nil || cMerged.failure != nil {
			merged.failures = append(merged.failures, fmt.Sprintf("%s: native=%v cli=%v", module, nMerged.failure, cMerged.failure))
		} else {
			compareList(merged, module, listFields, nMerged.list, cMerged.list)
		}

		// iids for MergeRequest/HeadPipeline: first three open, first merged,
		// and the first open one whose head pipeline is not success.
		picks := []int{}
		for _, m := range nOpen.list {
			if len(picks) < 3 {
				picks = append(picks, m.IID)
			}
		}
		nonSuccess := 0
		for i, m := range nOpen.list {
			if i >= 40 {
				break
			}
			d, f := base.MergeRequest(*project, m.IID)
			if f == nil && (d.HeadPipeline == nil || d.HeadPipeline.Status != gitlab.StatusSuccess) {
				nonSuccess = m.IID
				break
			}
		}
		if nonSuccess != 0 && !slices.Contains(picks, nonSuccess) {
			picks = append(picks, nonSuccess)
		}
		if len(nMerged.list) > 0 {
			picks = append(picks, nMerged.list[0].IID)
		}
		for _, iid := range picks {
			label := strconv.Itoa(iid)
			if iid == nonSuccess {
				label += " (non-success pipeline)"
			}
			usedIIDs[module] = append(usedIIDs[module], label)

			nMR, nt := timed(func() mrResult { m, f := base.Fresh().MergeRequest(*project, iid); return mrResult{m, f} })
			cMR, ct := timed(func() mrResult { m, f := cli.MergeRequest(*project, iid); return mrResult{m, f} })
			single.time(nt, ct)
			fmt.Fprintf(&log, "- MergeRequest %s !%d: native %s, CLI %s\n", module, iid, ms(nt), ms(ct))
			where := fmt.Sprintf("%s !%d", module, iid)
			if nMR.failure != nil || cMR.failure != nil {
				single.failures = append(single.failures, fmt.Sprintf("%s: native=%v cli=%v", where, kind(nMR.failure), kind(cMR.failure)))
			} else {
				single.compared++
				compareMR(single, where, detailFields, *nMR.mr, *cMR.mr)
			}

			nPL, nt := timed(func() plResult { p, f := base.Fresh().HeadPipeline(*project, iid); return plResult{p, f} })
			cPL, ct := timed(func() plResult { p, f := cli.HeadPipeline(*project, iid); return plResult{p, f} })
			head.time(nt, ct)
			fmt.Fprintf(&log, "- HeadPipeline %s !%d: native %s (%s), CLI %s (%s)\n", module, iid, ms(nt), pipelineString(nPL.p), ms(ct), pipelineString(cPL.p))
			if nPL.failure != nil || cPL.failure != nil {
				head.failures = append(head.failures, fmt.Sprintf("%s: native=%v cli=%v", where, kind(nPL.failure), kind(cPL.failure)))
			} else {
				head.compared++
				for _, f := range pipelineFields {
					n, l := f.get(nPL.p), f.get(cPL.p)
					if n != l {
						head.diffs = append(head.diffs, diff{where, f.name, n, l})
					}
				}
			}
		}
	}

	render([]*candidate{open, merged, single, head}, usedIIDs, log.String())
}

func kind(f *gitlab.Failure) string {
	if f == nil {
		return "ok"
	}

	return fmt.Sprintf("%v: %s", f.Kind, f.Message)
}

func verdict(c *candidate) (string, string) {
	missing := map[string]bool{}
	other := map[string]bool{}
	for _, d := range c.diffs {
		if d.missing() {
			missing[d.field] = true
		} else {
			other[d.field] = true
		}
	}
	ratio := float64(total(c.cliTimes)) / float64(max(total(c.nativeTimes), 1))
	if len(c.failures) > 0 {
		other["CLI call failures"] = true
	}
	if len(missing) > 0 {
		return "FAIL", "CLI cannot supply caller-read field(s): " + strings.Join(keys(missing), ", ") +
			extra(other) + fmt.Sprintf("; CLI %.1f× native", ratio)
	}
	reasons := keys(other)
	if ratio > 2 {
		reasons = append(reasons, fmt.Sprintf("CLI %.1f× slower", ratio))
	}
	if len(reasons) > 0 {
		return "NEEDS-DECISION", strings.Join(reasons, "; ")
	}

	return "PASS", fmt.Sprintf("CLI %.1f× native", ratio)
}

func extra(other map[string]bool) string {
	if len(other) == 0 {
		return ""
	}

	return "; also: " + strings.Join(keys(other), ", ")
}

func keys(m map[string]bool) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)

	return out
}

func render(cands []*candidate, used map[string][]string, log string) {
	fmt.Println("# Parity results: native gitlab.Client vs drupalorg-cli 0.10.3")
	fmt.Println()
	fmt.Printf("Run %s by `go run ./cmd/parity` (authenticated, read-only). Modules: %s.\n\n",
		time.Now().UTC().Format(time.RFC3339), strings.Join(modules, ", "))
	fmt.Println("## Summary")
	fmt.Println()
	fmt.Println("| Candidate | Verdict | Compared | Diffs (missing / value) | Native total (median) | CLI total (median) | Ratio | Reason |")
	fmt.Println("|---|---|---|---|---|---|---|---|")
	for _, c := range cands {
		v, why := verdict(c)
		m, o := 0, 0
		for _, d := range c.diffs {
			if d.missing() {
				m++
			} else {
				o++
			}
		}
		ratio := float64(total(c.cliTimes)) / float64(max(total(c.nativeTimes), 1))
		fmt.Printf("| %s | **%s** | %d | %d / %d | %s (%s) | %s (%s) | %.1f× | %s |\n", c.name, v, c.compared, m, o,
			ms(total(c.nativeTimes)), ms(median(c.nativeTimes)), ms(total(c.cliTimes)), ms(median(c.cliTimes)), ratio, why)
	}
	fmt.Println()
	fmt.Println("Verdict rules: a caller-read field the CLI leaves empty where native has a value = FAIL (no native fallback by user decision); any other diff, call failure, or CLI > 2× native wall-clock = NEEDS-DECISION. Timestamps compared in UTC at second precision, strings trimmed and whitespace-collapsed. Native calls each use a fresh (unmemoized) client; CLI calls run as upkeep would run them (no `--no-cache`).")
	fmt.Println()
	fmt.Println("## IIDs used for MergeRequest / HeadPipeline")
	fmt.Println()
	for _, m := range modules {
		fmt.Printf("- %s: %s\n", m, strings.Join(used[m], ", "))
	}
	fmt.Println()
	fmt.Println("## Caller-read fields (why each is compared)")
	fmt.Println()
	fmt.Println("Determined from call sites outside internal/gitlab and internal/drupalorgcli (`grep` for each field). List results feed patches.Pair (issues, patches), the dashboard snapshot (where the list row is the fallback when the detail fetch fails, and merged rows are stored as-is), notes and api:probe; single results feed the gate, dashboard rows, check/merge/checkout/issue loop, mr:logs and api:probe. HeadPipeline() itself has no caller outside internal/gitlab — every pipeline read goes through MergeRequest().HeadPipeline — so its field list is the gitlab.Pipeline fields those readers use.")
	fmt.Println()
	fmt.Println("| Field | Read by | Open | Merged | MergeRequest |")
	fmt.Println("|---|---|---|---|---|")
	for _, f := range detailFields {
		in := func(c *candidate) string {
			if slices.Contains(c.fieldsCompared, f.name) {
				return "yes"
			}

			return "–"
		}
		fmt.Printf("| %s | %s | %s | %s | %s |\n", f.name, f.why, in(cands[0]), in(cands[1]), in(cands[2]))
	}
	fmt.Println()
	fmt.Println("| Pipeline field (HeadPipeline) | Read by |")
	fmt.Println("|---|---|")
	for _, f := range pipelineFields {
		fmt.Printf("| %s | %s |\n", f.name, f.why)
	}
	fmt.Println()
	for _, c := range cands {
		v, why := verdict(c)
		fmt.Printf("## %s — %s\n\n%s\n\n", c.name, v, why)
		fmt.Printf("Timing: native total %s / median %s over %d calls; CLI total %s / median %s.\n\n",
			ms(total(c.nativeTimes)), ms(median(c.nativeTimes)), len(c.nativeTimes), ms(total(c.cliTimes)), ms(median(c.cliTimes)))
		for _, f := range c.failures {
			fmt.Printf("- call failure: %s\n", cutN(f, 400))
		}
		byField := map[string][]diff{}
		order := []string{}
		for _, d := range c.diffs {
			if _, ok := byField[d.field]; !ok {
				order = append(order, d.field)
			}
			byField[d.field] = append(byField[d.field], d)
		}
		if len(order) == 0 {
			fmt.Println("No field differences.")
			fmt.Println()
			continue
		}
		fmt.Println("| Field | Diffs | Kind | Example(s) |")
		fmt.Println("|---|---|---|---|")
		for _, name := range order {
			ds := byField[name]
			missingN := 0
			for _, d := range ds {
				if d.missing() {
					missingN++
				}
			}
			kindStr := "value"
			switch {
			case missingN == len(ds):
				kindStr = "missing in CLI"
			case missingN > 0:
				kindStr = fmt.Sprintf("%d missing, %d value", missingN, len(ds)-missingN)
			}
			ex := []string{}
			for i, d := range ds {
				if i >= 2 {
					break
				}
				ex = append(ex, fmt.Sprintf("%s: native `%s` vs CLI `%s`", d.where, cut(d.native), cut(d.cli)))
			}
			fmt.Printf("| %s | %d | %s | %s |\n", name, len(ds), kindStr, strings.Join(ex, "<br>"))
		}
		fmt.Println()
	}
	fmt.Println("## Per-call log")
	fmt.Println()
	fmt.Print(log)
}

func cut(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "`", "'")
	if len(s) > 90 {
		return s[:90] + "…"
	}
	if s == "" {
		return "∅"
	}

	return s
}

func cutN(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}

	return s
}
