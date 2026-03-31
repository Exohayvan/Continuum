package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"continuum/src/version"
)

var (
	stdoutWriter   io.Writer = os.Stdout
	stderrWriter   io.Writer = os.Stderr
	readFile                  = os.ReadFile
	bumpVersionFile           = version.BumpFile
	exitFunc                  = os.Exit
)

func run(args []string) error {
	flags := flag.NewFlagSet("versionbump", flag.ContinueOnError)
	flags.SetOutput(io.Discard)

	versionFile := flags.String("file", "src/version.yaml", "path to version yaml")
	messagesFile := flags.String("messages-file", "", "path to a file containing commit messages")

	if err := flags.Parse(args); err != nil {
		return err
	}

	if *messagesFile == "" {
		return fmt.Errorf("messages-file is required")
	}

	messages, err := readFile(*messagesFile)
	if err != nil {
		return err
	}

	next, bump, changed, err := bumpVersionFile(*versionFile, string(messages))
	if err != nil {
		return err
	}

	if !changed {
		_, err = fmt.Fprintf(stdoutWriter, "Version unchanged at %s\n", next.String())
		return err
	}

	_, err = fmt.Fprintf(stdoutWriter, "Bumped %s version to %s\n", bump, next.String())
	return err
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(stderrWriter, err)
		exitFunc(1)
	}
}
