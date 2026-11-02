# Host observation boundary (T8)

`(*Client).ReadObservation(ctx, envelope, requestID)` is the dedicated host-only
status read API. It returns only a validated `contract.ExecutionStatus`; errors
return unknown/unknown and never return raw results or output handles. The read
is bounded by the parent deadline, five seconds and the runtime read timeout.

## T9 caller responsibilities

Before calling, authenticate the actor and authorize the exact status tool using
ordinary feature/tool read rules. Capture the owner's current Client, generation
and incarnation, and dispatch on that same Client. Check lifecycle/provider
identity again when consuming the reply. Generate the operation ID and observation
request ID on the host, and share one observation within the enclosing request.
Cancel the context when the enclosing request closes. This API does not perform
provider admission, bypass uiOnly filtering, or implement a host-to-host carrier.

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
