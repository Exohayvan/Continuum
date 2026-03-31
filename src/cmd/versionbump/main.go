package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"continuum/src/version"
)

var (
	stdoutWriter          io.Writer = os.Stdout
	stderrWriter          io.Writer = os.Stderr
	readFile                        = os.ReadFile
	parseVersionString              = version.ParseString
	splitCommitMessages             = version.SplitCommitMessages
	calculateVersion                = version.Calculate
	setVersionFile                  = version.SetFile
	setRuntimeDefaultFile           = version.SetRuntimeDefaultFile
	exitFunc                        = os.Exit
)

func run(args []string) error {
	flags := flag.NewFlagSet("versionbump", flag.ContinueOnError)
	flags.SetOutput(io.Discard)

	versionFile := flags.String("file", "src/version.yaml", "path to version yaml")
	messagesFile := flags.String("messages-file", "", "path to a file containing commit messages")
	baseVersion := flags.String("base-version", "", "semantic version baseline")

	if err := flags.Parse(args); err != nil {
		return err
	}

	if *messagesFile == "" {
		return fmt.Errorf("messages-file is required")
	}

	if *baseVersion == "" {
		return fmt.Errorf("base-version is required")
	}

	base, err := parseVersionString(*baseVersion)
	if err != nil {
		return err
	}

	messages, err := readFile(*messagesFile)
	if err != nil {
		return err
	}

	next := calculateVersion(base, splitCommitMessages(messages))
	changed, err := setVersionFile(*versionFile, next)
	if err != nil {
		return err
	}

	if err := setRuntimeDefaultFile("src/version/runtime_default.go", next); err != nil {
		return err
	}

	if !changed {
		_, err = fmt.Fprintf(stdoutWriter, "Version unchanged at %s\n", next.String())
		return err
	}

	_, err = fmt.Fprintf(stdoutWriter, "Set version to %s\n", next.String())
	return err
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(stderrWriter, err)
		exitFunc(1)
	}
}
