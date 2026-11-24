// SPDX-License-Identifier: MIT

// Package sdk is the parent of the packages an add-on needs to run as a
// separate process under LocalAI. An add-on can live in its own repository
// and its own Go module and import these packages from
// github.com/mudler/LocalAI.
//
// The packages are:
//
//   - protocol: reads the startup object, serves the add-on protocol and
//     exits with the parent process.
//   - hostclient: calls the host operations an add-on may use.
//   - idle: a runtime with no tools, for add-ons that only add a UI.
//   - atomicfile: writes a file so that a reader never sees half of it.
//   - argutil: reads the typed arguments of a tool call.
//
// Stability. The exported API of these packages follows the manifest api
// range (contract.HostAPIVersion). A change that breaks a caller needs a new
// api version. Adding an exported name is not a break. A test records the
// exported names of every package, so an accidental change fails in CI.
//
// Imports. These packages import only the standard library, packages under
// pkg/ and external modules. They never import core/ or an internal path,
// because Go refuses internal imports from another module. A test checks this.
package sdk
