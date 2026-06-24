# Scheduling Rule Editing and Node Label Reference

## Summary

Improve the React scheduling view so cluster operators can edit existing scheduling rules and inspect node labels without moving back and forth to the Nodes page.

The scheduling page will gain a compact, collapsible node-label reference above the rules table. It will also gain an Edit action that opens the existing scheduling form with the selected rule prefilled. The model name will remain locked while editing because it identifies the rule being updated.

## Goals

- Let operators update an existing scheduling rule in place.
- Make the labels available on each node visible from the scheduling workflow.
- Keep the label reference usable for clusters with many nodes.
- Preserve the existing scheduling API and node API contracts.
- Keep the scheduling rules usable when node-label loading fails.

## Non-goals

- Editing node labels from the scheduling page.
- Renaming the model associated with an existing scheduling rule.
- Adding backend endpoints or changing scheduling semantics.
- Adding a separate scheduling documentation page for this discoverability enhancement.

## User Experience

### Node label reference

A collapsible **Node labels** section appears above the scheduling rules. It loads node data through the existing `nodesApi.list()` client and groups labels by node so operators can tell which selectors match which machines.

The expanded section contains:

- A fuzzy search field that matches node names, label keys, label values, and complete `key=value` text.
- A summary showing the visible result count and total matching node count.
- Node groups containing the node name, operational status, and its `key=value` label chips.
- Five matching nodes initially.
- A **Show 20 more** action when additional matches exist.

Changing the search query resets the visible limit to five. Clearing the query restores the unfiltered result set. The reference can be collapsed to preserve vertical space.

The matching implementation should be lightweight and local to the page. It should normalize searchable node data and support forgiving, case-insensitive token matching without adding a large dependency solely for this feature.

### Editing a scheduling rule

Each scheduling-rule row gains an **Edit** action beside **Delete**. Selecting Edit opens the existing scheduling form above the table and populates every editable field from the selected configuration:

- Scheduling mode
- Node selector
- Minimum and maximum replicas
- Routing policy
- Prefix-cache thresholds

The model selector is replaced by, or presented as, a visibly locked model field while editing. This prevents a rename from creating a second rule while leaving the original in place.

Only one add or edit form may be open at a time. Opening Add clears edit state; opening Edit closes any blank Add form. Cancel closes the form and discards its local changes.

Saving continues to use `nodesApi.setScheduling()`. On success, the page closes the form, shows the existing success toast, and refreshes the scheduling rules. On failure, it shows the error toast and keeps the populated form open so the operator does not lose changes.

## Component Design

### Scheduling form

Refactor `SchedulingForm` to accept an optional existing scheduling configuration. Initial form state will be derived from that configuration, including conversion of a serialized `node_selector` when necessary and derivation of the current mode from `spread_all`, replica values, and selector presence.

The form remains responsible for validation and for producing the existing scheduling request shape. The parent remains responsible for API calls, toast notifications, refreshes, and deciding whether the form is adding or editing.

### Node label reference

Add a focused scheduling-page component for label discovery. It receives node data and owns only presentation state:

- Expanded or collapsed
- Search query
- Visible result limit

Small pure helpers will normalize a node's searchable text and calculate filtered results. Node fetching remains in the scheduling page so loading and retry behavior stay next to the existing scheduling fetch lifecycle.

### Styling

Add scheduling-specific classes to `core/http/react-ui/src/App.css`. Reuse existing design-system tokens and button, input, badge, stack, and text primitives. Do not add static inline styles.

On wide screens, node groups use a responsive compact grid. On narrow screens, they collapse to one column. Search, collapse, pagination, and row actions remain keyboard accessible and expose explicit accessible names.

## Data Flow

1. The page mounts and independently requests scheduling configurations and nodes.
2. Scheduling configurations populate the rules table.
3. Node data populates the label reference; local search and limiting do not trigger network requests.
4. Selecting Edit copies one rule into form state and locks its model identity.
5. Saving posts the existing scheduling payload and refreshes the scheduling list.
6. Node-label retry repeats only the node request and does not disturb scheduling rules or an open scheduling form.

## States and Error Handling

- **Node loading:** Show a compact loading state inside the reference. Do not block the rules table.
- **No nodes:** Explain that no nodes are available yet.
- **Node without labels:** Include it in node-name search results and display **No labels**.
- **No search matches:** Show a clear empty result while preserving the query.
- **Node fetch failure:** Show an inline error with Retry. Scheduling remains fully usable.
- **Malformed selector:** Preserve the current defensive rendering behavior and avoid crashing the edit form; treat an unparseable selector as empty while keeping the rule visible.
- **Save failure:** Preserve all form values and show the existing error toast.
- **Save success:** Close the form and refresh the rules.

## Verification

Add or extend a focused Playwright scheduling spec to cover:

- Labels grouped under the correct nodes.
- Search by node name.
- Search by complete `key=value` text.
- Five-node initial limit and **Show 20 more** expansion.
- Empty-cluster, unlabeled-node, no-match, and failed-loading states.
- Edit opening with the complete rule prefilled.
- Locked model identity during editing.
- Updated values sent through the existing scheduling endpoint.
- Failed saves preserving the open form.

Run the focused Playwright spec, the React inline-style lint, and the production React build. Long repository-wide builds are outside the scope of this frontend-only change.

## Acceptance Criteria

- An operator can edit and save any existing scheduling rule without deleting and recreating it.
- The model identity cannot be changed while editing.
- An operator can inspect labels grouped by node without leaving Scheduling.
- The label reference remains compact with many nodes and supports forgiving search plus progressive expansion.
- A node API failure does not prevent viewing or editing scheduling rules.
- The enhancement works at narrow viewport widths and is keyboard accessible.
