package version

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Bump string

const (
	BumpNone  Bump = "none"
	BumpPatch Bump = "patch"
	BumpMinor Bump = "minor"
	BumpMajor Bump = "major"
)

type Value struct {
	Major int
	Minor int
	Patch int
}

var (
	errMissingField = errors.New("version file must define major, minor, and patch")
	errInvalidValue = errors.New("version string must use semantic version format")
	runtimeVersion  = "dev"

	majorKeywords = []string{
		"breaking",
		"breaking change",
		"break",
		"redid",
		"rewrite",
		"rewrote",
		"overhaul",
		"overhauled",
	}
	minorKeywords = []string{
		"feat",
		"feature",
		"features",
		"new",
		"add",
		"adds",
		"added",
		"implement",
		"implements",
		"implemented",
		"enhance",
		"enhances",
		"enhanced",
		"initial",
		"introduce",
		"introduces",
		"introduced",
		"create",
		"creates",
		"created",
	}
	patchKeywords = []string{
		"fix",
		"fixed",
		"fixes",
		"bugfix",
		"bugfixes",
		"update",
		"updates",
		"updated",
		"upgrade",
		"upgrades",
		"upgraded",
		"bump",
		"bumps",
		"bumped",
		"refactor",
		"refactors",
		"refactored",
		"cleanup",
		"cleanups",
		"cleaned",
		"adjust",
		"adjusts",
		"adjusted",
		"reworked",
		"rework",
		"ci",
	}
)

func DetectBump(messages string) Bump {
	switch {
	case containsAnyKeyword(messages, majorKeywords):
		return BumpMajor
	case containsAnyKeyword(messages, minorKeywords):
		return BumpMinor
	case containsAnyKeyword(messages, patchKeywords):
		return BumpPatch
	default:
		return BumpNone
	}
}

func containsAnyKeyword(messages string, keywords []string) bool {
	for _, keyword := range keywords {
		if containsKeyword(messages, keyword) {
			return true
		}
	}

	return false
}

func containsKeyword(messages, keyword string) bool {
	messageTokens := splitTokens(messages)
	keywordTokens := splitTokens(keyword)
	if len(keywordTokens) == 0 {
		return false
	}

	if len(keywordTokens) == 1 {
		for _, token := range messageTokens {
			if token == keywordTokens[0] {
				return true
			}
		}

		return false
	}

	for i := 0; i <= len(messageTokens)-len(keywordTokens); i++ {
		match := true
		for j := range keywordTokens {
			if messageTokens[i+j] != keywordTokens[j] {
				match = false
				break
			}
		}

		if match {
			return true
		}
	}

	return false
}

func splitTokens(messages string) []string {
	replacer := strings.NewReplacer(
		"\n", " ",
		"\r", " ",
		"\t", " ",
		".", " ",
		",", " ",
		":", " ",
		";", " ",
		"!", " ",
		"?", " ",
		"(", " ",
		")", " ",
		"[", " ",
		"]", " ",
		"{", " ",
		"}", " ",
		"-", " ",
		"_", " ",
		"/", " ",
		"\\", " ",
	)

	return strings.Fields(replacer.Replace(strings.ToLower(messages)))
}

func Parse(data []byte) (Value, error) {
	var (
		value                        Value
		hasMajor, hasMinor, hasPatch bool
	)

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		rawValue := strings.TrimSpace(parts[1])
		parsedValue, err := strconv.Atoi(rawValue)
		if err != nil {
			return Value{}, fmt.Errorf("parse %s: %w", key, err)
		}

		switch key {
		case "major":
			value.Major = parsedValue
			hasMajor = true
		case "minor":
			value.Minor = parsedValue
			hasMinor = true
		case "patch":
			value.Patch = parsedValue
			hasPatch = true
		}
	}

	if !hasMajor || !hasMinor || !hasPatch {
		return Value{}, errMissingField
	}

	return value, nil
}

func ParseString(raw string) (Value, error) {
	trimmed := strings.TrimSpace(raw)
	trimmed = strings.TrimPrefix(trimmed, "v")
	parts := strings.Split(trimmed, ".")
	if len(parts) != 3 {
		return Value{}, errInvalidValue
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return Value{}, errInvalidValue
	}

	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return Value{}, errInvalidValue
	}

	patch, err := strconv.Atoi(parts[2])
	if err != nil {
		return Value{}, errInvalidValue
	}

	return Value{Major: major, Minor: minor, Patch: patch}, nil
}

func Get() string {
	if runtimeVersion != "dev" {
		return runtimeVersion
	}

	return defaultRuntimeVersion
}

func SplitCommitMessages(data []byte) []string {
	var messages []string

	for _, raw := range strings.Split(string(data), "\x00") {
		trimmed := strings.TrimSpace(raw)
		if trimmed != "" {
			messages = append(messages, trimmed)
		}
	}

	return messages
}

func Calculate(base Value, commitMessages []string) Value {
	current := base
	for _, message := range commitMessages {
		current = current.Apply(DetectBump(message))
	}

	return current
}

func (v Value) Apply(bump Bump) Value {
	switch bump {
	case BumpMajor:
		return Value{Major: v.Major + 1}
	case BumpMinor:
		return Value{Major: v.Major, Minor: v.Minor + 1}
	case BumpPatch:
		return Value{Major: v.Major, Minor: v.Minor, Patch: v.Patch + 1}
	default:
		return v
	}
}

func (v Value) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

func (v Value) Compare(other Value) int {
	switch {
	case v.Major != other.Major:
		return compareInt(v.Major, other.Major)
	case v.Minor != other.Minor:
		return compareInt(v.Minor, other.Minor)
	default:
		return compareInt(v.Patch, other.Patch)
	}
}

func (v Value) Marshal() []byte {
	return []byte(fmt.Sprintf("major: %d\nminor: %d\npatch: %d\n", v.Major, v.Minor, v.Patch))
}

func SetFile(path string, value Value) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}

	current, err := Parse(data)
	if err != nil {
		return false, err
	}

	if current == value {
		return false, nil
	}

	if err := os.WriteFile(path, value.Marshal(), 0o644); err != nil {
		return false, err
	}

	return true, nil
}

func SetRuntimeDefaultFile(path string, value Value) error {
	content := fmt.Sprintf("package version\n\nconst defaultRuntimeVersion = %q\n", value.String())
	return os.WriteFile(path, []byte(content), 0o644)
}

func compareInt(left, right int) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}
