import { createContext } from './ctx.jsx'
import { createApi } from './api.js'
import { createFineTune } from './FineTune.jsx'
import * as tools from '../../../internal/tools-ui/src/tools.js'
import { readSse } from '../../../internal/tools-ui/src/sse.js'
import { createToolSteps } from '../../../internal/tools-ui/src/ToolSteps.jsx'
import { createChecks } from '../../../internal/tools-ui/src/Checks.jsx'
import { createStageLine } from '../../../internal/tools-ui/src/StageLine.jsx'
import { createJobLog } from '../../../internal/tools-ui/src/JobLog.jsx'
import { createToolOff } from '../../../internal/tools-ui/src/ToolOff.jsx'

// What the page and the shared job views are built from, for one SDK.
function build(sdk) {
  const ctx = createContext(sdk)
  const api = createApi(ctx)
  const shared = {
    tools, readSse,
    ToolSteps: createToolSteps(ctx), Checks: createChecks(ctx), StageLine: createStageLine(ctx),
    JobLog: createJobLog(ctx), ToolOff: createToolOff(ctx),
  }
  return { ctx, api, parts: createFineTune(ctx, api, shared) }
}

// The second argument is for tests: the host passes the SDK only.
export function createAddon(sdk, options = {}) {
  const { ctx, parts } = build(sdk)
  // The JSX of every factory compiles to calls of `h`; ctx binds it once and
  // each factory reads it from ctx. This `h` is for the JSX in this file.
  // eslint-disable-next-line no-unused-vars
  const { h } = ctx
  const pages = {
    jobs: parts.FineTune,
    ...options.pages,
  }

  function Component({ page, path }) {
    const Page = pages[page]
    return Page ? <Page path={path || ''} /> : null
  }
  return { Component, components: {}, chatActions: [], dispose() {} }
}

// For tests: the context, the API and the parts of the page, built the way
// createAddon builds them.
export const __partsFor = (sdk) => build(sdk)
export const __ctxFor = (sdk) => createContext(sdk)
