import { retryRead } from './lib/retry.js'

// The calls of the Tables UI, typed. Every one goes through the host SDK,
// which binds it to this add-on and its installed generation: this module
// has no address, no header and no raw request. Reads are typed reads of a
// declared tool; every change is sdk.resources.apply, which the host admits
// under the person's role, the revision they read and the add-on's receipt.
export function createApi(ctx) {
  const { sdk } = ctx
  // Reads wait out a moment of unavailability; changes never do.
  const again = (fn) => retryRead(fn, { signal: sdk.signal })
  const read = (resourceId, args, options) => again(() => sdk.api.read({ resourceId, arguments: args }, options))
  return {
    list: (options) => again(() => sdk.resources.list(options)),
    create: (name, options) => sdk.resources.create({ arguments: { name } }, options),
    rename: (id, name, revision, options) => sdk.resources.rename(id, { name, revision }, options),
    remove: (id, confirm, revision, options) => sdk.resources.remove(id, { confirm, revision }, options),
    grants: (id, options) => again(() => sdk.resources.grants(id, options)),
    setGrant: (id, userId, role, options) => sdk.resources.setGrant(id, userId, role, options),
    revokeGrant: (id, userId, options) => sdk.resources.revokeGrant(id, userId, options),
    searchUsers: (id, query, options) => again(() => sdk.resources.searchUsers(id, query, { limit: 8, ...(options || {}) })),
    operation: (id, kind, operationId, options) => again(() => sdk.resources.operation(id, kind, operationId, options)),
    inspect: (id, options) => read(id, { tool: 'databases-inspect' }, options),
    rows: (id, query, options) => read(id, { tool: 'rows-query', ...query }, options),
    // The CSV export of the view: the host turns the add-on's file into a
    // private artifact and answers with its identifier.
    exportCsv: (id, args, options) => sdk.files.read({ resourceId: id, arguments: args }, options),
    change: (id, tool, args, revision, options) => sdk.resources.apply(id, { tool, arguments: args, revision }, options),
    selection: {
      get: (conversationId, options) => again(() => sdk.resources.selection.get(conversationId, options)),
      set: (conversationId, id, options) => sdk.resources.selection.set(conversationId, id, options),
      clear: (conversationId, options) => sdk.resources.selection.clear(conversationId, options),
    },
  }
}
