# Host observation boundary (T8–T9)

`(*Client).ReadObservation(ctx, envelope, requestID)` is the dedicated host-only
status read API. It returns only a validated `contract.ExecutionStatus`; errors
return unknown/unknown and never return raw results or output handles. The read
is bounded by the parent deadline, five seconds and the runtime read timeout.

## Authenticated owner read (T9)

`Gateway.ExecutionStatusRead(ctx, actor, request)` is the host-only owner API.
The versioned request carries the observation ID and every component of binding B:
authenticated actor, addon, generation, incarnation, execution tool, status tool,
conversation and current provider-selection identity. The response echoes that identity with validated status.
No request field carries arguments, grants, administrator rights, or an operation ID.
The owner generates the runtime operation ID independently of the observation ID.
The wire envelope uses `{"tool":"declared-status-tool"}` for actual status input `{}`.

The owner requires `AuthorizeExecutionStatus` on every admission and before publishing a reply.
This host callback must check current selection, execution feature permission,
addon access, and authority to replace the builtin provider. An absent callback denies reads.
The addon admission checks resource-independent account access. The verified manifest
must bind the execution action to this exact resourceless, `uiOnly` status read.
`Manager.Runtime` also checks installed/enabled lifecycle and required host operations.
The status path does not call `ToolAdapter.Execute` or change its `uiOnly` exclusion.

The owner captures one actual client. `Client.AdmitObservation(generation, incarnation)`
checks identity and accepting state under the same mutex that serializes withdrawal.
Its idempotent lease pins that client through dispatch and consumption without holding
manager or supervisor locks across network I/O. Observation leases do not consume the
ordinary request concurrency allowance. Withdrawal rejects later admissions and cancels
pending observations. A replacement never receives a request bound to the old client.
The owner checks current lifecycle, client identity, selection, and permission again
before returning a positive value. Errors return unknown/unknown.

The existing authenticated mTLS carrier adds `execution-status-read-v1` as a typed call.
Its request and response DTOs have their own version 1; addon protocol v1 remains unchanged.
The carrier rejects mixed call variants, identity mismatches, unknown fields, duplicate
keys, and noncanonical keys. Transport capability support cannot grant selection authority.
Older owners without this call return unavailable. Reads have no retries or positive cache.

## T10 integration responsibilities

Generate the observation ID in the host request shell, never from addon or browser input.
Share one observation within that enclosing request. T9 does not implement coalescing
or the reducer shell. Cancel the parent context when the request closes or authority changes.
The owner and runtime independently cap each read at five seconds.
Recheck the current binding when consuming a returned observation, including after carrier delivery.
A reply can become stale after the owner's final check; it is never execution authorization.
Application integration must configure the owner callback from authoritative host selection
and permission state. Until then, the default path denies observations.

The client records observation ownership in the authoritative in-flight entry.
The incarnation-authenticated broker checks it before dispatching any operation
class (including inbound), without modifying grants. A denied callback invalidates
the status reply. Outputs and unknown payload fields, including artifact handles,
are rejected without opening, adopting or publishing anything. Independently
admitted concurrent ordinary reads/actions retain their normal authority.

Live records and cancellation registrations are removed idempotently on completion,
error, timeout, cancellation or withdrawal. Retired correlations are kept until
the incarnation's callback endpoint stops admission and all admitted HTTP
handlers drain. `http.Server.Close` alone does not wait for active handlers.
The supervisor stops admission, closes the server to cancel network requests,
drains admitted requests, then forgets correlations. Pending decoding requests
retain their denial. Dispatch admission is serialized with endpoint Stop for
all scopes, without holding a lifecycle mutex across a handler or drain.
Pre-Stop admissions may finish, but retirement cannot complete underneath them.
This prevents a late callback from recovering credential-only inbound authority.
Retention is bounded to 4096 records per InFlight index; observation admission
fails closed at capacity, rather than
evicting a denial and restoring authority. Ordinary work is not capacity-limited
by this observation index. A long-lived runtime reaching the limit needs restart
before further observations; no cross-request positive result cache exists.

Stop never waits for handlers. Drain runs in the endpoint cleanup goroutine,
not in process termination or under a supervisor lock. A handler that ignores
cancellation can delay its endpoint's drain and reclamation indefinitely, but
cannot restore admission or block another endpoint. Retained correlations stay
within the same 4096-record bound; no global closed-incarnation tombstones are
created. The endpoint and its cleanup goroutine remain until its handlers exit.

This is a host-mediated effect boundary, not a process sandbox or purity
attestation. An addon may still mutate its own memory/files or perform its own I/O.
Uncorrelated independently authorized inbound work remains ordinary inbound work;
correlation is not evidence about arbitrary process behavior.

## Owner binding API (bounded transport milestone)

`Gateway.BindingForActor(ctx, actor, ExecutionBindingClaim{ConversationID: id})`
accepts only a conversation claim. The optional `ResolveExecutionBinding` callback
runs on the owner and must load actor-owned authoritative context. A missing
resolver denies the request. `ValidateExecutionBinding` resolves the claim again
and compares every binding component. The optional ToolAdapter backend forwards
these host-only calls; it does not change discovery or ordinary execution.

The authenticated carrier negotiates `execution-binding-v1`, uses version 1
canonical-key DTOs, and rejects authority fields and mixed dispatch doors.
`conversationId` is optional in the older observation binding DTO for compatibility,
but is required for the new binding API. Binding lookup has a five-second budget.
Transport support is not execution authority or permission to observe.

This milestone tests an injected owner-trusted resolver, including real mTLS.
It does not install an application resolver, instrument agent/conversation stores,
or connect request/session lifecycle hooks. Product binding remains denied until
an owner resolver can capture authoritative mutation revisions and eligibility.
