package proofbench

import "testing"

func TestTaskFlagsAreAClosedVocabularyReadByMembership(t *testing.T) {
	if err := (Task{ID: "t", Flags: []string{"NEGATIVE_CONDITION", "OWNERSHIP_BOUNDARY"}}).ValidateFlags(); err != nil {
		t.Fatal(err)
	}
	if err := (Task{ID: "t", Flags: []string{"negative_condition"}}).ValidateFlags(); err == nil {
		t.Fatal("a flag outside the vocabulary must be refused, not read as something")
	}
	if err := (Task{ID: "t"}).ValidateFlags(); err != nil {
		t.Fatal("no flags is unlabelled, and valid")
	}
}
