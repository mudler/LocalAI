# Request-local execution observation shell

`New(parent, binding, q, eligibility, IO, Clock)` starts a fresh **Unbound**
model and submits its authoritative `Bind`. The caller must generate Q and
obtain the binding from the owner; this package does not admit providers.
Call `defer request.Close()` at the enclosing request or turn boundary.
Nested prompt, status and egress consumers call `Observe` on that same object.
No context attachment or production entry-point integration exists yet.

`IO.Read` handles the reducer's sole `Read` effect. It must preserve B/Q through
the carrier, decode the strict typed contract, and honor cancellation.
`IO.Validate` checks current owner authority, current selection and exact runtime
identity. It runs after the read and before each settled observation consumption.
Nil callbacks cannot produce a positive observation. Status never grants tool
access or proves network-policy enforcement.

The shell serializes actual `executionstatus.Reduce` events. `Publish` wakes
consumers, `Cancel` cancels the read, and `Read` starts its I/O goroutine. A
five-second maximum deadline starts at construction and respects an earlier
parent deadline. A clock callback supplies `Tick`; parent cancellation supplies
terminal `Close`. Explicit lifecycle/permission/provider changes call
`Invalidate`. There is deliberately no rebind method: a new admission lifetime
requires a new request object. Settled failures cannot retry because the reducer
ignores subsequent probes (R7), not because of a shell retry flag.

A consumer's own canceled wait returns unknown without closing other consumers.
Close/Invalidate stop waiting consumers without waiting for an uncooperative I/O
callback. Late callbacks still enter the reducer and cannot restore withdrawn or
closed values. I/O implementations remain responsible for stopping their work.
Validation and consumption cannot be atomic with subsequent execution: execution
must retain its independent owner admission and freshness checks.

`Trace` provides detached reducer transitions for diagnostics and tests. Binding
identities must be opaque tokens, never credentials. Model values carry readiness
and isolation only; typed wire explanations and backend/API projections belong
to the subsequent integration task.

Focused verification (with the repository's prepared offline environment):

```sh
go test -race -count=1 ./core/services/studio/executionrequest ./pkg/addons/model/executionstatus
go run ./pkg/addons/model/executionstatus/cmd/viewer -check
```
