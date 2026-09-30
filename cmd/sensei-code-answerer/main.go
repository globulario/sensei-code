// Command sensei-code-answerer answers architecture and advisory review
// requests on the configured Sensei Code mailbox. Everything it does lives in
// internal/answerer; this is only the process entrypoint.
package main

import (
	"context"
	"os"

	"github.com/globulario/sensei-code/internal/answerer"
)

func main() {
	logf := func(s string) { os.Stderr.WriteString("sensei-code-answerer: " + s + "\n") }
	os.Exit(answerer.Main(context.Background(), os.LookupEnv, os.ReadFile, logf))
}
