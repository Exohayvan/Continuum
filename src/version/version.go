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

var errMissingField = errors.New("version file must define major, minor, and patch")

func DetectBump(messages string) Bump {
	switch {
	case containsKeyword(messages, "breaking change"), containsKeyword(messages, "redid"):
		return BumpMajor
	case containsKeyword(messages, "feature"), containsKeyword(messages, "new"), containsKeyword(messages, "added"):
		return BumpMinor
	case containsKeyword(messages, "fix"), containsKeyword(messages, "fixed"), containsKeyword(messages, "fixes"):
		return BumpPatch
	default:
		return BumpNone
	}
}

func containsKeyword(messages string, keyword string) bool {
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
		value                   Value
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

func (v Value) Marshal() []byte {
	return []byte(fmt.Sprintf("major: %d\nminor: %d\npatch: %d\n", v.Major, v.Minor, v.Patch))
}

func BumpFile(path string, messages string) (Value, Bump, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Value{}, BumpNone, false, err
	}

	current, err := Parse(data)
	if err != nil {
		return Value{}, BumpNone, false, err
	}

	bump := DetectBump(messages)
	if bump == BumpNone {
		return current, bump, false, nil
	}

	next := current.Apply(bump)
	if err := os.WriteFile(path, next.Marshal(), 0o644); err != nil {
		return Value{}, BumpNone, false, err
	}

	return next, bump, true, nil
}
