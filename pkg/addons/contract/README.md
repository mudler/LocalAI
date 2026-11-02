# Typed addon contract

`ParseManifest` is the strict v1 parser used by staging and package verification.
Unknown fields, duplicate keys, invalid declarations, and unsupported compatibility ranges fail validation.
The typed declarations remain additive strict v1 fields, not a new protocol version.

- `capabilities.ui.homeCommands` names installed pages, never tools or install actions.
- `capabilities.executionProvider` names execution tools and one declared UI-only read status tool.
- `capabilities.hostOperations.required` and `.optional` request exact operation names, never grants.

`ValidHostOp` validates syntax only. The host's registered vocabulary determines support.
Administrator policy and request admission remain authoritative, including for optional operations.
Status is advisory; an isolation report does not prove network enforcement or authorize execution.

See [the addon guide](../../../docs/content/features/addons.md#optional-typed-manifest-extensions)
for JSON examples, bounds, collision rules, lifecycle behavior, policy migration, and old-host staging refusal.
Old hosts must retain the old package when an extension-bearing upgrade is refused.
Release maintainers select the minimum supporting release and the duration of any legacy package support.

Typed owner observations currently support local-owner JSON selections only.
Named-user and SQL selections remain unavailable and fail closed; they are not completed scope.
