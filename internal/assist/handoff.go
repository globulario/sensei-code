package assist

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

const HandoffVersion = 1

type HandoffPacket struct {
	Version       int       `json:"version"`
	TaskID        string    `json:"task_id"`
	Task          string    `json:"task"`
	FromAgent     string    `json:"from_agent"`
	ToAgent       string    `json:"to_agent"`
	BaseSHA       string    `json:"base_sha"`
	ContextDigest string    `json:"context_digest"`
	Summary       string    `json:"summary"`
	Decisions     []string  `json:"decisions,omitempty"`
	ChangedFiles  []string  `json:"changed_files,omitempty"`
	Tests         []string  `json:"tests,omitempty"`
	OpenQuestions []string  `json:"open_questions,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	Authority     Authority `json:"authority"`
}

func NewHandoff(contextPacket ContextPacket, fromAgent, toAgent, summary string, decisions, changedFiles, tests, openQuestions []string, now time.Time) (HandoffPacket, error) {
	if err := contextPacket.Validate(); err != nil {
		return HandoffPacket{}, fmt.Errorf("context packet: %w", err)
	}
	digest, err := contextPacket.Digest()
	if err != nil {
		return HandoffPacket{}, err
	}
	fromAgent = strings.TrimSpace(fromAgent)
	toAgent = strings.TrimSpace(toAgent)
	summary = strings.TrimSpace(summary)
	if fromAgent == "" || toAgent == "" || summary == "" {
		return HandoffPacket{}, errors.New("handoff requires from agent, to agent, and summary")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	p := HandoffPacket{
		Version:       HandoffVersion,
		TaskID:        contextPacket.TaskID,
		Task:          contextPacket.Task,
		FromAgent:     fromAgent,
		ToAgent:       toAgent,
		BaseSHA:       contextPacket.BaseSHA,
		ContextDigest: digest,
		Summary:       summary,
		Decisions:     cleanStrings(decisions),
		ChangedFiles:  cleanFiles(changedFiles),
		Tests:         cleanStrings(tests),
		OpenQuestions: cleanStrings(openQuestions),
		CreatedAt:     now,
		Authority: Authority{
			Mode:      "assisted",
			Admission: "not-requested",
		},
	}
	return p, p.ValidateAgainst(contextPacket)
}

func (p HandoffPacket) ValidateAgainst(contextPacket ContextPacket) error {
	if p.Version != HandoffVersion {
		return fmt.Errorf("unsupported handoff packet version %d", p.Version)
	}
	if err := contextPacket.Validate(); err != nil {
		return err
	}
	digest, err := contextPacket.Digest()
	if err != nil {
		return err
	}
	if p.ContextDigest != digest {
		return errors.New("handoff context digest does not match the supplied context packet")
	}
	if p.TaskID != contextPacket.TaskID || p.Task != contextPacket.Task || p.BaseSHA != contextPacket.BaseSHA {
		return errors.New("handoff task identity does not match the supplied context packet")
	}
	if strings.TrimSpace(p.FromAgent) == "" || strings.TrimSpace(p.ToAgent) == "" || strings.TrimSpace(p.Summary) == "" {
		return errors.New("handoff requires from agent, to agent, and summary")
	}
	if p.Authority.Mode != "assisted" || p.Authority.Admission != "not-requested" {
		return errors.New("assisted handoff must not claim governed admission")
	}
	return nil
}

func (p HandoffPacket) Write(path string, contextPacket ContextPacket) error {
	if err := p.ValidateAgainst(contextPacket); err != nil {
		return err
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o600)
}

func cleanStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
