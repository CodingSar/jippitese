// Package compiler holds the toolchain pin of spec 13 rule 11. The
// subpackages implement the compiler; cmd/jip is the command.
package compiler

// GoVersion is the minimum Go language version the generated code needs
// and the `go` line of the generated go.mod.
const GoVersion = "1.24"

// GoToolchain is the pinned toolchain written to the generated go.mod.
// Bump it deliberately; JIP_GO may name a newer toolchain.
const GoToolchain = "go1.24.0"
