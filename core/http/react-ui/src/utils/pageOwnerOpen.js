// SPDX-License-Identifier: MIT
// Where the owner opens one of their own pages. Today every own page opens at
// its own route. A page that others may edit may leave the owner's own list
// and routes, and then the owner would open it another way (for example
// through the allow step). That decision is open: change this one helper when
// it is made, and nothing else needs to know.
export const ownerOpenPath = (page) => `/app/pages/${encodeURIComponent(page.name)}`
