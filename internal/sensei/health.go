package sensei

// Why a Sensei call failed, as far as this process can witness it.
//
// A transport error is not a root cause. "awareness-graph backend is
// unreachable ... dial tcp 127.0.0.1:10199: connect: connection refused" is an
// accurate sentence about the socket and says nothing about WHY the socket is
// closed, so an operator reads it and debugs the wrong layer. During the first
// self-repair experiment the isolated stack died twice, both times because
// `sensei serve` lost a race starting oxigraph and exited, and both times the
// reported symptom named the transport.
//
// For an interactive run that costs a minute. For an unattended run it is the
// difference between failing loudly at minute forty and quietly burning the
// next five hours.
//
// # Deliberately only a witness
//
// This restarts nothing, retries nothing and recreates nothing. Recovery needs
// to know what a healthy environment IS, and that is a separate question from
// reporting what changed. Guessing at it here would hide the very failures this
// exists to surface.
//
// # Only what this process can actually see
//
// A client speaks MCP to a subprocess; it does not hold the graph's address and
// never speaks to oxigraph. So the chain below reports three things and does
// not pretend to more: when Sensei last answered, what the failure said, and
// whether the MCP subprocess is still alive. Anything about the store is quoted
// from Sensei's own words rather than observed.

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

// Health is what the client witnessed about its dependency.
type Health struct {
	// LastOK is when a tool call last succeeded, and which one. Zero if none
	// ever has, which is itself the finding: the environment was never working
	// rather than having stopped.
	LastOK   time.Time
	LastTool string
	// SubprocessAlive reports whether the MCP process is still running. A dead
	// subprocess and a live one that cannot reach the graph are different
	// failures with different fixes.
	SubprocessAlive bool
	// Stderr is what the subprocess last printed. Usually where the real reason
	// is, and usually discarded.
	Stderr string
}

// Causes is the failure classified by what this process can establish.
type Causes struct {
	Tool    string
	Err     error
	Health  Health
	Elapsed time.Duration
}

// Diagnose renders the causal chain, deepest observable cause last.
//
// Written for someone reading a log hours later with no memory of the run, so
// it states the time the environment was last known good rather than only that
// it is bad now.
func (c Causes) Diagnose() string {
	var b strings.Builder
	fmt.Fprintf(&b, "sensei tool %s failed: %v", c.Tool, c.Err)

	switch {
	case c.Health.LastOK.IsZero():
		b.WriteString("\n  no Sensei call has ever succeeded in this session, so the " +
			"environment was not working rather than having stopped")
	default:
		fmt.Fprintf(&b, "\n  Sensei last answered %s ago (%s), at %s",
			c.Elapsed.Round(time.Second), c.Health.LastTool,
			c.Health.LastOK.UTC().Format(time.RFC3339))
	}

	if c.Health.SubprocessAlive {
		b.WriteString("\n  the MCP subprocess is still running, so the fault is between it and the graph, " +
			"not a crashed client")
	} else {
		b.WriteString("\n  the MCP subprocess is NOT running; it exited rather than returning an error")
	}

	if s := strings.TrimSpace(c.Health.Stderr); s != "" {
		b.WriteString("\n  last words from the subprocess:")
		for _, line := range strings.Split(s, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				b.WriteString("\n    " + line)
			}
		}
	}
	// Sensei's own account of its backend, quoted rather than observed: this
	// process never speaks to the store.
	if reason := backendReason(c.Err); reason != "" {
		b.WriteString("\n  Sensei reported about its own backend: " + reason +
			"\n  (quoted from Sensei; this process does not observe the store directly)")
	}
	return b.String()
}

// backendReason lifts Sensei's statement about its store out of a transport error.
func backendReason(err error) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	for _, marker := range []string{"backend is unreachable", "backend unhealthy", "oxigraph"} {
		if i := strings.Index(text, marker); i >= 0 {
			rest := text[i:]
			if j := strings.Index(rest, ";"); j > 0 {
				rest = rest[:j]
			}
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

// lastOK records the moment of the last successful call.
type lastOK struct {
	at   atomic.Int64
	tool atomic.Value
}

func (l *lastOK) mark(tool string) {
	l.at.Store(time.Now().UnixNano())
	l.tool.Store(tool)
}

func (l *lastOK) read() (time.Time, string) {
	ns := l.at.Load()
	if ns == 0 {
		return time.Time{}, ""
	}
	tool, _ := l.tool.Load().(string)
	return time.Unix(0, ns), tool
}
