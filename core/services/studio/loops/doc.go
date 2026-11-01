// Package loops keeps the loops of the chats: a prompt that a chat sends to
// itself again and again, at a fixed interval or when the model asks for the
// next run, until the person stops it or a limit ends it.
//
// A loop belongs to one chat of one owner. The package stores loops and the
// owner's daily budget of loop runs; it does not run anything. The chats
// service runs the loops (one frontend at a time) and reads and writes them
// through a Store. The Store contract is checked by loopstest.RunStoreSuite;
// NewJSON and NewGORM implement it.
//
// A loop is different from a trigger: a trigger starts a new chat on a
// schedule (at most once an hour), a loop repeats a prompt inside the chat
// where it was started, as often as every minute.
package loops
