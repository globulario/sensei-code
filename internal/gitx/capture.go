package gitx

// The candidate boundary.
//
// sensei-code#89: an implementor verified a two-line edit by building the
// command, and the 9.2 MB binary it produced was swept into the candidate by
// intent-to-add. From there every governance surface failed on SIZE rather than
// on substance -- the audit refused the payload, edit-check overflowed its
// transport, two reviewers could not bound a review they could not read -- and
// the engine retried the same oversized candidate through the next executor as
// if judgement were the hard part. The correct two lines were never judged.
//
// A candidate contains the CHANGE. Build outputs a worker produces to check its
// work are not the change, and they enter the candidate only when the plan
// names them as intended output. Everything else a new binary might be is
// excluded here, named, and carried as metadata -- not hauled through gRPC to
// blind a reviewer to text they could have read.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Artifact is a path the candidate boundary classified, with why.
type Artifact struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
	// Class is "binary" or "oversized".
	Class string `json:"class"`
	// Excluded says the path was taken back out of the candidate.
	Excluded bool   `json:"excluded"`
	Reason   string `json:"reason"`
}

// Capture is a candidate's change as governance may read it.
type Capture struct {
	// Diff is the textual patch against the base. Binary members that remain
	// in the candidate appear as git's "Binary files differ" line, never as
	// bytes; the bytes are what no reviewer needs and every transport chokes on.
	Diff string
	// Excluded are new paths the boundary refused: binaries and oversized
	// files the plan did not name.
	Excluded []Artifact
	// Binaries are binary members the candidate legitimately holds -- tracked
	// binaries it modified, or new ones the plan named -- as metadata.
	Binaries []Artifact
	// Tree is the canonical tree: the base tree plus exactly Paths. It is the
	// candidate's CONTENT IDENTITY and the immutable centre of the capture --
	// Diff is rendered FROM it, not from a second look at the worktree.
	//
	// Two sequential reads of a mutable worktree leave a seam: the content can
	// move between the read that produced the review text and the read that
	// produced the tree. Deriving Diff from Tree means there is one measurement
	// and one representation of it, with no instant in between.
	//
	// (An earlier version of this comment argued that a kept binary renders as
	// "Binary files differ" and so different bytes could share a Diff. That is
	// wrong and was measured to be wrong: git also emits an `index <old>..<new>`
	// line, whose abbreviated blob hashes distinguish them. The seam above is
	// the reason; the binary corner is not.)
	//
	// A candidate that changed nothing has Tree equal to the base's own tree,
	// which is how "produced no work" is MEASURED rather than inferred.
	Tree string
	// BaseTree is the base commit's own tree, carried so a caller can MEASURE
	// "this candidate produced no work" as Tree == BaseTree rather than infer
	// it from an empty path list.
	BaseTree string
	// Paths is the exact set of repository-relative paths this candidate
	// changed, as Git reports them, taken AFTER artifact exclusions.
	//
	// It exists because every consumer used to re-derive it from text: this
	// file split `--numstat` on tabs after TrimSpace, and report.FromDiff
	// parsed `diff --git a/P b/P` headers and failed open on a quoted path.
	// A pathname Git quotes was therefore invisible to whichever consumer
	// looked -- and the canonical candidate identity commit stages exactly
	// these paths, so an identity built on a lossy set would be exact about
	// the wrong tree.
	//
	// Produced with `--name-status -z`, parsed on NUL boundaries: no trimming,
	// no tab splitting, no unquoting. Rename detection is deliberately OFF, so
	// a rename arrives as a deletion at the old path and an addition at the
	// new one -- which is what a tree builder needs, rather than a
	// presentation-level encoding it would have to undo.
	Paths []string
}

// RenderCandidateDiff is the ONE rendering of a candidate.
//
// Capture uses it to produce what the reviewer reads, and any later comparison
// -- diff(B,C) against diff(B,T) -- must call this rather than reassemble an
// equivalent command line. Two command lines that are equivalent today are two
// things that can drift apart tomorrow, and the whole point of the comparison
// is that a difference means something.
//
// Text only: `--binary` would inline every kept binary as a patch, which is
// exactly the payload nobody can judge and every transport refuses.
func (r Repo) RenderCandidateDiff(ctx context.Context, base, treeish string) (string, error) {
	return r.raw(ctx, "diff", "--no-ext-diff", base, treeish, "--")
}

// validateBoundaryAgainstTree re-asks the artifact question of the frozen tree.
//
// Every load-bearing claim must ultimately refer to T: if T is the content
// authority, then artifact admissibility has to hold of T and not merely of
// whatever the worktree held when the classification loop ran.
func (r Repo) validateBoundaryAgainstTree(ctx context.Context, base, tree string, want map[string]bool) error {
	out, err := r.raw(ctx, "diff", "--no-ext-diff", "--no-renames", "--numstat", "-z", base, tree, "--")
	if err != nil {
		return err
	}
	records := strings.Split(out, "\x00")
	if n := len(records); n > 0 && records[n-1] == "" {
		records = records[:n-1]
	}
	for _, record := range records {
		parts := strings.SplitN(record, "\t", 3)
		if len(parts) != 3 {
			return fmt.Errorf("malformed --numstat -z record while validating the frozen tree: %q", record)
		}
		added, deleted, path := parts[0], parts[1], parts[2]
		if want[path] || r.existsAt(ctx, base, path) {
			continue
		}
		if added == "-" && deleted == "-" {
			return fmt.Errorf("the frozen candidate tree holds %q as a new binary the plan did not name; "+
				"the artifact boundary allowed it against different bytes", path)
		}
		size, err := r.sizeInTree(ctx, tree, path)
		if err != nil {
			return err
		}
		if size > oversizedBytes {
			return fmt.Errorf("the frozen candidate tree holds %q at %d bytes, over the %d MiB limit, and the plan did not name it; "+
				"the artifact boundary allowed it against different bytes", path, size, oversizedBytes>>20)
		}
	}
	return nil
}

// sizeInTree is a blob's size AS THE FROZEN TREE HOLDS IT, not as the worktree
// happens to hold it now.
func (r Repo) sizeInTree(ctx context.Context, tree, path string) (int64, error) {
	out, err := r.envOutput(ctx, []string{"GIT_LITERAL_PATHSPECS=1"},
		"ls-tree", "-l", "-z", tree, "--", path)
	if err != nil {
		return 0, err
	}
	record := strings.TrimSuffix(out, "\x00")
	head, _, ok := strings.Cut(record, "\t")
	if !ok {
		return 0, fmt.Errorf("cannot read the size of %q in tree %s", path, tree)
	}
	fields := strings.Fields(head)
	if len(fields) != 4 {
		return 0, fmt.Errorf("cannot read the size of %q in tree %s: %q", path, tree, head)
	}
	size, err := strconv.ParseInt(fields[3], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("cannot read the size of %q in tree %s: %w", path, tree, err)
	}
	return size, nil
}

// literalPathOutput runs git with pathspec magic DISABLED.
//
// Every path this package hands back to git came from git as a measured
// pathname. A file genuinely named ":(glob)*" survives the NUL-safe capture
// intact and is then read by `reset` as a pattern, unstaging paths the boundary
// never classified. Exact bytes are not enough if the receiver may read them as
// a pattern.
func (r Repo) literalPathOutput(ctx context.Context, args ...string) (string, error) {
	return r.envOutput(ctx, []string{"GIT_LITERAL_PATHSPECS=1"}, args...)
}

// parseNameStatusZ reads `--name-status -z` output: a status field and a path
// field, each terminated by NUL. With rename detection off there is never a
// second path field, so the shape is a strict alternation.
//
// Nothing here trims, splits on whitespace, or unquotes. A pathname containing
// a tab, a newline, a quote or a non-UTF-8 byte survives it unchanged, which is
// the whole reason this function exists.
//
// It FAILS CLOSED. This is identity infrastructure now: a record it cannot
// model is a pathname it cannot vouch for, and silently skipping one would omit
// a path from the set that decides what enters the canonical tree.
func parseNameStatusZ(out string) ([]string, error) {
	fields := strings.Split(out, "\x00")
	// A NUL-terminated stream ends with one empty field. Anything else empty is
	// a record this parser does not understand.
	if n := len(fields); n > 0 && fields[n-1] == "" {
		fields = fields[:n-1]
	}
	if len(fields)%2 != 0 {
		return nil, fmt.Errorf("malformed --name-status -z output: %d field(s), which is not status/path pairs", len(fields))
	}
	var paths []string
	for i := 0; i+1 < len(fields); i += 2 {
		status, path := fields[i], fields[i+1]
		if status == "" || path == "" {
			return nil, fmt.Errorf("malformed --name-status -z record at field %d: status %q path %q", i, status, path)
		}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, nil
}

// oversizedBytes is the size above which a NEW non-binary file is treated as
// an artifact unless the plan names it. Chosen below the audit's 5 MiB payload
// limit and the 4 MiB gRPC message limit, so that a generated text file cannot
// do by size what a binary does by kind.
const oversizedBytes = 3 << 20

// CandidateCapture records the candidate's new paths, refuses the ones that
// are artifacts rather than change, and returns what remains as text.
//
// intended are repository-relative paths the plan names as the change's own
// outputs; an artifact at one of those paths is kept, as metadata.
func (r Repo) CandidateCapture(ctx context.Context, base string, intended []string) (Capture, error) {
	// Before AND after recording new paths: `add` itself looks inside a
	// tracked gitlink whose path holds a repository, so that must be refused
	// before add runs; an embedded repository add has just recorded must be
	// refused after.
	if err := r.refuseNewGitlinks(ctx, base); err != nil {
		return Capture{}, err
	}
	// Before the first operation that would pass content through a filter:
	// every tracked and every new, non-ignored path (RULING-319).
	listed, err := r.raw(ctx, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return Capture{}, err
	}
	if err := r.refuseFilterAttributes(ctx, "", splitZ(listed)); err != nil {
		return Capture{}, err
	}
	if _, err := r.output(ctx, "add", "--intent-to-add", "--", "."); err != nil {
		return Capture{}, err
	}
	if err := r.refuseNewGitlinks(ctx, base); err != nil {
		return Capture{}, err
	}
	// The intended set is compared against pathnames Git reported EXACTLY, so it
	// must not normalise them either. A plan naming " asset.bin" or "asset.bin "
	// -- with real leading or trailing whitespace, which is data in a pathname
	// -- would otherwise fail to match its own measured path and be excluded as
	// an unplanned artifact. TrimSpace here undid, on one side, the exactness
	// the other side had just gained.
	//
	// The "./" prefix is still dropped: Git never reports a path that way, so it
	// can only be a human writing "./x" for "x".
	want := map[string]bool{}
	for _, p := range intended {
		want[filepath.ToSlash(strings.TrimPrefix(p, "./"))] = true
	}
	// -z and --no-renames for the same reason Paths uses them: this loop
	// decides which paths are ARTIFACTS and resets them out of the candidate,
	// so a pathname it misreads is a file excluded or kept by the wrong name.
	// The exact path set downstream cannot repair a boundary decision already
	// made against a mangled name.
	numstatArgs := []string{"diff", "--no-ext-diff", "--no-renames", "--numstat", "-z"}
	if b := strings.TrimSpace(base); b != "" {
		numstatArgs = append(numstatArgs, b)
	}
	numstat, err := r.raw(ctx, append(numstatArgs, "--")...)
	if err != nil {
		return Capture{}, err
	}
	var cap Capture
	numstatRecords := strings.Split(numstat, "\x00")
	if n := len(numstatRecords); n > 0 && numstatRecords[n-1] == "" {
		numstatRecords = numstatRecords[:n-1]
	}
	for _, record := range numstatRecords {
		// Each record is "added\tdeleted\tpath". Only the first two tabs are
		// separators; everything after them is the pathname's own bytes, which
		// may themselves contain a tab. TrimSpace is deliberately absent: a
		// pathname may begin or end with whitespace.
		parts := strings.SplitN(record, "\t", 3)
		if len(parts) != 3 {
			// Fail closed: this loop decides which paths are artifacts and
			// resets them out of the candidate. A record it cannot read is a
			// classification it cannot make.
			return Capture{}, fmt.Errorf("malformed --numstat -z record: %q", record)
		}
		added, deleted, path := parts[0], parts[1], parts[2]
		isNew := !r.existsAt(ctx, base, path)
		size := r.sizeOf(path)
		switch {
		case added == "-" && deleted == "-":
			a := Artifact{Path: path, Size: size, Class: "binary"}
			if isNew && !want[path] {
				a.Excluded, a.Reason = true, "a new binary the plan did not name as an intended output"
				if _, err := r.literalPathOutput(ctx, "reset", "-q", "--", path); err != nil {
					return Capture{}, fmt.Errorf("exclude %s from the candidate: %w", path, err)
				}
				cap.Excluded = append(cap.Excluded, a)
				continue
			}
			a.Reason = "binary member kept: " + keptWhy(isNew, want[path])
			cap.Binaries = append(cap.Binaries, a)
		default:
			if isNew && !want[path] && size > oversizedBytes {
				a := Artifact{Path: path, Size: size, Class: "oversized", Excluded: true,
					Reason: "a new file over " + strconv.Itoa(oversizedBytes>>20) + " MiB the plan did not name as an intended output"}
				if _, err := r.literalPathOutput(ctx, "reset", "-q", "--", path); err != nil {
					return Capture{}, fmt.Errorf("exclude %s from the candidate: %w", path, err)
				}
				cap.Excluded = append(cap.Excluded, a)
			}
		}
	}
	sort.Slice(cap.Excluded, func(i, j int) bool { return cap.Excluded[i].Path < cap.Excluded[j].Path })
	// The exact path set, taken after exclusions so it names what the reviewer
	// will actually judge. Same base, same boundary, one authority.
	nameArgs := []string{"diff", "--no-ext-diff", "--no-renames", "--name-status", "-z"}
	if b := strings.TrimSpace(base); b != "" {
		nameArgs = append(nameArgs, b)
	}
	names, err := r.raw(ctx, append(nameArgs, "--")...)
	if err != nil {
		return Capture{}, err
	}
	cap.Paths, err = parseNameStatusZ(names)
	if err != nil {
		return Capture{}, err
	}

	// A capture with no base has no content identity: a tree is "the base tree
	// plus these paths", and there is no base tree to start from. The governed
	// loop always has one (the candidate's recorded base); this shape exists
	// for callers that only want to see what a worktree holds, and it keeps the
	// older worktree rendering.
	if strings.TrimSpace(base) == "" {
		diff, err := r.raw(ctx, "diff", "--no-ext-diff", "--")
		if err != nil {
			return Capture{}, err
		}
		cap.Diff = diff
		return cap, nil
	}

	// The content identity, built ONCE, before anything renders it.
	if cap.BaseTree, err = r.CommitTreeOf(ctx, base); err != nil {
		return Capture{}, err
	}
	if len(cap.Paths) == 0 {
		// No work. The candidate's tree IS the base's tree, which is a
		// measurement of "produced nothing" rather than an inference from an
		// empty diff.
		cap.Tree = cap.BaseTree
		return cap, nil
	}
	if cap.Tree, err = r.CanonicalTree(ctx, base, cap.Paths); err != nil {
		return Capture{}, err
	}

	// The boundary decision was made against a MUTABLE worktree, before T
	// existed. Now that the content is frozen, the same decision is re-asked of
	// it: a path could have changed between classification and the tree build,
	// leaving a capture whose exclusion metadata describes bytes the reviewer
	// will never see. Failing is the only honest answer -- silently rebuilding
	// or reinterpreting T would make the boundary describe whichever read won.
	if err := r.validateBoundaryAgainstTree(ctx, base, cap.Tree, want); err != nil {
		return Capture{}, err
	}

	// The review representation, rendered FROM the tree, through the ONE
	// renderer any later comparison must also use.
	diff, err := r.RenderCandidateDiff(ctx, base, cap.Tree)
	if err != nil {
		return Capture{}, err
	}
	cap.Diff = diff
	return cap, nil
}

func keptWhy(isNew, intended bool) string {
	switch {
	case intended:
		return "the plan names this path as an intended output"
	case !isNew:
		return "a tracked binary the candidate modified"
	}
	return "kept"
}

func (r Repo) existsAt(ctx context.Context, base, path string) bool {
	if strings.TrimSpace(base) == "" {
		return false
	}
	_, err := r.readOnly(ctx, r.Root, "cat-file", "-e", strings.TrimSpace(base)+":"+path)
	return err == nil
}

func (r Repo) sizeOf(path string) int64 {
	if fi, err := os.Stat(filepath.Join(r.Root, filepath.FromSlash(path))); err == nil {
		return fi.Size()
	}
	return 0
}

// Describe renders the artefacts for an event or a review packet.
func Describe(as []Artifact) string {
	var out []string
	for _, a := range as {
		out = append(out, fmt.Sprintf("%s (%s, %d bytes)", a.Path, a.Class, a.Size))
	}
	return strings.Join(out, "; ")
}

// refuseNewGitlinks refuses a candidate that holds a nested repository: a
// gitlink the worker added (intent-to-add records an embedded repository as
// one) or changed, or any gitlink -- the base's own included -- whose path in
// the worktree holds a .git that git could look inside.
//
// Looking inside means running git THERE, with configuration this profile
// never inspected: status and diff do it unless --ignore-submodules=all, and
// `add` does it with no flag to stop it (measured on git 2.43). And a skipped
// gitlink would silently fall out of the captured paths, so the reviewer would
// never see it. A refusal says so instead (independent review, B2). The index
// listing reads the index and the .git test reads the filesystem; neither runs
// anything inside the nested repository.
func (r Repo) refuseNewGitlinks(ctx context.Context, base string) error {
	out, err := r.raw(ctx, "ls-files", "--stage", "-z")
	if err != nil {
		return err
	}
	// Every index entry by its exact path, parsed on record boundaries: a
	// pathname may contain a tab, so no substring of the listing may stand in
	// for an entry (independent final-delta review, F1).
	type entry struct{ mode, sha, stage string }
	index := map[string][]entry{}
	for _, record := range strings.Split(strings.TrimSuffix(out, "\x00"), "\x00") {
		meta, path, ok := strings.Cut(record, "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 3 {
			if record == "" {
				continue
			}
			return fmt.Errorf("malformed ls-files --stage record: %q", record)
		}
		index[path] = append(index[path], entry{fields[0], fields[1], fields[2]})
		if fields[0] != "160000" {
			continue
		}
		if _, err := os.Lstat(filepath.Join(r.Root, filepath.FromSlash(path), ".git")); err == nil {
			return fmt.Errorf("%s: the gitlink %q holds a repository in the worktree; "+
				"trusted git does not look inside one", ProfileRefused, path)
		}
		if b := strings.TrimSpace(base); b != "" {
			at, err := r.envOutput(ctx, []string{"GIT_LITERAL_PATHSPECS=1"}, "ls-tree", "-z", b, "--", path)
			if err == nil && strings.HasPrefix(at, "160000 commit "+fields[1]+"\t") {
				continue // the base's own gitlink, unchanged
			}
		}
		return fmt.Errorf("%s: the candidate holds a nested repository at %q; "+
			"trusted git does not look inside one, so its content cannot be captured for review", ProfileRefused, path)
	}
	// And the other direction: a gitlink the base holds that the candidate
	// removed or changed. Status and diff ignore gitlinks, so a removal would
	// vanish from the captured paths while the canonical tree silently kept the
	// base's gitlink (independent review cycle 2, N-b).
	if b := strings.TrimSpace(base); b != "" {
		tree, err := r.raw(ctx, "ls-tree", "-r", "-z", "--full-tree", b)
		if err != nil {
			return err
		}
		for _, record := range strings.Split(strings.TrimSuffix(tree, "\x00"), "\x00") {
			meta, path, ok := strings.Cut(record, "\t")
			fields := strings.Fields(meta)
			if !ok || len(fields) != 3 || fields[0] != "160000" {
				continue
			}
			if e := index[path]; len(e) != 1 || e[0] != (entry{"160000", fields[2], "0"}) {
				return fmt.Errorf("%s: the candidate removed or changed the base's gitlink %q; "+
					"trusted git does not look at gitlinks, so that change cannot be captured for review", ProfileRefused, path)
			}
		}
	}
	return nil
}

// splitZ splits NUL-terminated git output into its records.
func splitZ(out string) []string {
	if out == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
}

// refuseFilterAttributesAt refuses a checkout of base when any of its paths
// would be smudged through a content filter (RULING-319).
func (r Repo) refuseFilterAttributesAt(ctx context.Context, base string) error {
	names, err := r.raw(ctx, "ls-tree", "-r", "-z", "--name-only", "--full-tree", base)
	if err != nil {
		return err
	}
	return r.refuseFilterAttributes(ctx, base, splitZ(names))
}

// refuseFilterAttributes refuses when any path selects a content filter.
//
// Ordinary git would pass such a path's content through the filter's
// program; the trusted profile runs no such program, so the bytes it stored
// or compared would not be the ones git stores. It does not supply an empty
// driver in place of a missing one: a selected filter is a refusal, wherever
// its driver is (or is not) configured (Codex P1 on #297, RULING-319).
//
// check-attr only looks attributes up; it runs nothing. With source it reads
// the attributes a checkout of that tree would apply; without, the ones add
// and diff apply to the worktree.
func (r Repo) refuseFilterAttributes(ctx context.Context, source string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	args := []string{"check-attr", "-z", "--stdin"}
	if source != "" {
		args = append(args, "--source="+source)
	}
	out, err := r.stdinOutput(ctx, nil, strings.Join(paths, "\x00")+"\x00", append(args, "filter")...)
	if err != nil {
		return fmt.Errorf("%s: the content filters of the candidate's paths cannot be evaluated: %v", ProfileRefused, err)
	}
	return filterAttributesRefusal(out, paths)
}

// filterAttributesRefusal reads check-attr -z output -- path, attribute,
// value, each NUL-terminated, one triple per path in input order -- and
// refuses a selected filter, or output it cannot match to its input.
func filterAttributesRefusal(out string, paths []string) error {
	fields := strings.Split(out, "\x00")
	if len(fields) != 3*len(paths)+1 || fields[len(fields)-1] != "" {
		return fmt.Errorf("%s: the content-filter query answered %d field(s) for %d path(s)", ProfileRefused, len(fields)-1, len(paths))
	}
	for i, p := range paths {
		path, attr, value := fields[3*i], fields[3*i+1], fields[3*i+2]
		if path != p || attr != "filter" {
			return fmt.Errorf("%s: the content-filter query answered %q/%q for %q", ProfileRefused, path, attr, p)
		}
		if value != "unspecified" && value != "unset" {
			return fmt.Errorf("%s: %q selects the content filter %q, which the trusted git profile does not run; "+
				"its content cannot be captured as git would store it", ProfileRefused, p, value)
		}
	}
	return nil
}
