package main

import (
	"fmt"
	"io"
	"os"

	"continuum/src/nodeid"
)

var getNodeID = nodeid.GetNodeID

func run(w io.Writer) error {
	_, err := fmt.Fprintln(w, getNodeID())
	return err
}

func main() {
	if err := run(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
