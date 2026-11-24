// The browser's client of the pin routes, bound to the page's base path.
import { apiUrl } from './basePath'
import { createPinsApi } from './pinsApi'

export const pinsApi = createPinsApi({ base: apiUrl })
