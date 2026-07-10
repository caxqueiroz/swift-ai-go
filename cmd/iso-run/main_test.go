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

func TestParseArgsAcceptsPostalDBPath(t *testing.T) {
	var stderr bytes.Buffer

	opts, err := parseArgs([]string{
		"--input-path", "/tmp/in.txt",
		"--output-path", "/tmp/out.json",
		"--postal-db", "/tmp/postal.db",
	}, &stderr)
	if err != nil {
		t.Fatalf("parseArgs() error = %v; stderr=%q", err, stderr.String())
	}
	if opts.postalDBPath != "/tmp/postal.db" {
		t.Fatalf("postalDBPath = %q, want /tmp/postal.db", opts.postalDBPath)
	}
}

func TestParseArgsAcceptsClientCSVShape(t *testing.T) {
	var stderr bytes.Buffer

	opts, err := parseArgs([]string{
		"--input-path", "/tmp/in.csv",
		"--output-path", "/tmp/out.csv",
		"--delimiter", ";",
		"--address-column", "customer_address",
		"--id-column", "client_ref",
		"--suggested-country-column", "country_hint",
		"--force-suggested-country-column", "pin_country",
	}, &stderr)
	if err != nil {
		t.Fatalf("parseArgs() error = %v; stderr=%q", err, stderr.String())
	}

	if opts.delimiter != ";" {
		t.Fatalf("delimiter = %q, want ;", opts.delimiter)
	}
	if opts.addressColumn != "customer_address" {
		t.Fatalf("addressColumn = %q, want customer_address", opts.addressColumn)
	}
	if opts.idColumn != "client_ref" {
		t.Fatalf("idColumn = %q, want client_ref", opts.idColumn)
	}
	if opts.suggestedCountryColumn != "country_hint" {
		t.Fatalf("suggestedCountryColumn = %q, want country_hint", opts.suggestedCountryColumn)
	}
	if opts.forceSuggestedCountryColumn != "pin_country" {
		t.Fatalf("forceSuggestedCountryColumn = %q, want pin_country", opts.forceSuggestedCountryColumn)
	}
}

func TestSelectReaderWithOptionsReadsClientCSVShape(t *testing.T) {
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "addresses.csv")
	if err := os.WriteFile(inputPath, []byte(strings.Join([]string{
		"client_ref;customer_address;country_hint",
		"cust-1;10 Downing St;gb",
	}, "\n")), 0o644); err != nil {
		t.Fatalf("write input: %v", err)
	}

	readSamples, err := selectReaderWithOptions(inputPath, cliOptions{
		delimiter:              "semicolon",
		addressColumn:          "customer_address",
		idColumn:               "client_ref",
		suggestedCountryColumn: "country_hint",
	})
	if err != nil {
		t.Fatalf("selectReaderWithOptions() error = %v", err)
	}

	got, err := readSamples()
	if err != nil {
		t.Fatalf("readSamples() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("sample count = %d, want 1", len(got))
	}
	if got[0].ID != "cust-1" || got[0].Text != "10 Downing St" || got[0].SuggestedCountry != "GB" {
		t.Fatalf("sample = %#v, want client-ref sample with GB hint", got[0])
	}
}

func TestParseDelimiterRejectsAmbiguousValues(t *testing.T) {
	_, err := parseDelimiter("||")
	if err == nil {
		t.Fatal("parseDelimiter() error = nil, want error")
	}
}
