package version

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const sampleVersionYAML = `# Continuum version
major: 1
minor: 1
patch: 0
`

const (
	versionFileName = "version.yaml"
	runtimeFileName = "runtime_default.go"
	writeFileFormat = "WriteFile() error = %v"
)

func TestDetectBump(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		messages string
		want     Bump
	}{
		{
			name:     "major wins over everything else",
			messages: "fix node id lookup\nadded panel card\nbreaking change for protocol",
			want:     BumpMajor,
		},
		{
			name:     "minor wins over patch",
			messages: "fix startup path\nfeature network panel",
			want:     BumpMinor,
		},
		{
			name:     "major from breaking keyword anywhere",
			messages: "docs update\nthis is a breaking protocol rewrite",
			want:     BumpMajor,
		},
		{
			name:     "major from redid keyword",
			messages: "redid: simplify context handling in App struct",
			want:     BumpMajor,
		},
		{
			name:     "minor from feat prefix",
			messages: "feat: implement version bump workflow and version management",
			want:     BumpMinor,
		},
		{
			name:     "minor from add keyword",
			messages: "Add GitHub Actions workflow for desktop build automation",
			want:     BumpMinor,
		},
		{
			name:     "minor from enhance keyword",
			messages: "Enhance README with project details and roadmap",
			want:     BumpMinor,
		},
		{
			name:     "minor from initial keyword",
			messages: "Initial commit",
			want:     BumpMinor,
		},
		{
			name:     "patch from fix keyword",
			messages: "Fix startup crash",
			want:     BumpPatch,
		},
		{
			name:     "patch from fixes keyword",
			messages: "Fixes node sync retry",
			want:     BumpPatch,
		},
		{
			name:     "patch from update keyword",
			messages: "Update indirect dependencies in go.mod and go.sum to latest versions",
			want:     BumpPatch,
		},
		{
			name:     "patch from bump keyword",
			messages: "Bump golang.org/x/net from 0.35.0 to 0.38.0",
			want:     BumpPatch,
		},
		{
			name:     "patch from refactor keyword",
			messages: "Refactor sonar-project.properties to remove network from sources",
			want:     BumpPatch,
		},
		{
			name:     "patch from ci prefix",
			messages: "ci: bump actions/checkout from 5 to 6",
			want:     BumpPatch,
		},
		{
			name:     "none without tracked keyword",
			messages: "docs only note about local setup",
			want:     BumpNone,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := DetectBump(tt.messages); got != tt.want {
				t.Fatalf("DetectBump() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestContainsAnyKeyword(t *testing.T) {
	t.Parallel()

	if !containsAnyKeyword("feat: add panel shell", []string{"fix", "feat"}) {
		t.Fatal("containsAnyKeyword() should match any listed keyword")
	}

	if containsAnyKeyword("docs only", []string{"fix", "feat"}) {
		t.Fatal("containsAnyKeyword() should return false when no keywords match")
	}
}

func TestContainsKeyword(t *testing.T) {
	t.Parallel()

	if containsKeyword("anything at all", "") {
		t.Fatal("containsKeyword() should reject an empty keyword")
	}

	if !containsKeyword("This is a BREAKING change", "breaking change") {
		t.Fatal("containsKeyword() should match phrase case-insensitively")
	}

	if containsKeyword("renew bootstrap list", "new") {
		t.Fatal("containsKeyword() should not match inside another word")
	}

	if containsKeyword("breaking protocol migration", "breaking change") {
		t.Fatal("containsKeyword() should require the full multi-word phrase")
	}
}

func TestSplitTokens(t *testing.T) {
	t.Parallel()

	got := splitTokens("Fix: node-id/new_path!")
	want := []string{"fix", "node", "id", "new", "path"}
	if len(got) != len(want) {
		t.Fatalf("len(splitTokens()) = %d, want %d", len(got), len(want))
	}

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("splitTokens()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestParse(t *testing.T) {
	t.Parallel()

	value, err := Parse([]byte(sampleVersionYAML))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if value != (Value{Major: 1, Minor: 1, Patch: 0}) {
		t.Fatalf("Parse() = %#v, want 1.1.0", value)
	}
}

func TestParseBadNumber(t *testing.T) {
	t.Parallel()

	_, err := Parse([]byte("major: nope\nminor: 1\npatch: 0\n"))
	if err == nil {
		t.Fatal("Parse() error = nil, want parse failure")
	}
}

func TestParseMissingField(t *testing.T) {
	t.Parallel()

	_, err := Parse([]byte("major: 0\nminor: 1\n"))
	if !errors.Is(err, errMissingField) {
		t.Fatalf("Parse() error = %v, want %v", err, errMissingField)
	}
}

func TestParseIgnoresUnknownAndMalformedLines(t *testing.T) {
	t.Parallel()

	data := []byte("major: 1\nbad line\nminor: 2\nextra: 99\npatch: 3\n")
	value, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if value != (Value{Major: 1, Minor: 2, Patch: 3}) {
		t.Fatalf("Parse() = %#v, want 1.2.3", value)
	}
}

func TestParseString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  Value
	}{
		{name: "plain version", input: "1.2.3", want: Value{Major: 1, Minor: 2, Patch: 3}},
		{name: "v prefix", input: "v2.3.4", want: Value{Major: 2, Minor: 3, Patch: 4}},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseString(tt.input)
			if err != nil {
				t.Fatalf("ParseString() error = %v", err)
			}

			if got != tt.want {
				t.Fatalf("ParseString() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestGet(t *testing.T) {
	t.Parallel()

	originalRuntimeVersion := runtimeVersion
	runtimeVersion = "1.5.0"
	t.Cleanup(func() {
		runtimeVersion = originalRuntimeVersion
	})

	if got := Get(); got != "1.5.0" {
		t.Fatalf("Get() = %q, want %q", got, "1.5.0")
	}
}

func TestGetDefaultsToStoredRuntimeVersion(t *testing.T) {
	t.Parallel()

	originalRuntimeVersion := runtimeVersion
	runtimeVersion = "dev"
	t.Cleanup(func() {
		runtimeVersion = originalRuntimeVersion
	})

	if got := Get(); got != defaultRuntimeVersion {
		t.Fatalf("Get() = %q, want %q", got, defaultRuntimeVersion)
	}
}

func TestParseStringInvalid(t *testing.T) {
	t.Parallel()

	tests := []string{"", "1.2", "one.2.3", "v1.two.3", "1.2.three"}
	for _, input := range tests {
		input := input
		t.Run(input, func(t *testing.T) {
			t.Parallel()

			_, err := ParseString(input)
			if !errors.Is(err, errInvalidValue) {
				t.Fatalf("ParseString() error = %v, want %v", err, errInvalidValue)
			}
		})
	}
}

func TestSplitCommitMessages(t *testing.T) {
	t.Parallel()

	data := []byte("feat: add panel\x00\x00fix: patch retry\x00  \x00")
	got := SplitCommitMessages(data)
	want := []string{"feat: add panel", "fix: patch retry"}
	if len(got) != len(want) {
		t.Fatalf("len(SplitCommitMessages()) = %d, want %d", len(got), len(want))
	}

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("SplitCommitMessages()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestCalculate(t *testing.T) {
	t.Parallel()

	base := Value{Major: 1, Minor: 1, Patch: 0}
	got := Calculate(base, []string{
		"fix bootstrap retry",
		"feat: add desktop shell",
		"update workflow labels",
		"redid: protocol handshake layout",
	})

	if got != (Value{Major: 2, Minor: 0, Patch: 0}) {
		t.Fatalf("Calculate() = %#v, want 2.0.0", got)
	}
}

func TestCalculateNoChanges(t *testing.T) {
	t.Parallel()

	base := Value{Major: 1, Minor: 1, Patch: 0}
	got := Calculate(base, []string{"docs only note about local setup"})
	if got != base {
		t.Fatalf("Calculate() = %#v, want %#v", got, base)
	}
}

func TestApply(t *testing.T) {
	t.Parallel()

	base := Value{Major: 1, Minor: 2, Patch: 3}
	tests := []struct {
		name string
		bump Bump
		want Value
	}{
		{name: "major", bump: BumpMajor, want: Value{Major: 2}},
		{name: "minor", bump: BumpMinor, want: Value{Major: 1, Minor: 3}},
		{name: "patch", bump: BumpPatch, want: Value{Major: 1, Minor: 2, Patch: 4}},
		{name: "none", bump: BumpNone, want: base},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := base.Apply(tt.bump); got != tt.want {
				t.Fatalf("Apply() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestCompare(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		left  Value
		right Value
		want  int
	}{
		{name: "less than", left: Value{Major: 1, Minor: 2, Patch: 2}, right: Value{Major: 1, Minor: 2, Patch: 3}, want: -1},
		{name: "minor greater than", left: Value{Major: 1, Minor: 3, Patch: 0}, right: Value{Major: 1, Minor: 2, Patch: 9}, want: 1},
		{name: "equal", left: Value{Major: 1, Minor: 2, Patch: 3}, right: Value{Major: 1, Minor: 2, Patch: 3}, want: 0},
		{name: "greater than", left: Value{Major: 2, Minor: 0, Patch: 0}, right: Value{Major: 1, Minor: 9, Patch: 9}, want: 1},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.left.Compare(tt.right); got != tt.want {
				t.Fatalf("Compare() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestString(t *testing.T) {
	t.Parallel()

	if got := (Value{Major: 2, Minor: 3, Patch: 4}).String(); got != "2.3.4" {
		t.Fatalf("String() = %q, want %q", got, "2.3.4")
	}
}

func TestMarshal(t *testing.T) {
	t.Parallel()

	got := string((Value{Major: 3, Minor: 2, Patch: 1}).Marshal())
	want := "major: 3\nminor: 2\npatch: 1\n"
	if got != want {
		t.Fatalf("Marshal() = %q, want %q", got, want)
	}
}

func TestSetFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), versionFileName)
	if err := os.WriteFile(path, []byte(sampleVersionYAML), 0o600); err != nil {
		t.Fatalf(writeFileFormat, err)
	}

	changed, err := SetFile(path, Value{Major: 1, Minor: 2, Patch: 0})
	if err != nil {
		t.Fatalf("SetFile() error = %v", err)
	}

	if !changed {
		t.Fatal("SetFile() changed = false, want true")
	}
}

func TestSetFileNoChange(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), versionFileName)
	if err := os.WriteFile(path, []byte(sampleVersionYAML), 0o600); err != nil {
		t.Fatalf(writeFileFormat, err)
	}

	changed, err := SetFile(path, Value{Major: 1, Minor: 1, Patch: 0})
	if err != nil {
		t.Fatalf("SetFile() error = %v", err)
	}

	if changed {
		t.Fatal("SetFile() changed = true, want false")
	}
}

func TestSetFileReadError(t *testing.T) {
	t.Parallel()

	_, err := SetFile(filepath.Join(t.TempDir(), versionFileName), Value{Major: 1, Minor: 1, Patch: 0})
	if err == nil {
		t.Fatal("SetFile() error = nil, want read failure")
	}
}

func TestSetFileParseError(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), versionFileName)
	if err := os.WriteFile(path, []byte("major: x\nminor: 1\npatch: 0\n"), 0o600); err != nil {
		t.Fatalf(writeFileFormat, err)
	}

	_, err := SetFile(path, Value{Major: 1, Minor: 1, Patch: 0})
	if err == nil {
		t.Fatal("SetFile() error = nil, want parse failure")
	}
}

func TestSetFileWriteError(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), versionFileName)
	if err := os.WriteFile(path, []byte(sampleVersionYAML), 0o600); err != nil {
		t.Fatalf(writeFileFormat, err)
	}

	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}

	_, err := SetFile(path, Value{Major: 1, Minor: 2, Patch: 0})
	if err == nil {
		t.Fatal("SetFile() error = nil, want write failure")
	}
}

func TestSetRuntimeDefaultFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), runtimeFileName)
	if err := SetRuntimeDefaultFile(path, Value{Major: 1, Minor: 5, Patch: 2}); err != nil {
		t.Fatalf("SetRuntimeDefaultFile() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	want := "package version\n\nconst defaultRuntimeVersion = \"1.5.2\"\n"
	if string(data) != want {
		t.Fatalf("SetRuntimeDefaultFile() = %q, want %q", string(data), want)
	}
}
