// Package triggerstest checks a triggers.Store.
//
// RunStoreSuite is the contract: every Store passes it. For the tests of a
// Runner or of a host that need no database, triggers.NewJSON("") is a Store
// in memory.
package triggerstest
