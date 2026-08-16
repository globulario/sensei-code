package report

import (
	"strings"
	"testing"
)

const sampleDiff = `diff --git a/cmd/sensei-code/main.go b/cmd/sensei-code/main.go
index 1111111..2222222 100644
--- a/cmd/sensei-code/main.go
+++ b/cmd/sensei-code/main.go
@@ -1,3 +1,4 @@
+	fmt.Println(version)
diff --git a/internal/version/version.go b/internal/version/version.go
new file mode 100644
index 0000000..3333333
--- /dev/null
+++ b/internal/version/version.go
@@ -0,0 +1,3 @@
+package version
diff --git a/internal/old/gone.go b/internal/old/gone.go
deleted file mode 100644
index 4444444..0000000
--- a/internal/old/gone.go
+++ /dev/null
@@ -1,2 +0,0 @@
-package old
diff --git a/docs/awareness/invariants.yaml b/docs/awareness/invariants.yaml
index 5555555..6666666 100644
--- a/docs/awareness/invariants.yaml
+++ b/docs/awareness/invariants.yaml
@@ -1,2 +1,8 @@
+  - id: sensei_code.version.flag_is_inert
+    title: version flag changes nothing else
+  - id: sensei_code.version.second
+    title: another
diff --git a/docs/awareness/failure_modes.yaml b/docs/awareness/failure_modes.yaml
index 7777777..8888888 100644
--- a/docs/awareness/failure_modes.yaml
+++ b/docs/awareness/failure_modes.yaml
@@ -1,2 +1,4 @@
+  - id: failure.version.printed_wrong
+    title: wrong version printed
`

func TestFromDiffCountsFilesByStatus(t *testing.T) {
	c := FromDiff(sampleDiff)
	added, modified, deleted := c.Counts()
	if added != 1 || deleted != 1 || modified != 3 {
		t.Fatalf("added=%d modified=%d deleted=%d; want 1/3/1", added, modified, deleted)
	}
}

func TestFromDiffCountsAwarenessEntriesByKind(t *testing.T) {
	c := FromDiff(sampleDiff)
	if got := c.Awareness["invariants"]; got != 2 {
		t.Fatalf("invariants = %d, want 2", got)
	}
	if got := c.Awareness["failure modes"]; got != 1 {
		t.Fatalf("failure modes = %d, want 1", got)
	}
}

func TestGoFilesSeparatesTests(t *testing.T) {
	c := FromDiff(sampleDiff)
	source, tests := c.GoFiles()
	if source != 3 || tests != 0 {
		t.Fatalf("source=%d tests=%d; want 3/0", source, tests)
	}
}

func TestReportNeverClaimsAdmissionOrCorrectness(t *testing.T) {
	got := FromDiff(sampleDiff).Render("add a version flag")
	for _, want := range []string{
		"not admitted",
		"reviewer acceptance is not proof",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("report is missing the disclaimer %q:\n%s", want, got)
		}
	}
}

func TestReportFlagsMissingTestsAndAbsentAudit(t *testing.T) {
	not := strings.Join(FromDiff(sampleDiff).NotEstablished(), "\n")
	if !strings.Contains(not, "no test file") {
		t.Fatal("report did not notice that Go sources changed with no test")
	}
	if !strings.Contains(not, "Sensei did not audit this diff") {
		t.Fatal("report did not notice the missing Sensei audit")
	}
}

func TestReportDoesNotWarnAboutTestsWhenTestsChanged(t *testing.T) {
	diff := sampleDiff + `diff --git a/internal/version/version_test.go b/internal/version/version_test.go
new file mode 100644
--- /dev/null
+++ b/internal/version/version_test.go
@@ -0,0 +1,1 @@
+package version
`
	not := strings.Join(FromDiff(diff).NotEstablished(), "\n")
	if strings.Contains(not, "no test file") {
		t.Fatal("report warned about missing tests when a test was added")
	}
}
