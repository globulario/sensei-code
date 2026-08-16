package architect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/globulario/sensei-code/internal/sensei"
)

// Caller is the Sensei MCP surface these commands read. Everything an architect
// command reports comes through here or through Sensei's own CLI; nothing is
// inferred locally except where the output says so.
type Caller interface {
	CallTool(name string, args map[string]any) (sensei.ToolResult, error)
}

// Report answers "what does Sensei know about this repository".
func RunReport(caller Caller, domain string, width int) (string, error) {
	args := map[string]any{}
	if domain != "" {
		args["domain"] = domain
	}
	result, err := caller.CallTool("awareness_metadata", args)
	if err != nil {
		return "", err
	}
	if len(result.Structured) == 0 {
		// A metadata call that returns nothing is not an empty repository.
		return "", errors.New("Sensei returned no metadata, so nothing here can be counted")
	}
	scope := domain
	if scope == "" {
		scope = "(no repository domain is bound in this checkout)"
	}
	structured := result.Structured
	// Absent and zero are both treated as "not answered here": the MCP surface
	// reports this class as zero even where a pack is installed, and a wrong
	// zero would silently credit the repository with 138 invariants it never
	// wrote.
	if toInt(structured["meta_principle_count"]) == 0 {
		// The MCP surface omits the pack size while the CLI publishes it, and
		// without it an invariant total credits this repository with governance
		// it never wrote. Borrow the one figure; if the CLI is absent the report
		// says the split is unknown rather than guessing it.
		if meta, ok := metaPrincipleCount(context.Background(), domain); ok {
			structured["meta_principle_count"] = meta
		}
	}
	return FromMetadata(scope, structured).Render(width), nil
}

// metaPrincipleCount reads the installed pack size from Sensei's CLI metadata.
func metaPrincipleCount(ctx context.Context, domain string) (int, bool) {
	args := []string{"metadata", "--json"}
	if domain != "" {
		args = append(args, "--domain", domain)
	}
	out, err := senseiCLI(ctx, "", args)
	if err != nil && strings.TrimSpace(out) == "" {
		return 0, false
	}
	var document map[string]any
	if json.Unmarshal([]byte(out), &document) != nil {
		return 0, false
	}
	raw, present := document["meta_principle_count"]
	if !present {
		return 0, false
	}
	return toInt(raw), true
}

// RunFocus answers "what governs this file, before I change it".
func RunFocus(caller Caller, domain, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("/focus needs a path: /focus internal/tui/model.go")
	}
	briefingArgs := map[string]any{"file": path, "depth": "standard"}
	preflightArgs := map[string]any{"task": "review " + path, "files": []string{path}, "mode": "standard"}
	if domain != "" {
		briefingArgs["domain"] = domain
		preflightArgs["domain"] = domain
	}

	var b strings.Builder
	b.WriteString("Focus — " + path + "\n")

	briefing, err := caller.CallTool("awareness_briefing", briefingArgs)
	if err != nil {
		return "", fmt.Errorf("Sensei briefing: %w", err)
	}
	if status := structuredString(briefing.Structured, "status"); status != "" {
		b.WriteString("  briefing status: " + humanState(status, "BRIEFING_STATUS_") + "\n")
	}
	if prose := strings.TrimSpace(firstText(briefing)); prose != "" {
		b.WriteString("\n" + indent(prose, "  ") + "\n")
	}

	preflight, err := caller.CallTool("awareness_preflight", preflightArgs)
	if err != nil {
		return "", fmt.Errorf("Sensei preflight: %w", err)
	}
	// Sensei's risk class is quoted as Sensei states it. Every other surface in
	// this tool shows the same vocabulary, and rewording a verdict makes two
	// spellings of one fact.
	risk := structuredString(preflight.Structured, "risk_class")
	if risk == "" {
		risk = "unstated"
	}
	b.WriteString("\n  risk: " + risk +
		" · confidence " + humanState(structuredString(preflight.Structured, "confidence"), "CONFIDENCE_") + "\n")
	for _, action := range stringList(preflight.Structured, "required_actions") {
		b.WriteString("    - " + action + "\n")
	}
	for _, blind := range stringList(preflight.Structured, "blind_spots") {
		b.WriteString("    ! " + blind + "\n")
	}

	b.WriteString("\n  what this does not establish:\n")
	b.WriteString("    - an empty briefing means Sensei holds nothing for this path, which is\n")
	b.WriteString("      thin coverage rather than a safe change\n")
	return strings.TrimRight(b.String(), "\n"), nil
}

// RunGate checks the working tree against what governs it, before a commit.
//
// It shells to Sensei's own enforcing gate rather than reimplementing the
// check, because deciding whether a diff is permitted is Sensei's judgement and
// a second implementation of it would be a second answer.
func RunGate(ctx context.Context, repoRoot, domain string) (string, error) {
	args := []string{"gate", "--diff", "HEAD", "--enforce"}
	if domain != "" {
		args = append(args, "--domain", domain)
	}
	out, err := senseiCLI(ctx, repoRoot, args)
	var b strings.Builder
	b.WriteString("Gate — uncommitted changes against HEAD\n\n")
	b.WriteString(indent(out, "  ") + "\n")
	if err != nil {
		b.WriteString("\n  the gate did not pass\n")
	}
	b.WriteString("\n  what a pass does not establish:\n")
	b.WriteString("    - passing the gate is not Sensei admission, and not proof of correctness\n")
	return strings.TrimRight(b.String(), "\n"), nil
}

// RunAudit reports Sensei's own evaluation of the repository.
func RunAudit(ctx context.Context, repoRoot string) (string, error) {
	var b strings.Builder
	b.WriteString("Audit — Sensei's evaluation of this repository\n\n")

	evaluation, evalErr := senseiCLI(ctx, repoRoot, []string{"repo-eval"})
	b.WriteString(indent(evaluation, "  ") + "\n")
	if evalErr != nil && strings.TrimSpace(evaluation) == "" {
		return "", fmt.Errorf("sensei repo-eval: %w", evalErr)
	}

	validation, validateErr := senseiCLI(ctx, repoRoot, []string{"validate"})
	b.WriteString("\n  corpus validation:\n" + indent(validation, "    ") + "\n")
	if validateErr != nil {
		b.WriteString("    the corpus did not validate\n")
	}
	b.WriteString("\n  this is Sensei's verdict, quoted. It scores the recorded corpus, not the\n")
	b.WriteString("  behaviour of the code.\n")
	return strings.TrimRight(b.String(), "\n"), nil
}

// senseiCLI runs one Sensei command with direct argv. Its combined output is
// returned even on failure, because Sensei's refusal text is the answer.
func senseiCLI(ctx context.Context, dir string, args []string) (string, error) {
	path, err := exec.LookPath("sensei")
	if err != nil {
		return "", errors.New("the sensei CLI is not installed, so this command cannot run")
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return strings.TrimRight(string(out), "\n"), err
}

// RunDebt names the parts of the repository nothing governs.
func RunDebt(repoRoot string, limit int) (string, error) {
	protected, err := ProtectedPaths(filepath.Join(repoRoot, "docs", "awareness"))
	if err != nil {
		return "", fmt.Errorf("read the authored corpus: %w", err)
	}
	debt, err := Measure(repoRoot, protected, []string{".go"})
	if err != nil {
		return "", err
	}
	return debt.Render(limit), nil
}

func indent(text, prefix string) string {
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return prefix + "(no output)"
	}
	return prefix + strings.ReplaceAll(text, "\n", "\n"+prefix)
}

func firstText(result sensei.ToolResult) string {
	for _, item := range result.Content {
		if item.Type == "text" && strings.TrimSpace(item.Text) != "" {
			return item.Text
		}
	}
	return ""
}

func structuredString(structured map[string]any, key string) string {
	value, _ := structured[key].(string)
	return value
}

func stringList(structured map[string]any, key string) []string {
	raw, _ := structured[key].([]any)
	var out []string
	for _, item := range raw {
		if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
			out = append(out, text)
		}
	}
	return out
}

// RunLearn records a scar so the next agent cannot repeat it.
//
// It writes to Sensei's review queue rather than the live graph. A tool that
// could promote its own lessons into governance would let one bad afternoon
// become a permanent rule, so a human promotes what survives review.
func RunLearn(caller Caller, domain, what string) (string, error) {
	what = strings.TrimSpace(what)
	if what == "" {
		return "", errors.New("/learn needs a description: /learn logout reported success while the session survived")
	}
	args := map[string]any{
		"kind":        "failure_mode",
		"title":       firstLine(what, 120),
		"description": what,
		"severity":    "high",
	}
	if domain != "" {
		args["domain"] = domain
		args["repo"] = domain
	}
	result, err := caller.CallTool("awareness_propose", args)
	if err != nil {
		// Sensei's refusal is the answer: its contract-first rules decide what
		// is worth recording, and restating a rejection as success would put a
		// lesson in the graph that is not there.
		return "", fmt.Errorf("Sensei did not accept it: %w", err)
	}
	var b strings.Builder
	b.WriteString("Learned — queued for Sensei's review\n\n  " + firstLine(what, 200) + "\n")
	if detail := strings.TrimSpace(firstText(result)); detail != "" {
		b.WriteString("\n" + indent(detail, "  ") + "\n")
	}
	b.WriteString("\n  it is a candidate, not governance: a human promotes it before anything is bound by it\n")
	return strings.TrimRight(b.String(), "\n"), nil
}

func firstLine(text string, limit int) string {
	text = strings.TrimSpace(text)
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = text[:i]
	}
	if len(text) > limit {
		text = text[:limit-1] + "…"
	}
	return text
}

// RunWhy reads back one rule by id.
//
// /focus names the invariants that govern a file, and a name is not a rule. An
// architect asked to "verify invariant X still holds" cannot do that without
// reading X, and a rule that cannot be read is one that will be guessed at.
func RunWhy(caller Caller, domain, id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", errors.New("/why needs an id: /why sensei_code.publication.never_merges")
	}
	bare := normaliseID(id)
	// The class is usually implied by the id, but an architect copies whatever
	// they were shown, so the others are tried rather than refusing on a guess.
	for _, class := range candidateClasses(id) {
		args := map[string]any{"class": class, "id": bare}
		if domain != "" {
			args["domain"] = domain
		}
		result, err := caller.CallTool("awareness_resolve", args)
		if err != nil {
			continue
		}
		node, ok := result.Structured["node"].(map[string]any)
		if !ok || node == nil {
			continue
		}
		return renderNode(class, node), nil
	}
	// Holding nothing under this id is not the same as the rule not mattering;
	// most often the id was mistyped or belongs to another domain.
	return "", fmt.Errorf("Sensei holds no invariant, failure mode or forbidden fix under %q in this domain", id)
}

func candidateClasses(id string) []string {
	switch {
	case strings.HasPrefix(id, "failure"):
		return []string{"failure_mode", "invariant", "forbidden_fix"}
	case strings.HasPrefix(id, "forbidden_fix"):
		return []string{"forbidden_fix", "invariant", "failure_mode"}
	default:
		return []string{"invariant", "failure_mode", "forbidden_fix"}
	}
}

// renderNode shows the rule and, just as importantly, what proves it. An
// invariant with no test behind it is an intention rather than a guarantee, and
// the reader should be able to see which one they have.
func renderNode(class string, node map[string]any) string {
	var b strings.Builder
	b.WriteString("Why — " + str(node, "id") + "\n")
	b.WriteString("  " + class)
	if severity := str(node, "severity"); severity != "" {
		b.WriteString(" · " + severity)
	}
	if status := str(node, "status"); status != "" {
		b.WriteString(" · " + status)
	}
	b.WriteString("\n\n  " + str(node, "label") + "\n")

	protects, tests, other := splitRelated(node)
	writeList(&b, "  protects:", protects)
	writeList(&b, "  proven by:", tests)
	writeList(&b, "  related:", other)
	if len(tests) == 0 {
		b.WriteString("\n  no test is recorded against this rule, so it states an intention rather than\n  something that would be caught if it were broken\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func splitRelated(node map[string]any) (protects, tests, other []string) {
	raw, _ := node["related_ids"].([]any)
	for _, item := range raw {
		id, _ := item.(string)
		switch {
		case strings.HasPrefix(id, "source_file:"):
			protects = append(protects, strings.TrimPrefix(id, "source_file:"))
		case strings.HasPrefix(id, "test:"):
			tests = append(tests, strings.TrimPrefix(id, "test:"))
		case id != "":
			other = append(other, id)
		}
	}
	return
}

func writeList(b *strings.Builder, title string, items []string) {
	if len(items) == 0 {
		return
	}
	b.WriteString("\n" + title + "\n")
	for _, item := range items {
		b.WriteString("    " + item + "\n")
	}
}

// qualify accepts the id in either the form Sensei prints or the bare corpus
// form, because an architect copies whichever one they were shown.
func qualify(id string) string {
	for _, prefix := range []string{"invariant:", "failure_mode:", "failure:", "forbidden_fix:"} {
		if strings.HasPrefix(id, prefix) {
			return id
		}
	}
	switch {
	case strings.HasPrefix(id, "failure."):
		return "failure_mode:" + id
	case strings.HasPrefix(id, "forbidden_fix."):
		return "forbidden_fix:" + id
	default:
		return "invariant:" + id
	}
}

// normaliseID strips the class prefix Sensei's graph queries print, so an id
// copied from either surface resolves.
func normaliseID(id string) string {
	id = strings.TrimSpace(id)
	for _, prefix := range []string{"invariant:", "failure_mode:", "failure:", "forbidden_fix:"} {
		if strings.HasPrefix(id, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(id, prefix))
		}
	}
	return id
}
