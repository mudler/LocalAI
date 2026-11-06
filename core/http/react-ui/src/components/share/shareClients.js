// SPDX-License-Identifier: MIT
// The two transports of the share dialog. The dialog takes a client with four
// calls and does not know which routes are behind them:
//
//   grants()                 -> { grants: [{ userId, displayName, role }] }
//   setGrant(principal, role)-> the grants after the change
//   revoke(principal)        -> the grants after the change
//   searchUsers(query)       -> { users: [{ id, displayName }] }
//
// No React and no network: the node unit tests drive both directly.

export const SEARCH_LIMIT = 8

// kindShareClient is the client of a resource of a kind that is not an
// add-on, over /api/shares (sharesApi in utils/api.js).
export function kindShareClient(api, kind, resourceId) {
  return Object.freeze({
    grants: (options) => api.grants(kind, resourceId, options),
    setGrant: (principal, role, options) => api.setGrant(kind, resourceId, principal, role, options),
    revoke: (principal, options) => api.revokeGrant(kind, resourceId, principal, options),
    searchUsers: (query, options) => api.searchUsers(kind, resourceId, query, { limit: SEARCH_LIMIT, ...(options || {}) }),
  })
}

// addonShareClient is the client of an add-on resource, over the SDK's
// sdk.resources, which binds every call to the add-on and its generation.
export function addonShareClient(resources, resourceId) {
  return Object.freeze({
    grants: (options) => resources.grants(resourceId, options),
    setGrant: (principal, role, options) => resources.setGrant(resourceId, principal, role, options),
    revoke: (principal, options) => resources.revokeGrant(resourceId, principal, options),
    searchUsers: (query, options) => resources.searchUsers(resourceId, query, { limit: SEARCH_LIMIT, ...(options || {}) }),
  })
}
