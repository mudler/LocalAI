// Package triggers starts work without a user: on a schedule, or when a file
// arrives in a folder of a workspace.
//
// A Trigger names its owner, a prompt, and either a Schedule or a folder. A
// Runner watches the triggers in a Store and, when one is due, claims a Run and
// asks the host to start it. The package does not know what a run is: the host
// gives the Runner a Start function (for example, one that opens a chat for the
// owner and sends the prompt) and reports the end of a run with Runner.Finish.
//
// A due time or a file version starts a run at most once, also across
// restarts: Store.Claim refuses a run whose trigger and DueKey already exist.
// A run that was missed while the host was down is caught up once. A change
// made by the chat of a run of a trigger never starts the same trigger. The
// Runner finds the run of a chat through Store.RunByChat, and keeps the chats
// of the runs it started in memory too, until the feed has passed the end of
// the run, for the case that the Store could not record the chat. That memory
// does not survive a restart. The guard is lost only when the write that
// records the chat fails and the host restarts before the last writes of the
// run are read.
//
// The package imports the standard library and workspace only. The Store
// contract is checked by triggerstest.RunStoreSuite; the JSON and GORM stores of
// the chats are in this package too (json.go, gorm.go). Rules reads the
// AGENTS.md files that apply to a folder.
package triggers
