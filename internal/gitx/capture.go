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
	if _, err := r.output(ctx, "add", "--intent-to-add", "--", "."); err != nil {
		return Capture{}, err
	}
	want := map[string]bool{}
	for _, p := range intended {
		want[filepath.ToSlash(strings.TrimPrefix(strings.TrimSpace(p), "./"))] = true
	}
	numstatArgs := []string{"diff", "--no-ext-diff", "--numstat"}
	if b := strings.TrimSpace(base); b != "" {
		numstatArgs = append(numstatArgs, b)
	}
	numstat, err := r.raw(ctx, append(numstatArgs, "--")...)
	if err != nil {
		return Capture{}, err
	}
	var cap Capture
	for _, line := range strings.Split(numstat, "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), "\t", 3)
		if len(parts) != 3 {
			continue
		}
		added, deleted, path := parts[0], parts[1], parts[2]
		isNew := !r.existsAt(ctx, base, path)
		size := r.sizeOf(path)
		switch {
		case added == "-" && deleted == "-":
			a := Artifact{Path: path, Size: size, Class: "binary"}
			if isNew && !want[path] {
				a.Excluded, a.Reason = true, "a new binary the plan did not name as an intended output"
				if _, err := r.output(ctx, "reset", "-q", "--", path); err != nil {
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
				if _, err := r.output(ctx, "reset", "-q", "--", path); err != nil {
					return Capture{}, fmt.Errorf("exclude %s from the candidate: %w", path, err)
				}
				cap.Excluded = append(cap.Excluded, a)
			}
		}
	}
	sort.Slice(cap.Excluded, func(i, j int) bool { return cap.Excluded[i].Path < cap.Excluded[j].Path })
	// Text only. `--binary` would inline every kept binary as a patch, which is
	// exactly the payload nobody can judge and every transport refuses.
	diffArgs := []string{"diff", "--no-ext-diff"}
	if b := strings.TrimSpace(base); b != "" {
		diffArgs = append(diffArgs, b)
	}
	diff, err := r.raw(ctx, append(diffArgs, "--")...)
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
