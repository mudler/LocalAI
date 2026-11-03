// Package contextfiles loads small files of the user's home into the prompt of
// a chat, and reads their details on demand.
//
// It is the host side of a generic mechanism. Any component registers a
// Source: a scope (user, agent or channel), a home-relative path to a short
// index file and a byte cap. Each turn the Loader reads the index of every
// source that applies to the chat and returns one block, under a fixed header
// that tells the model to consult the notes. The details of a topic stay in
// files next to the index and are read with the context_read tool. Nothing in
// this package knows which application started the chat: a plain Studio chat,
// a chat that came from a messaging app and a chat of a saved agent all use
// the same code.
//
// The package only reads. The path that saves a note is a Writer that another
// part of the host plugs in.
package contextfiles
