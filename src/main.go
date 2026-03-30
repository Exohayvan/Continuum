package main

import (
	"fmt"
	"io"
	"os"

	"continuum/src/nodeid"
)

var (
	getNodeID           = nodeid.GetNodeID
	stdout    io.Writer = os.Stdout
	stderr    io.Writer = os.Stderr
	exit                = os.Exit
)

func run(w io.Writer) error {
	_, err := fmt.Fprintln(w, getNodeID())
	return err
}

func main() {
	if err := run(stdout); err != nil {
		fmt.Fprintln(stderr, err)
		exit(1)
	}
}
