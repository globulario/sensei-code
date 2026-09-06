package ghbridge

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/globulario/sensei-code/internal/roles"
)

const (
	architectureRequestMarker  = "[sensei-code:architecture-request]"
	architectureResponseMarker = "[sensei-code:architecture]"
)

var architectureRequestID = regexp.MustCompile(`^[0-9A-Za-z_.:-]{1,128}$`)

// ArchitectureRequest asks the remote architect one question whose identity is
// already owned by the workflow. Prompt is presentation and context; Binding is
// the subject. The transport never derives the latter back out of the former.
type ArchitectureRequest struct {
	Binding   roles.ArchitectureBinding
	RequestID string
	Prompt    string
}

func (r ArchitectureRequest) Validate() error {
	if !r.Binding.Valid() {
		return fmt.Errorf("architecture request has no complete objective/world binding: %+v", r.Binding)
	}
	if !architectureRequestID.MatchString(strings.TrimSpace(r.RequestID)) {
		return fmt.Errorf("architecture request id is missing or malformed: %q", r.RequestID)
	}
	if strings.TrimSpace(r.Prompt) == "" {
		return errors.New("architecture request carries no prompt")
	}
	return nil
}

func (r ArchitectureRequest) Marker() (string, error) {
	if err := r.Validate(); err != nil {
		return "", err
	}
	b := strings.Builder{}
	b.WriteString(architectureRequestMarker + "\n")
	b.WriteString("kind=architecture\n")
	fmt.Fprintf(&b, "task=%s\n", r.Binding.TaskID)
	fmt.Fprintf(&b, "request=%s\n", r.RequestID)
	fmt.Fprintf(&b, "objective_digest=%s\n", r.Binding.ObjectiveDigest)
	fmt.Fprintf(&b, "base=%s\n", r.Binding.BaseSHA)
	fmt.Fprintf(&b, "graph_build_commit=%s\n", r.Binding.GraphBuildCommit)
	b.WriteString("\n")
	b.WriteString(r.Prompt)
	return b.String(), nil
}

// ArchitectureResponse carries only the architect's answer. The marker says
// which exact question it answers; Body remains the architecture JSON contract
// the workflow already parses, so there is no second decision representation.
type ArchitectureResponse struct {
	Binding   roles.ArchitectureBinding
	RequestID string
	Body      string
	Author    string
	AuthorID  int64
}

func (r ArchitectureResponse) Validate() error {
	if !r.Binding.Valid() {
		return fmt.Errorf("architecture response has no complete objective/world binding: %+v", r.Binding)
	}
	if !architectureRequestID.MatchString(strings.TrimSpace(r.RequestID)) {
		return fmt.Errorf("architecture response request id is missing or malformed: %q", r.RequestID)
	}
	if strings.TrimSpace(r.Body) == "" {
		return errors.New("architecture response carries no answer")
	}
	return nil
}

func (r ArchitectureResponse) Marker() (string, error) {
	if err := r.Validate(); err != nil {
		return "", err
	}
	b := strings.Builder{}
	b.WriteString(architectureResponseMarker + "\n")
	fmt.Fprintf(&b, "task=%s\n", r.Binding.TaskID)
	fmt.Fprintf(&b, "request=%s\n", r.RequestID)
	fmt.Fprintf(&b, "objective_digest=%s\n", r.Binding.ObjectiveDigest)
	fmt.Fprintf(&b, "base=%s\n", r.Binding.BaseSHA)
	fmt.Fprintf(&b, "graph_build_commit=%s\n", r.Binding.GraphBuildCommit)
	b.WriteString("\n")
	b.WriteString(r.Body)
	return b.String(), nil
}

func (r ArchitectureResponse) Answers(q ArchitectureRequest) bool {
	return r.RequestID == q.RequestID && r.Binding.Same(q.Binding)
}

// parseArchitectureEnvelope is strict about the identity header. Duplicate or
// unknown fields are refused rather than ignored: ambiguity in presentation
// must never decide which objective/world an answer belongs to.
func parseArchitectureEnvelope(body, marker string, request bool) (roles.ArchitectureBinding, string, string, error) {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	if !strings.HasPrefix(body, marker+"\n") {
		return roles.ArchitectureBinding{}, "", "", errors.New("architecture marker missing")
	}
	rest := strings.TrimPrefix(body, marker+"\n")
	header, payload, ok := strings.Cut(rest, "\n\n")
	if !ok {
		return roles.ArchitectureBinding{}, "", "", errors.New("architecture envelope has no payload boundary")
	}
	fields := map[string]string{}
	for _, line := range strings.Split(header, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok || key == "" || value == "" {
			return roles.ArchitectureBinding{}, "", "", fmt.Errorf("malformed architecture header line %q", line)
		}
		if _, duplicate := fields[key]; duplicate {
			return roles.ArchitectureBinding{}, "", "", fmt.Errorf("duplicate architecture header %q", key)
		}
		fields[key] = value
	}
	allowed := map[string]bool{
		"task": true, "request": true, "objective_digest": true,
		"base": true, "graph_build_commit": true,
	}
	if request {
		allowed["kind"] = true
		if fields["kind"] != "architecture" {
			return roles.ArchitectureBinding{}, "", "", fmt.Errorf("architecture request kind = %q", fields["kind"])
		}
	}
	for key := range fields {
		if !allowed[key] {
			return roles.ArchitectureBinding{}, "", "", fmt.Errorf("unknown architecture header %q", key)
		}
	}
	for key := range allowed {
		if fields[key] == "" {
			return roles.ArchitectureBinding{}, "", "", fmt.Errorf("architecture header %q is missing", key)
		}
	}
	binding := roles.ArchitectureBinding{
		TaskID:           fields["task"],
		ObjectiveDigest:  fields["objective_digest"],
		BaseSHA:          fields["base"],
		GraphBuildCommit: fields["graph_build_commit"],
	}
	if !binding.Valid() {
		return roles.ArchitectureBinding{}, "", "", fmt.Errorf("architecture binding is malformed: %+v", binding)
	}
	id := fields["request"]
	if !architectureRequestID.MatchString(id) {
		return roles.ArchitectureBinding{}, "", "", fmt.Errorf("architecture request id is malformed: %q", id)
	}
	if strings.TrimSpace(payload) == "" {
		return roles.ArchitectureBinding{}, "", "", errors.New("architecture envelope payload is empty")
	}
	return binding, id, payload, nil
}

func ParseArchitectureRequest(body string) (ArchitectureRequest, bool) {
	if !strings.HasPrefix(strings.ReplaceAll(body, "\r\n", "\n"), architectureRequestMarker+"\n") {
		return ArchitectureRequest{}, false
	}
	binding, id, prompt, err := parseArchitectureEnvelope(body, architectureRequestMarker, true)
	if err != nil {
		return ArchitectureRequest{}, false
	}
	return ArchitectureRequest{Binding: binding, RequestID: id, Prompt: prompt}, true
}

func ParseArchitectureResponse(body string) (ArchitectureResponse, bool) {
	if !strings.HasPrefix(strings.ReplaceAll(body, "\r\n", "\n"), architectureResponseMarker+"\n") {
		return ArchitectureResponse{}, false
	}
	binding, id, answer, err := parseArchitectureEnvelope(body, architectureResponseMarker, false)
	if err != nil {
		return ArchitectureResponse{}, false
	}
	return ArchitectureResponse{Binding: binding, RequestID: id, Body: answer}, true
}
