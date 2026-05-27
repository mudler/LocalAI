// useOperations now lives in OperationsContext so all consumers
// (OperationsBar, Models, Backends, Chat) share a single poller instead
// of each spinning up its own setInterval against /api/operations.
export { useOperations } from '../contexts/OperationsContext'
