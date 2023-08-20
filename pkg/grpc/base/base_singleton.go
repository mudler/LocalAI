package base

// BaseSingleton enables a singleton pattern for the base class
// such as there will be only one request being served at the time.
// This is useful for models that are not thread safe and cannot run
// multiple requests at the same time.
type BaseSingleton struct {
	Base
}

// Locking returns true if the backend needs to lock resources
func (llm *BaseSingleton) Locking() bool {
	return true
}
