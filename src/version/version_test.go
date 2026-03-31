package version

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const sampleVersionYAML = `# Continuum version
major: 0
minor: 1
patch: 0
`

const (
	versionFileName   = "version.yaml"
	writeFileFormat   = "WriteFile() error = %v"
	fixStartupMessage = "fix startup"
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
			name:     "none without keyword",
			messages: "refactor internal wiring",
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

	if value != (Value{Major: 0, Minor: 1, Patch: 0}) {
		t.Fatalf("Parse() = %#v, want 0.1.0", value)
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

func TestBumpFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), versionFileName)
	if err := os.WriteFile(path, []byte(sampleVersionYAML), 0o644); err != nil {
		t.Fatalf(writeFileFormat, err)
	}

	got, bump, changed, err := BumpFile(path, "added bootstrap manifest")
	if err != nil {
		t.Fatalf("BumpFile() error = %v", err)
	}

	if !changed {
		t.Fatal("BumpFile() changed = false, want true")
	}

	if bump != BumpMinor {
		t.Fatalf("BumpFile() bump = %q, want %q", bump, BumpMinor)
	}

	if got != (Value{Major: 0, Minor: 2, Patch: 0}) {
		t.Fatalf("BumpFile() value = %#v, want 0.2.0", got)
	}
}

func TestBumpFileNoChange(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), versionFileName)
	if err := os.WriteFile(path, []byte(sampleVersionYAML), 0o644); err != nil {
		t.Fatalf(writeFileFormat, err)
	}

	got, bump, changed, err := BumpFile(path, "docs cleanup")
	if err != nil {
		t.Fatalf("BumpFile() error = %v", err)
	}

	if changed {
		t.Fatal("BumpFile() changed = true, want false")
	}

	if bump != BumpNone {
		t.Fatalf("BumpFile() bump = %q, want %q", bump, BumpNone)
	}

	if got != (Value{Major: 0, Minor: 1, Patch: 0}) {
		t.Fatalf("BumpFile() value = %#v, want 0.1.0", got)
	}
}

func TestBumpFileReadError(t *testing.T) {
	t.Parallel()

	_, _, _, err := BumpFile(filepath.Join(t.TempDir(), "missing.yaml"), fixStartupMessage)
	if err == nil {
		t.Fatal("BumpFile() error = nil, want read failure")
	}
}

func TestBumpFileParseError(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), versionFileName)
	if err := os.WriteFile(path, []byte("major: x\nminor: 1\npatch: 0\n"), 0o644); err != nil {
		t.Fatalf(writeFileFormat, err)
	}

	_, _, _, err := BumpFile(path, fixStartupMessage)
	if err == nil {
		t.Fatal("BumpFile() error = nil, want parse failure")
	}
}

func TestBumpFileWriteError(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), versionFileName)
	if err := os.WriteFile(path, []byte(sampleVersionYAML), 0o644); err != nil {
		t.Fatalf(writeFileFormat, err)
	}

	if err := os.Chmod(path, 0o444); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}

	_, _, _, err := BumpFile(path, fixStartupMessage)
	if err == nil {
		t.Fatal("BumpFile() error = nil, want write failure")
	}
}
