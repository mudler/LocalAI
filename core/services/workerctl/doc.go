// Package workerctl holds the request and reply payloads of the worker control
// verbs (backend install, upgrade, list, stop and delete, model stop, unload
// and delete, running models, and the file staging verbs).
//
// The payloads live apart from any carrier so that every transport that serves
// or sends a verb decodes the same structs. The package imports only the
// standard library, which keeps it a leaf that both the controller and the
// worker can depend on without an import cycle.
package workerctl
