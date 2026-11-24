import { createToolSteps } from '../../../src/ToolSteps.jsx'
import { createChecks } from '../../../src/Checks.jsx'
import { createStageLine } from '../../../src/StageLine.jsx'
import { createJobLog } from '../../../src/JobLog.jsx'
import { createToolOff } from '../../../src/ToolOff.jsx'

// A page host built the way an add-on builds one: a context from the SDK, the
// shared factories, and a registration of pages that the shell draws.
function contextFor(sdk) {
  const { React } = sdk
  const h = React.createElement
  return {
    sdk, React, h, Fragment: React.Fragment,
    t: (key, vars) => sdk.locale.t(key, key, vars),
    Icon: sdk.Icon, ui: sdk.ui, viewer: sdk.viewer, basePath: '',
  }
}

export function createAddon(sdk, options = {}) {
  const ctx = contextFor(sdk)
  const { h } = ctx
  const props = options.props || {}
  const ToolSteps = createToolSteps(ctx)
  const Checks = createChecks(ctx)
  const StageLine = createStageLine(ctx)
  const JobLog = createJobLog(ctx)
  const ToolOff = createToolOff(ctx)
  const pages = {
    steps: () => <ToolSteps {...props.steps} />,
    checks: () => <Checks {...props.checks} />,
    stage: () => <StageLine {...props.stage} />,
    log: () => <JobLog {...props.log} />,
    off: () => <ToolOff {...props.off}><p>children</p></ToolOff>,
  }
  function Component({ page }) {
    const Page = pages[page]
    return Page ? <Page /> : null
  }
  return { Component, components: {}, chatActions: [], dispose() {} }
}
