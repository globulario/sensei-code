package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/doctor"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/gitx"
	"github.com/globulario/sensei-code/internal/session"
	"github.com/globulario/sensei-code/internal/tui"
	"github.com/globulario/sensei-code/internal/workflow"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cwd, err := os.Getwd()
	fatalIf(err)
	repo, err := gitx.Discover(ctx, cwd)
	fatalIf(err)
	cfg, err := config.Load(repo.Root)
	fatalIf(err)
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "init":
			fatalIf(config.Save(repo.Root, cfg))
			fmt.Println("Sensei Code initialized at", filepath.Join(repo.Root, ".sensei-code"))
			return
		case "doctor":
			report := doctor.Run(ctx, repo.Root, cfg)
			for _, check := range report.Checks {
				detail := ""
				if strings.TrimSpace(check.Detail) != "" {
					detail = " · " + check.Detail
				}
				fmt.Printf("%-4s  %s%s\n", check.Status, check.Name, detail)
			}
			if !report.OK() {
				os.Exit(1)
			}
			return
		case "help", "--help", "-h":
			printUsage()
			return
		default:
			fmt.Fprintf(os.Stderr, "sensei-code: unknown command %q\n\n", os.Args[1])
			printUsage()
			os.Exit(2)
		}
	}
	sessionID := "session-" + time.Now().UTC().Format("20060102T150405.000000000Z")
	store, err := session.New(repo.Root, sessionID)
	fatalIf(err)
	bus := event.NewBus()
	events, unsubscribe := bus.Subscribe(512)
	defer unsubscribe()
	engine := workflow.New(repo, cfg, bus, store, sessionID)
	p := tea.NewProgram(tui.New(ctx, engine, events))
	_, err = p.Run()
	fatalIf(err)
}

func fatalIf(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "sensei-code:", err)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`Sensei Code

Usage:
  sensei-code          launch the interactive governed workspace
  sensei-code init     create the local capability/provider configuration
  sensei-code doctor   verify Git, providers, and the Sensei MCP surface
  sensei-code help     show this help`)
}
