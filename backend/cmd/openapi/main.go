package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/xgtian-root/aginex/backend/internal/composition"
)

func main() {
	output := flag.String("output", "docs/openapi.json", "OpenAPI output file")
	flag.Parse()

	document, err := json.MarshalIndent(
		composition.Definition().BuildOpenAPI(),
		"",
		"  ",
	)
	if err != nil {
		exit(err)
	}
	document = append(document, '\n')
	if err := os.MkdirAll(filepath.Dir(*output), 0o750); err != nil {
		exit(err)
	}
	if err := os.WriteFile(*output, document, 0o640); err != nil {
		exit(err)
	}
	fmt.Printf("Wrote %s\n", *output)
}

func exit(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
