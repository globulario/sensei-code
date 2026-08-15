package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/globulario/sensei-code/internal/assist"
	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/gitx"
	"github.com/globulario/sensei-code/internal/sensei"
)

func runContext(ctx context.Context, repo gitx.Repo, cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("context", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	task := fs.String("task", "", "task to brief through Sensei")
	taskID := fs.String("task-id", "", "durable task identity")
	filesCSV := fs.String("files", "", "comma-separated file hints")
	out := fs.String("out", "", "write the packet to this path instead of stdout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*task) == "" {
		return errors.New("context requires --task")
	}
	id := strings.TrimSpace(*taskID)
	if id == "" {
		id = "task-" + time.Now().UTC().Format("20060102T150405.000000000Z")
	}

	client, err := sensei.Start(ctx, repo.Root, cfg.Sensei.Command, cfg.Sensei.Args)
	if err != nil {
		return fmt.Errorf("start Sensei MCP: %w", err)
	}
	defer client.Close()

	packet, err := assist.Build(ctx, repo, client, id, *task, splitCSV(*filesCSV), time.Now())
	if err != nil {
		return err
	}
	if strings.TrimSpace(*out) == "" {
		return writeJSON(os.Stdout, packet)
	}
	if err := ensureParent(*out); err != nil {
		return err
	}
	if err := packet.Write(*out); err != nil {
		return err
	}
	digest, err := packet.Digest()
	if err != nil {
		return err
	}
	fmt.Printf("context packet: %s · %s\n", *out, digest)
	return nil
}

func runHandoff(args []string) error {
	fs := flag.NewFlagSet("handoff", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	contextPath := fs.String("context", "", "context packet path")
	from := fs.String("from", "", "agent handing off the task")
	to := fs.String("to", "", "agent receiving the task")
	summary := fs.String("summary", "", "what the receiving agent must continue")
	changed := fs.String("changed-files", "", "comma-separated changed files")
	tests := fs.String("tests", "", "comma-separated test/evidence statements")
	open := fs.String("open", "", "comma-separated unresolved questions")
	out := fs.String("out", "", "write the handoff packet to this path instead of stdout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*contextPath) == "" {
		return errors.New("handoff requires --context")
	}
	packet, err := assist.ReadContext(*contextPath)
	if err != nil {
		return fmt.Errorf("read context packet: %w", err)
	}
	handoff, err := assist.NewHandoff(packet, *from, *to, *summary, nil, splitCSV(*changed), splitCSV(*tests), splitCSV(*open), time.Now())
	if err != nil {
		return err
	}
	if strings.TrimSpace(*out) == "" {
		return writeJSON(os.Stdout, handoff)
	}
	if err := ensureParent(*out); err != nil {
		return err
	}
	if err := handoff.Write(*out, packet); err != nil {
		return err
	}
	fmt.Println("handoff packet:", *out)
	return nil
}

func writeJSON(file *os.File, value any) error {
	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")
	return enc.Encode(value)
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.Split(value, ",")
}

func ensureParent(path string) error {
	dir := filepath.Dir(path)
	if dir == "." || dir == "" {
		return nil
	}
	return os.MkdirAll(dir, 0o755)
}
