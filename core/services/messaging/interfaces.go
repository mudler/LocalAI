package messaging

// Publisher publishes JSON-encoded messages to NATS subjects.
type Publisher interface {
	Publish(subject string, data any) error
}
