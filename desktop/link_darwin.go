//go:build darwin

package main

// Wails v2 references UTType (file-dialog filters) but does not always emit
// the UniformTypeIdentifiers framework link flag, which fails the link step
// with "_OBJC_CLASS_$_UTType ... symbol(s) not found" on some macOS SDK / Go
// toolchain combinations (wails#1066). Linking it explicitly here fixes
// plain `go build` without requiring CGO_LDFLAGS.

/*
#cgo LDFLAGS: -framework UniformTypeIdentifiers
*/
import "C"
