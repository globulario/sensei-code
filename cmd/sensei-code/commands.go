package main

// The public command surface, declared once.
//
// `observe` and `audit-repair` shipped in the dispatch switch and never
// appeared in `sensei-code help`. They are the two newest capabilities and the
// ones a proof campaign depends on, so the tool's most interesting behaviour
// was reachable only by reading the source. `setup`, `mcp` and `routine-scan`
// were missing the same way.
//
// A command a user cannot discover is not a public command, whatever the switch
// says. So the table below is the single statement of what is public, help is
// rendered from it, and TestEveryDispatchedCommandIsAccountedFor parses the
// dispatch switch and fails on anything that is neither listed here nor
// explicitly classified as an alias or hidden.
//
// This is a truthfulness correction, not a CLI redesign. No command's behaviour
// changes and none is added or removed.

import (
	"fmt"
	"sort"
	"strings"
)

// command is one entry on the public surface.
type command struct {
	Name string
	// Args is the argument shape shown after the name, if any.
	Args string
	// Summary is one line. It says what the command does, not how.
	Summary string
}

// publicCommands is every command `sensei-code help` advertises.
//
// Order is presentation order: the two things a newcomer does first, then the
// governed lanes, then configuration and diagnostics.
var publicCommands = []command{
	{Name: "run", Args: "--task \"...\"", Summary: "run one governed task headlessly (same engine as /run)"},
	{Name: "observe", Args: "--task \"...\"", Summary: "read-only audit: report findings, admit nothing, change no file"},
	{Name: "audit-repair", Args: "--task \"...\"", Summary: "observe, then open governed repair work for what was established"},
	{Name: "init", Summary: "create the local capability/provider configuration"},
	{Name: "setup", Summary: "check this repository's readiness and apply the fixes it names"},
	{Name: "doctor", Summary: "verify Git, providers, and the Sensei MCP surface"},
	{Name: "providers", Summary: "show provider installation/authentication state"},
	{Name: "login", Summary: "connect a provider using its native authentication"},
	{Name: "logout", Summary: "disconnect a provider using its native authentication"},
	{Name: "context", Summary: "build an assisted context packet from live Sensei evidence"},
	{Name: "handoff", Summary: "bind agent continuity to an exact context packet"},
	// `mcp` configures each AGENT's route to Sensei. `control` is the opposite
	// direction: a capable agent reaching in to hold a role here. The summary
	// below used to read "serve Sensei Code's own MCP surface", which described
	// neither -- it named the thing `control` actually does while pointing at
	// the command that configures clients.
	{Name: "mcp", Summary: "configure each agent's access to the Sensei MCP server"},
	{Name: "control", Summary: "serve this instance's remote control surface over loopback"},
	{Name: "submit", Args: "--task \"...\"", Summary: "place one objective into the running control process"},
	{Name: "routine-scan", Summary: "classify tracked files by how routine a change to them would be"},
	{Name: "help", Summary: "show this help"},
}

// commandAliases are second spellings of a listed command. They dispatch and
// are deliberately not advertised, because two names for one thing in a help
// list reads as two capabilities.
var commandAliases = map[string]string{
	"accounts": "providers",
	"--help":   "help",
	"-h":       "help",
}

// hiddenCommands are dispatched and deliberately absent from help.
//
// Empty, and that is the honest current state: every command this binary
// dispatches is one a user is meant to find. An entry here is a claim that a
// command is internal, and it has to be argued for rather than used to silence
// the test.
var hiddenCommands = map[string]string{}

// publicCommandNames is the advertised set.
func publicCommandNames() map[string]bool {
	out := make(map[string]bool, len(publicCommands))
	for _, c := range publicCommands {
		out[c.Name] = true
	}
	return out
}

// renderCommands lays the table out for help, aligned on the widest usage.
func renderCommands() string {
	usage := make([]string, len(publicCommands))
	width := 0
	for i, c := range publicCommands {
		u := "sensei-code " + c.Name
		if c.Args != "" {
			u += " " + c.Args
		}
		usage[i] = u
		if len(u) > width {
			width = len(u)
		}
	}
	var b strings.Builder
	for i, c := range publicCommands {
		b.WriteString(fmt.Sprintf("  %-*s  %s\n", width, usage[i], c.Summary))
	}
	return strings.TrimRight(b.String(), "\n")
}

// sortedNames is a stable rendering of a name set, for test failure messages.
func sortedNames(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
