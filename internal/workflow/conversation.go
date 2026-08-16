package workflow

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/globulario/sensei-code/internal/sensei"
)

// PrepareArchitectConversation builds the live, read-only context for a direct
// conversation with the ChatGPT architect. It deliberately does not start a
// candidate workflow. Execution remains an explicit /run boundary in the TUI.
func (e *Engine) PrepareArchitectConversation(ctx context.Context, message string) (string, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		return "", errors.New("architect message is empty")
	}
	if !e.Config.Permissions.ReadRepository {
		return "", errors.New("repository read capability is not granted")
	}

	sc, err := sensei.Start(ctx, e.Repo.Root, e.Config.Sensei.Command, e.Config.Sensei.Args)
	if err != nil {
		return "", fmt.Errorf("start Sensei: %w", err)
	}
	defer sc.Close()

	workspaceStatus, err := sc.CallTool("sensei_workspace_status", map[string]any{"repo": e.Repo.Root})
	if err != nil {
		return "", fmt.Errorf("Sensei workspace status: %w", err)
	}
	preflight, err := sc.CallTool("awareness_preflight", map[string]any{
		"task":  message,
		"files": []string{},
		"mode":  "compact",
	})
	if err != nil {
		return "", fmt.Errorf("Sensei preflight: %w", err)
	}

	return architectConversationPrompt(message, firstText(workspaceStatus), firstText(preflight)), nil
}

func architectConversationPrompt(message, workspaceStatus, preflight string) string {
	return fmt.Sprintf(`You are ChatGPT, the first-version architectural authority for Sensei Code, speaking directly with the human owner of this repository.

This is a human architectural conversation, not a machine handoff. Answer naturally and with enough depth to be genuinely useful. Be precise, concrete, and technically rich. Explain the evidence, architectural consequences, tradeoffs, and your recommendation when they matter. Do not compress the answer into a terse status line, and do not return JSON unless the human explicitly asks for JSON.

You may inspect the repository using read-only tools when that improves the answer. Do not edit files, create commits, push, deploy, or perform implementation work in this conversational turn. Sensei remains the governance authority: do not weaken, reinterpret, or invent its contracts. Treat the supplied Sensei evidence as live evidence, not decorative prompt text.

Routine architectural judgment is yours to make. Do not ask the human for ordinary implementation choices or permission. If and only if the discussion reaches a genuinely human-owned boundary such as product intent, a new invariant, an externally meaningful contract, or a trust-policy choice that existing authority cannot settle, explain that boundary clearly and offer at most three concrete choices with a recommendation.

The human may later use /run to cross from discussion into governed execution. Until then, stay in design/review/advice mode.

HUMAN MESSAGE:
%s

LIVE SENSEI WORKSPACE AUTHORITY:
%s

LIVE SENSEI PREFLIGHT:
%s`, message, workspaceStatus, preflight)
}
