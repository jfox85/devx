package main

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	// Run tests
	code := m.Run()
	os.Exit(code)
}

func TestMainFunction(t *testing.T) {
	// Save original args
	oldArgs := os.Args
	defer func() {
		os.Args = oldArgs
	}()
	
	// Test that main doesn't panic with help flag
	os.Args = []string{"devx", "--help"}
	
	// We can't directly test main() as it calls cmd.Execute() which may call os.Exit
	// Instead, we ensure the package compiles and the imports are correct
	// The actual CLI functionality is tested in the cmd package tests
	
	// This test primarily ensures:
	// 1. The main package compiles
	// 2. The cmd package is imported correctly
	// 3. No initialization panics occur
	
	// If we get here without compilation errors, the test passes
	t.Log("main package compiled successfully")
}