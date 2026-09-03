package taskstate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListReadsTheDirectoryThatIsTheIndex(t *testing.T) {
	root := t.TempDir()
	// A repository that has never run enumerates to nothing, and that is not an
	// error: nothing has been claimed about any task.
	ids, err := List(root)
	if err != nil {
		t.Fatalf("a repository with no tasks errored: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("got %v", ids)
	}

	for _, id := range []string{"task-b", "task-a"} {
		if err := (State{TaskID: id, Phase: Planning}).Save(root); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	// Not every file in the directory is a task record.
	dir := filepath.Join(root, ".sensei-code", "tasks")
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	ids, err = List(root)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(ids) != 2 || ids[0] != "task-a" || ids[1] != "task-b" {
		t.Fatalf("got %v, want [task-a task-b] in stable order", ids)
	}
}

// List and Load must agree about which tasks exist. Two answers to that
// question would disagree the first time a write failed halfway.
func TestEveryListedTaskLoads(t *testing.T) {
	root := t.TempDir()
	if err := (State{TaskID: "task-1", Phase: Accepted}).Save(root); err != nil {
		t.Fatalf("save: %v", err)
	}
	ids, err := List(root)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, id := range ids {
		if _, found, err := Load(root, id); err != nil || !found {
			t.Fatalf("List named %q and Load could not find it (found=%v err=%v)", id, found, err)
		}
	}
}
