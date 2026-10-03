// Command openapi writes the HTTP API contract, contracts/http/openapi.yaml,
// from the API's route table. Run it with "go generate ./..." from
// apps/backend; a test fails while the committed file is stale.
//
// Usage:
//
//	openapi [-o file]
package main

//go:generate go run . -o ../../../../contracts/http/openapi.yaml

import (
	"flag"
	"fmt"
	"os"

	"github.com/quackdiscord/bot/internal/app"
	"github.com/quackdiscord/bot/internal/contract"
)

func main() {
	out := flag.String("o", "-", "write the contract to `file` instead of stdout")
	flag.Parse()
	if err := run(*out); err != nil {
		fmt.Fprintln(os.Stderr, "openapi:", err)
		os.Exit(1)
	}
}

func run(out string) error {
	spec, err := generate()
	if err != nil {
		return err
	}
	if out == "-" {
		_, err = os.Stdout.Write(spec)
		return err
	}
	return os.WriteFile(out, spec, 0o644)
}

// generate returns the contract for the routes the server mounts.
func generate() ([]byte, error) {
	routes, err := app.Routes()
	if err != nil {
		return nil, err
	}
	return contract.Generate(routes)
}
