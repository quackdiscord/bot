package main

import (
	"bytes"
	"os"
	"testing"
)

// committed is the contract in the repository, relative to this package.
const committed = "../../../../contracts/http/openapi.yaml"

// TestCommittedContractIsCurrent fails when a route, or a type a route reads
// or writes, changed without regenerating the contract.
func TestCommittedContractIsCurrent(t *testing.T) {
	want, err := generate()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(committed)
	if err != nil {
		t.Fatalf("read the committed contract: %v; run go generate ./... from apps/backend", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("contracts/http/openapi.yaml is stale; run go generate ./... from apps/backend and commit the result")
	}
	again, err := generate()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(again, want) {
		t.Fatal("generating the contract twice gave different output")
	}
}
