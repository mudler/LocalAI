package agentpool

import (
	"context"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/syncstate"
)

// taskStoreAdapter bridges the existing JobPersister (file- or DB-backed) to the
// generic syncstate.Store the tasks SyncedMap consumes. Only tasks are migrated:
// jobs already converge across replicas via the dispatcher (NATS) plus the DB
// read-through in ListJobs/GetJob, whereas ListTasks read in-memory only and so
// went stale on replicas that did not originate the change.
//
// The adapter reads svc.persister and svc.userID live (rather than capturing
// them) because both are configured by setters - SetDistributedJobStore swaps the
// file persister for the DB one, SetUserID scopes per-user queries - AFTER the
// service, and thus this adapter, is constructed. Reading them at call time means
// the SyncedMap never has to be rebuilt when the persister is swapped.
//
// The SyncedMap value type is schema.Task: the exact shape ListTasks returns, so
// reads need no conversion and REST responses are provably unchanged.
type taskStoreAdapter struct {
	svc *AgentJobService
}

// compile-time assertion that the adapter satisfies the component's Store.
var _ syncstate.Store[string, schema.Task] = (*taskStoreAdapter)(nil)

// List hydrates the map from durable storage on Start/reconnect: the file's task
// list (standalone) or every task row (DB / distributed).
func (a *taskStoreAdapter) List(_ context.Context) ([]schema.Task, error) {
	return a.svc.persister.LoadTasks(a.svc.userID)
}

// Upsert write-through persists a single task created/updated locally; the
// SyncedMap then broadcasts the delta to peers.
func (a *taskStoreAdapter) Upsert(_ context.Context, task schema.Task) error {
	return a.svc.persister.SaveTask(a.svc.userID, task)
}

// Delete write-through removes a task locally; the SyncedMap then broadcasts the
// removal to peers.
//
// The user id travels with the id, read live from the service exactly as List
// and Upsert already read it. A delete by primary key alone reaches every
// tenant's row with that key, and the in-memory map is not the authority on who
// owns a row - the store is - so the predicate belongs on the query and not on
// a lookup before it.
func (a *taskStoreAdapter) Delete(_ context.Context, id string) error {
	return a.svc.persister.DeleteTask(a.svc.userID, id)
}
