package processx

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
)

type Line struct{ Stream, Text string }

// Environ returns the process environment with the named variables removed.
func Environ(unset []string) []string {
	if len(unset) == 0 {
		return os.Environ()
	}
	drop := make(map[string]struct{}, len(unset))
	for _, name := range unset {
		drop[name] = struct{}{}
	}
	out := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if _, ok := drop[name]; ok {
			continue
		}
		out = append(out, entry)
	}
	return out
}

type Result struct{ ExitCode int }

func Run(ctx context.Context, dir, name string, args []string, stdin io.Reader, onLine func(Line)) (Result, error) {
	return RunWithEnv(ctx, dir, name, args, nil, nil, stdin, onLine)
}

// RunWithEnv runs a command with extra environment entries appended to the
// inherited environment, and with unsetEnv variables removed from it.
func RunWithEnv(ctx context.Context, dir, name string, args, extraEnv, unsetEnv []string, stdin io.Reader, onLine func(Line)) (Result, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Stdin = stdin
	if len(extraEnv) != 0 || len(unsetEnv) != 0 {
		cmd.Env = append(Environ(unsetEnv), extraEnv...)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Result{}, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return Result{}, err
	}
	if err := cmd.Start(); err != nil {
		return Result{}, err
	}
	var wg sync.WaitGroup
	pump := func(stream string, r io.Reader) {
		defer wg.Done()
		s := bufio.NewScanner(r)
		buf := make([]byte, 64*1024)
		s.Buffer(buf, 1024*1024)
		for s.Scan() {
			if onLine != nil {
				onLine(Line{Stream: stream, Text: s.Text()})
			}
		}
	}
	wg.Add(2)
	go pump("stdout", stdout)
	go pump("stderr", stderr)
	err = cmd.Wait()
	wg.Wait()
	if err == nil {
		return Result{ExitCode: 0}, nil
	}
	var ee *exec.ExitError
	if ok := errorAs(err, &ee); ok {
		return Result{ExitCode: ee.ExitCode()}, fmt.Errorf("%s exited %d", name, ee.ExitCode())
	}
	return Result{}, err
}

func errorAs(err error, target **exec.ExitError) bool {
	ee, ok := err.(*exec.ExitError)
	if !ok {
		return false
	}
	*target = ee
	return true
}
