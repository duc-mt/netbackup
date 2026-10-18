// ==============================================================================
// Package main implements tests for cmd/resultdemo.
// Author:        Mai Tan Duc <ducmai.network@gmail.com>
// Created:       2026-10-18
// License:       MIT
// ==============================================================================
package main

import (
	"os"
	"testing"
)

func TestMainExecution(t *testing.T) {
	// Divert stdout to keep test logs clean
	oldStdout := os.Stdout
	defer func() { os.Stdout = oldStdout }()
	os.Stdout = os.NewFile(0, os.DevNull)

	// main() executes report.PrintSummaryTable(report.MockResults()) without panics
	main()
}
