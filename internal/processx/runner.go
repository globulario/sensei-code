package processx

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
)

type Line struct{ Stream, Text string }

type Result struct{ ExitCode int }

func Run(ctx context.Context, dir, name string, args []string, stdin io.Reader, onLine func(Line)) (Result, error) {
	return RunWithEnv(ctx, dir, name, args, nil, stdin, onLine)
}

// RunWithEnv runs a command with extra environment entries appended to the
// inherited environment.
func RunWithEnv(ctx context.Context, dir, name string, args, extraEnv []string, stdin io.Reader, onLine func(Line)) (Result, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Stdin = stdin
	if len(extraEnv) != 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
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
