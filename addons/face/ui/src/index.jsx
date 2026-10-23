import { createContext } from './ctx.jsx'
import { createApi } from './api.js'
import { createFaceRecognition } from './FaceRecognition.jsx'
import * as identity from '../../../internal/identity-ui/src/identity.js'

// The second argument is for tests: the host passes the SDK only.
export function createAddon(sdk, options = {}) {
  const ctx = createContext(sdk)
  // The JSX of every factory compiles to calls of `h`; ctx binds it once and
  // each factory reads it from ctx. This `h` is for the JSX in this file.
  // eslint-disable-next-line no-unused-vars
  const { h } = ctx
  const api = createApi(ctx)
  const pages = {
    recognition: createFaceRecognition(ctx, api, identity).FaceRecognition,
    ...options.pages,
  }

  function Component({ page, path }) {
    const Page = pages[page]
    return Page ? <Page path={path || ''} /> : null
  }
  return { Component, components: {}, chatActions: [], dispose() {} }
}

// For tests: the context of an SDK, built the way createAddon builds it.
export const __ctxFor = (sdk) => createContext(sdk)
