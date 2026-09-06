// Package ghwebhook authenticates GitHub webhook deliveries and hands the
// authenticated facts to a sink.
//
// It is TRANSPORT. The whole of what this package establishes is:
//
//	GitHub delivered these exact bytes
//
// and the whole of what it does NOT establish, however valid the signature:
//
//	Dave authorized an objective
//	the sender is an architect
//	the sender is a reviewer
//	the comment should execute anything
//
// Those four are the reason this package has no path to the workflow engine.
// It imports no engine, no resolver and no runner, and the import boundary is
// pinned by a test rather than left to discipline — see boundary_test.go. A
// signed webhook is transport truth, not objective authority, and the cheapest
// way for that distinction to be lost is for something here to acquire an
// engine handle "just to notify it".
//
// Two listeners, two authentication regimes, deliberately not one:
//
//	MCP control surface   Bearer credential   control.Endpoint
//	GitHub webhook        HMAC-SHA256         ghwebhook.Endpoint
//
// They share no socket, no handler, no secret and no code. A single surface
// holding both would eventually authenticate one regime's request with the
// other's rule, and the failure would be a silent authorization bypass rather
// than an error.
//
// # Secret handling
//
// Only the PATH of the shared secret is configuration. The content is read
// through loadSecret, held in the Server, used to compute an HMAC, and never
// returned, logged, printed, embedded in an error, placed in argv, or written
// to any evidence record. Neither the expected nor the received MAC appears in
// any message: an attacker who can read error text must learn nothing that
// brings them closer to forging one.
package ghwebhook
