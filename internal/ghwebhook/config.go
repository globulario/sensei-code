package ghwebhook

import (
	"fmt"
	"net"
	"strings"
)

// Config is the operator's webhook ingress configuration, exactly as supplied.
//
// Every binding is STATED. None is inferred from the working directory, a git
// remote, the App configuration used elsewhere in this process, or the payload
// itself — least of all the payload, which is the thing being checked. An
// ingress that read its expected repository out of the delivery would accept
// every repository.
//
// Only SecretFile's PATH is configuration. The secret's content never is.
type Config struct {
	// Addr is the loopback address to bind. Required, and loopback-only: the
	// public edge is a reverse proxy in front of this, deliberately outside
	// this process.
	Addr string
	// SecretFile is the path to the HMAC shared secret.
	SecretFile string
	// InstallationID is the one App installation whose deliveries are accepted.
	InstallationID int64
	// RepositoryID is the numeric repository id. Checked alongside the name
	// because a name can be transferred to a different repository and a
	// numeric id cannot.
	RepositoryID int64
	// Repository is the expected owner/name.
	Repository string
}

// Selected reports whether the operator asked for webhook ingress at all.
//
// ANY field means yes, for the reason AppConfig.Selected has: keying selection
// on one field means the others, with that one merely forgotten, silently do
// nothing. An operator who configured a secret file and a repository has
// plainly asked for this, and starting without it — with no listener and no
// complaint — is the failure. Intent is expressed by configuring anything;
// completeness is then required rather than assumed.
func (c Config) Selected() bool {
	return strings.TrimSpace(c.Addr) != "" ||
		strings.TrimSpace(c.SecretFile) != "" ||
		c.InstallationID != 0 ||
		c.RepositoryID != 0 ||
		strings.TrimSpace(c.Repository) != ""
}

// Missing names the fields still required for the selected ingress.
func (c Config) Missing() []string {
	var missing []string
	if strings.TrimSpace(c.Addr) == "" {
		missing = append(missing, "listen address")
	}
	if strings.TrimSpace(c.SecretFile) == "" {
		missing = append(missing, "secret file path")
	}
	if c.InstallationID == 0 {
		missing = append(missing, "installation id")
	}
	if c.RepositoryID == 0 {
		missing = append(missing, "repository id")
	}
	if strings.TrimSpace(c.Repository) == "" {
		missing = append(missing, "repository full name")
	}
	return missing
}

// Server builds the webhook ingress, or refuses.
//
// Three outcomes and no fourth:
//
//	nothing configured    -> (nil, nil)   no listener; the process runs as before
//	partly configured     -> (nil, error) startup refusal
//	completely configured -> (server, nil)
//
// The middle outcome is a refusal to START, not a quieter ingress than the one
// asked for. A half-configured webhook that came up anyway would be a public
// route whose bindings nobody checked.
//
// Everything that can be decided before a socket exists is decided here: the
// secret is read, its mode is checked, and the address is proved loopback. An
// operator learns about a wrong path or a world-readable key at startup rather
// than on GitHub's first delivery.
func (c Config) Server(sink Sink) (*Server, error) {
	if !c.Selected() {
		return nil, nil
	}
	if missing := c.Missing(); len(missing) > 0 {
		return nil, fmt.Errorf(
			"github webhook ingress was selected but is missing: %s; refusing to start a public route with bindings nobody stated",
			strings.Join(missing, ", "))
	}
	if sink == nil {
		return nil, fmt.Errorf("github webhook ingress needs a sink for authenticated deliveries")
	}
	addr := strings.TrimSpace(c.Addr)
	if err := requireLoopback(addr); err != nil {
		return nil, err
	}
	secret, err := loadSecret(c.SecretFile)
	if err != nil {
		return nil, err
	}
	return &Server{
		addr:           addr,
		secret:         secret,
		installationID: c.InstallationID,
		repositoryID:   c.RepositoryID,
		repository:     strings.TrimSpace(c.Repository),
		sink:           sink,
	}, nil
}

// requireLoopback refuses an address that is not, or cannot be shown to be,
// the local machine.
//
// Deliberately this package's own copy rather than control's. The two listeners
// are independent by design — different secret, different regime, different
// socket — and a shared helper would be the first thread tying them together.
// The predicate is small, pure, and the messages name the surface that refused.
//
// A name is resolved and EVERY answer must be loopback: "localhost" is a line
// in a file anything on the machine can write, so accepting it on trust would
// make the boundary a property of /etc/hosts. An empty host means every
// interface and is refused by name, because binding this surface publicly —
// with the reverse proxy then no longer the only way in — is the specific
// mistake worth catching.
func requireLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("the github webhook address %q is not host:port: %w", addr, err)
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return fmt.Errorf("refusing to serve the github webhook on %q, which binds every interface; use 127.0.0.1", addr)
	}
	if ip := net.ParseIP(host); ip != nil {
		if !ip.IsLoopback() {
			return fmt.Errorf("refusing to serve the github webhook on %s, which is not loopback; the public edge is the reverse proxy, not this socket", host)
		}
		return nil
	}
	resolved, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("refusing to serve the github webhook on %q, which does not resolve: %w", host, err)
	}
	if len(resolved) == 0 {
		return fmt.Errorf("refusing to serve the github webhook on %q, which resolves to nothing", host)
	}
	for _, ip := range resolved {
		if !ip.IsLoopback() {
			return fmt.Errorf("refusing to serve the github webhook on %q, which resolves to %s and is not loopback", host, ip)
		}
	}
	return nil
}
