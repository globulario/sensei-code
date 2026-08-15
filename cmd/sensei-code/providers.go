package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/globulario/sensei-code/internal/provider"
)

func runProviders(ctx context.Context) error {
	fmt.Println("Provider sessions")
	for i, id := range provider.Ordered {
		status := provider.StatusFor(ctx, id)
		fmt.Printf("  %d. %-44s %s\n", i+1, provider.Label(id), provider.FormatStatus(status))
	}
	return nil
}

func runLogin(ctx context.Context, args []string) error {
	id, err := resolveProviderChoice(ctx, "Login", args)
	if err != nil {
		return err
	}
	status, err := provider.Login(ctx, id)
	if err != nil {
		return err
	}
	fmt.Printf("%s: %s\n", provider.Label(id), provider.FormatStatus(status))
	return nil
}

func runLogout(ctx context.Context, args []string) error {
	id, err := resolveProviderChoice(ctx, "Logout", args)
	if err != nil {
		return err
	}
	if err := provider.Logout(ctx, id); err != nil {
		return err
	}
	fmt.Println(provider.Label(id) + ": logged out")
	return nil
}

func resolveProviderChoice(ctx context.Context, title string, args []string) (provider.ID, error) {
	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		return provider.Parse(args[0])
	}
	fmt.Println(title + " provider")
	for i, id := range provider.Ordered {
		status := provider.StatusFor(ctx, id)
		fmt.Printf("  %d. %-44s %s\n", i+1, provider.Label(id), provider.FormatStatus(status))
	}
	fmt.Print("Choose 1-4: ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && !errors.Is(err, os.ErrClosed) {
		return "", err
	}
	return provider.Parse(strings.TrimSpace(line))
}
