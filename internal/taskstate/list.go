package taskstate

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// List returns the task ids this repository holds canonical state for, in
// stable order.
//
// It reads the same directory Save writes and Load reads, deliberately: a
// second index of which tasks exist would be a second answer to that question,
// and the two would disagree the first time a write failed halfway. The
// directory IS the index.
//
// A repository with no tasks and a repository that has never run are the same
// answer here — an empty list, no error. That is not typed absence being
// flattened: nothing has been claimed about any task, and a caller asking about
// a specific one still gets Load's found/not-found. It is only the enumeration
// that is legitimately empty.
func List(repoRoot string) ([]string, error) {
	dir := filepath.Join(repoRoot, ".sensei-code", "tasks")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var ids []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		ids = append(ids, strings.TrimSuffix(name, ".json"))
	}
	sort.Strings(ids)
	return ids, nil
}
