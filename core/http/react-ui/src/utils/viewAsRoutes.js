// What follows "View as". This is the one list: the request guard, the nav
// markers, the page chip and the page notice all read it, so a route that
// starts to honour ?user_id= on the server is added here and nowhere else.
//
// The list comes from the server code. A route follows only when its handler
// resolves its user through auth.EffectiveUserID or the project owner helper
// (core/http/endpoints/studio/projects.go, projectOwner), which both read
// ?user_id= for an administrator. A route that reads the caller only is not
// listed: sending user_id to it changes nothing and would hide that the page
// still shows the administrator's own data.
//
// Pure: no React, no imports, so a node test drives it directly.

export const USER_ID_PARAM = 'user_id'

// Each entry is one stream of data the server scopes by user.
//   api    patterns of the request path (without the app's base path)
//   pages  patterns of the page path, the pages that show that data
//   nav    ids of primaryDestinations that get the marker
//
// Projects: every /api/chats/projects route except the shared ones
// (/api/chats/projects/shared...), which belong to the caller. Collections
// are not here: their list and create routes read the caller only, so the
// Knowledge page cannot follow, whatever its per-collection routes do.
export const FOLLOWING = Object.freeze([
  Object.freeze({
    id: 'projects',
    api: Object.freeze([/^\/api\/chats\/projects(?:\/(?!shared(?:\/|$))[^/]+(?:\/.*)?)?\/?$/]),
    pages: Object.freeze([/^\/app\/projects(?:\/(?!shared(?:\/|$))[^/]+)?\/?$/]),
    nav: Object.freeze(['projects']),
  }),
])

// Whether a request path (no base path, no query) is one the server scopes by
// user_id.
export function followsApi(path) {
  return FOLLOWING.some(f => f.api.some(re => re.test(path)))
}

// Whether the page at this path shows the viewed user's data.
export function followsPage(pathname) {
  return FOLLOWING.some(f => f.pages.some(re => re.test(pathname)))
}

// Whether a nav item (an id of primaryDestinations) gets the marker.
export function followsNav(id) {
  return FOLLOWING.some(f => f.nav.includes(id))
}
