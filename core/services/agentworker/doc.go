// Package agentworker serves the control plane of an agent worker over the
// tunnel it holds to a frontend.
//
// An agent worker runs no backend processes and stages no files. It creates the
// MCP sessions that a frontend cannot (a stdio server under docker), runs tools
// against them, answers discovery, and executes the agent runs and MCP CI jobs
// that the frontend hands to it. On NATS each of these arrived on a subject and
// a queue group picked the worker. On the tunnel the frontend picks the worker
// and sends the request over the stream it opens, so this package answers
// requests and chooses nothing.
//
// A run is a streaming request. The events of the run are the lines of the
// response, and the last line is the reply. The reply is the result of the run,
// so it cannot be published to nobody, and a response that ends without a reply
// line means the link broke: it says nothing about the work.
package agentworker
