package processx

import (
	"os"
	"strings"
	"testing"
)

func TestEnvironRemovesOverridingCredentials(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-should-not-reach-the-agent")
	t.Setenv("KEEP_ME", "yes")

	env := Environ([]string{"ANTHROPIC_API_KEY"})
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "ANTHROPIC_API_KEY=") {
		t.Fatal("an overriding credential reached the agent environment")
	}
	if !strings.Contains(joined, "KEEP_ME=yes") {
		t.Fatal("unrelated environment was dropped")
	}
}

func TestEnvironWithNothingToUnsetIsTheProcessEnvironment(t *testing.T) {
	if len(Environ(nil)) != len(os.Environ()) {
		t.Fatal("Environ(nil) must be the process environment unchanged")
	}
}
