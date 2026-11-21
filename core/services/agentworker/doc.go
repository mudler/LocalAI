// Package agentworker serves the control plane of an agent worker over the
// tunnel it holds to a frontend.
//
// An agent worker runs no backend processes and stages no files. It creates the
// MCP sessions that a frontend cannot (a stdio server under docker), runs tools
// against them and answers discovery. On NATS each request arrives on a subject
// and a queue group picks the worker. On the tunnel the frontend picks the
// worker and sends the request over the stream it opens, so this package
// answers requests and chooses nothing.
package agentworker
