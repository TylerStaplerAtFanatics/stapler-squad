//go:build darwin

package main

// cgo is imported here only to force the Go toolchain to invoke the external
// (C) linker on Darwin, which is required for CGO_LDFLAGS=-sectcreate to take
// effect. The Go internal linker ignores CGO_LDFLAGS entirely.
import "C"
