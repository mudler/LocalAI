// The browser's client of the plugin routes, bound to the page's base path.
import { apiUrl } from './basePath'
import { createPluginsApi } from './pluginsApi'

export const pluginsApi = createPluginsApi({ base: apiUrl })
