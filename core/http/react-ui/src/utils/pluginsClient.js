// The browser's client of the plugin routes, bound to the page's base path.
// Its requests carry the incognito marker while an incognito chat is open,
// like every other request of the page (utils/incognito.js): the plugin
// switch of the chat and the plugin lists it reads are part of that chat.
import { apiUrl } from './basePath'
import { incognitoHeaders } from './incognito'
import { createPluginsApi } from './pluginsApi'

export const pluginsApi = createPluginsApi({
  base: apiUrl,
  fetch: (url, init = {}) => globalThis.fetch(url, { ...init, headers: incognitoHeaders(init.headers) }),
})
