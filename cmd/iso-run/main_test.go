package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunVersionReturnsZeroAndPrintsVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run(context.Background(), []string{"--version"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, "iso-run") {
		t.Fatalf("stdout = %q, want version containing iso-run", got)
	}
}

func TestRunUnsupportedInputSuffixFailsBeforeResourceLoading(t *testing.T) {
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "addresses.xlsx")
	outputPath := filepath.Join(dir, "out.csv")
	if err := os.WriteFile(inputPath, []byte("address\nUS NY\n"), 0o644); err != nil {
		t.Fatalf("write input: %v", err)
	}
	var stdout, stderr bytes.Buffer

	code := run(context.Background(), []string{
		"--input-path", inputPath,
		"--output-path", outputPath,
		"--resources-dir", filepath.Join(dir, "missing-resources"),
		"--model-dir", filepath.Join(dir, "missing-model"),
	}, &stdout, &stderr)

	if code == 0 {
		t.Fatalf("exit code = 0, want nonzero; stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if got := stderr.String(); !strings.Contains(got, "unsupported input format") {
		t.Fatalf("stderr = %q, want unsupported input format", got)
	}
}

func TestRunMissingAddressColumnInCSVFailsBeforeResourceLoading(t *testing.T) {
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "addresses.csv")
	outputPath := filepath.Join(dir, "out.csv")
	if err := os.WriteFile(inputPath, []byte("not_address\nUS NY\n"), 0o644); err != nil {
		t.Fatalf("write input: %v", err)
	}
	var stdout, stderr bytes.Buffer

	code := run(context.Background(), []string{
		"-i", inputPath,
		"-o", outputPath,
		"--resources-dir", filepath.Join(dir, "missing-resources"),
		"--model-dir", filepath.Join(dir, "missing-model"),
	}, &stdout, &stderr)

	if code == 0 {
		t.Fatalf("exit code = 0, want nonzero; stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if got := stderr.String(); !strings.Contains(got, "Column 'address' not found") {
		t.Fatalf("stderr = %q, want missing address column", got)
	}
}
