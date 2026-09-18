package reviewartifact

import (
	"os"
	"testing"
)

// TestShowContract prints the contract a published request now carries, so the
// exact reviewer-facing text can be reviewed as text rather than inferred from
// assertions. It asserts nothing; run it with SHOW_CONTRACT=1.
func TestShowContract(t *testing.T) {
	if os.Getenv("SHOW_CONTRACT") == "" {
		t.Skip("set SHOW_CONTRACT=1 to print the reviewer-facing contract")
	}
	c, err := ResponseContract(Artifact{
		ReviewerProvider: "chatgpt",
		TaskID:           "task-1789608707905891045",
		RequestID:        "r-73f20755c254af95",
		BaseSHA:          "5883f9782ae3b9aac3d2180da500c4c45e6cfa42",
		CandidateDigest:  "591fb6cd212f9380063746a09b16aca5d212bd4ac666ec41b26165701d19c8d0",
		CandidateTree:    "5364963a4544bcc52288d631518af6c920b81f58",
		ReviewCommit:     "90aed98d1fc80d158fee66424badf1c099731cc3",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("\n%s", c)
}
