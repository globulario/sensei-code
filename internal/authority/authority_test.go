package authority

import "testing"

func TestLevelString(t *testing.T) {
	cases := map[Level]string{Execution: "execution", Architectural: "architectural", Human: "human"}
	for got, want := range cases {
		if got.String() != want {
			t.Fatalf("%v: got %q want %q", got, got.String(), want)
		}
	}
}

func TestRequiresHuman(t *testing.T) {
	if !(Decision{Level: Human}).RequiresHuman() {
		t.Fatal("human decision should require escalation")
	}
	if (Decision{Level: Architectural}).RequiresHuman() {
		t.Fatal("architectural decision must not require human")
	}
}
