package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/xgtian/aginex/internal/app"
	"github.com/xgtian/aginex/internal/config"
	"github.com/xgtian/aginex/internal/platform/database"
)

func main() {
	output := flag.String("output", "docs/openapi.json", "OpenAPI output file")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		exit(err)
	}
	db, err := database.Open(cfg.Database)
	if err != nil {
		exit(err)
	}
	instance, err := app.New(cfg, db)
	if err != nil {
		exit(err)
	}
	document, err := json.MarshalIndent(instance.OpenAPI(), "", "  ")
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
