package ghwebhook

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A signed webhook is transport truth, not objective authority.
//
// That sentence is only worth writing if something enforces it. These tests are
// that something: they read this package's own source and refuse the imports
// and the call shapes through which "authenticated" would become "authorized".
//
// A comment saying "do not call the engine from here" is advice. An import that
// does not exist is a guarantee, and it survives an author who never read the
// comment.

// forbiddenImports are the packages this ingress must not be able to reach.
//
// Not a style rule. Each one is a capability: with an engine handle a delivery
// can create work, with a resolver it can choose who answers, with a runner it
// can execute. The absence of the import is what makes "this slice has no
// governed effect" a fact about the code rather than a claim about intent.
var forbiddenImports = map[string]string{
	"github.com/globulario/sensei-code/internal/workflow": "the workflow engine: a delivery could submit or mutate governed work",
	"github.com/globulario/sensei-code/internal/control":  "the control surface: the two authentication regimes must not share code",
	"github.com/globulario/sensei-code/internal/agent":    "the agent runners: a delivery could execute something",
	"github.com/globulario/sensei-code/internal/ghbridge": "the review bridge: a delivery could answer or wake a review turn",
	"github.com/globulario/sensei-code/internal/session":  "session evidence: a webhook secret or comment body must not reach it",
	"github.com/globulario/sensei-code/internal/roles":    "role semantics: a sender is not a role holder",
	"os/exec": "process execution",
}

func packageFiles(t *testing.T) map[string]*ast.File {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	fset := token.NewFileSet()
	files := make(map[string]*ast.File)
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Clean(name), nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files[name] = f
	}
	if len(files) == 0 {
		t.Fatal("no source files found; the boundary test is not reading the package")
	}
	return files
}

func TestTheIngressCannotReachTheWorkflowEngine(t *testing.T) {
	for name, file := range packageFiles(t) {
		// The test files are allowed nothing extra either: a test that imported
		// the engine would be a working demonstration that the wiring is
		// possible, and the next author would copy it.
		for _, spec := range file.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("%s: bad import %s", name, spec.Path.Value)
			}
			if why, forbidden := forbiddenImports[path]; forbidden {
				t.Errorf("%s imports %s -- %s", name, path, why)
			}
		}
	}
}

// The call shapes named in the slice brief, refused by name.
//
// The import test above is the real guarantee; this catches the case where the
// capability arrives some other way — an interface satisfied here, a callback
// handed in — and someone writes the call anyway.
var forbiddenCalls = []string{
	"SubmitGovernedLocal",
	"SubmitGoverned",
	"Submission",
	"Resolve",
	"CLIResolved",
}

func TestTheIngressCallsNothingGoverned(t *testing.T) {
	for name, file := range packageFiles(t) {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			var ident string
			switch fn := call.Fun.(type) {
			case *ast.Ident:
				ident = fn.Name
			case *ast.SelectorExpr:
				ident = fn.Sel.Name
			default:
				return true
			}
			for _, forbidden := range forbiddenCalls {
				if ident == forbidden {
					t.Errorf("%s calls %s: webhook ingress must have no governed effect", name, ident)
				}
			}
			return true
		})
	}
}

// The verification ORDER, pinned structurally as well as behaviourally.
//
// The behavioural pins (malformed JSON + bad signature -> 401) prove the order
// from outside. This proves it from inside, because the behavioural proof has
// one blind spot: a second parse added ABOVE the signature check, whose result
// is discarded on the refusal path, would leave every status code unchanged
// while still running a parser on unauthenticated bytes.
func TestNothingParsesTheBodyBeforeTheSignatureIsChecked(t *testing.T) {
	src, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("read server.go: %v", err)
	}
	text := string(src)

	handler := text[strings.Index(text, "func (s *Server) handle("):]
	if end := strings.Index(handler, "\n}\n"); end > 0 {
		handler = handler[:end]
	}
	if handler == "" {
		t.Fatal("the handler is gone")
	}

	verifyAt := strings.Index(handler, "verify(s.secret")
	if verifyAt < 0 {
		t.Fatal("the handler no longer verifies the signature")
	}
	readAt := strings.Index(handler, "io.ReadAll")
	if readAt < 0 {
		t.Fatal("the handler no longer reads the raw body")
	}
	if readAt > verifyAt {
		t.Fatal("the body is read after the signature check; the MAC cannot be over bytes not yet read")
	}
	// The bound must WRAP the read, not sit beside it. A limit checked after
	// io.ReadAll returns has already allocated whatever it was meant to refuse,
	// so the composition is the pin rather than the mere presence of both names.
	if !strings.Contains(handler, "io.ReadAll(http.MaxBytesReader(") {
		t.Fatal("the raw body is not read through http.MaxBytesReader; it is unbounded at the moment it is read")
	}
	if strings.Count(handler, "io.ReadAll") != 1 {
		t.Error("the handler reads the body more than once; the MAC must be over the one copy that was kept")
	}

	// Nothing that parses may appear before the verify call.
	before := handler[:verifyAt]
	for _, parseShape := range []string{"json.Unmarshal", "json.NewDecoder", "json.Decode", "r.ParseForm", "r.FormValue"} {
		if strings.Contains(before, parseShape) {
			t.Errorf("%s runs before the signature is verified: a parser is exposed to unauthenticated input", parseShape)
		}
	}

	// And the dispatch happens after it.
	if dispatchAt := strings.Index(handler, "EventHeader"); dispatchAt >= 0 && dispatchAt < verifyAt {
		t.Error("the event header is dispatched on before the signature is verified")
	}
}

// The headers and payload fields that are NOT authentication, pinned so nobody
// promotes one into the pre-verification region.
func TestNoRequestFactSubstitutesForTheSignature(t *testing.T) {
	src, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("read server.go: %v", err)
	}
	handler := string(src)[strings.Index(string(src), "func (s *Server) handle("):]
	if end := strings.Index(handler, "\n}\n"); end > 0 {
		handler = handler[:end]
	}
	before := handler[:strings.Index(handler, "verify(s.secret")]

	for _, notAuth := range []string{"User-Agent", "UserAgent", "sender", "Sender", "DeliveryHeader", "EventHeader"} {
		if strings.Contains(before, notAuth) {
			t.Errorf("%q is consulted before the signature check; it is a fact carried BY an authenticated delivery, not authentication", notAuth)
		}
	}
}
