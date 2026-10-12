package workflow

// The N3 witnesses that need no Linux kernel facility: the default-off launch
// and configuration. They compile and run on every platform; the
// Linux-dependent witnesses are in execservice_test.go.

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/agent"
	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/roles"
)

// The default is unchanged: with the profile off nothing is started and the
// request the implementer receives is exactly the one it always was.
func TestN3TheDefaultLaunchIsUnchanged(t *testing.T) {
	e := &Engine{Config: config.Default()}
	if e.Config.ExecBinding.Enabled {
		t.Fatal("the execution profile is on by default")
	}
	svc, err := e.startImplementerExec(context.Background(), "task-1", t.TempDir(), agent.CLI{Name: "claude", Command: "claude"})
	if err != nil || svc != nil {
		t.Fatalf("a service started with the profile off: %v", err)
	}
	req := agent.Request{Role: roles.Implementer, TaskID: "task-1", Prompt: "p"}
	if got := svc.bind(req); got.Exec != nil || got.Prompt != "p" {
		t.Fatalf("the default request was changed: %+v", got)
	}
	svc.stop()
}

// F12. A default-off configuration is saved exactly as before the profile
// existed: no exec_binding key at all. A configured one round-trips.
func TestN3ADefaultOffConfigurationSavesNoExecBinding(t *testing.T) {
	repo := t.TempDir()
	if err := config.Save(repo, config.Default()); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(config.Path(repo))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "exec_binding") {
		t.Fatalf("the default configuration serializes the execution profile:\n%s", body)
	}
	on := config.Default()
	on.ExecBinding = config.ExecBinding{Enabled: true, Confinement: config.Confinement{Profile: "d2", Command: "/usr/bin/d2"}}
	if err := config.Save(repo, on); err != nil {
		t.Fatal(err)
	}
	got, err := config.Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !got.ExecBinding.Enabled || got.ExecBinding.Confinement.Command != "/usr/bin/d2" {
		t.Fatalf("a configured profile did not round-trip: %+v", got.ExecBinding)
	}
}
