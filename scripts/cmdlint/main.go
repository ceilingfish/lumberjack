package main

import (
	"fmt"
	"os"
)

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	findings, err := Check(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cmdlint:", err)
		os.Exit(2)
	}
	for _, f := range findings {
		fmt.Println(f)
	}
	if len(findings) > 0 {
		fmt.Fprintf(os.Stderr, "\ncmdlint: %d violation(s)\n", len(findings))
		os.Exit(1)
	}
}
