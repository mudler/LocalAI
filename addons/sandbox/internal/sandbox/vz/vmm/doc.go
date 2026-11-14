// Package vmm runs one Linux virtual machine with Apple's
// Virtualization.framework from a pure-Go process. It is the only package of
// the add-on that talks to the framework, so the binding behind it can change
// without a change in the backend.
//
// The binding is github.com/tmc/apple, which calls the Objective-C runtime
// through purego. It needs no cgo, so the add-on builds for macOS from any
// host. The process that runs a VM needs the com.apple.security.virtualization
// entitlement (see scripts/addons/package.sh).
//
// A VM lives in an XPC service of the framework as a child of the process
// that made it. When the process exits for any reason, its VMs stop, and
// nothing can attach to them again.
//
// The machine has no network device. It has a read-only root disk, a
// read-write upper disk, a read-only virtio-fs share with the tag "lower", a
// virtio socket device, a virtio console that writes to a file, and an
// entropy device. Every call into the framework runs on one serial dispatch
// queue per VM.
package vmm
