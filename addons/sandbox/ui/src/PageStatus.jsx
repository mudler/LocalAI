import { pageStatus } from './lib/model.js'

// The state of the sandbox on the main page, for every user: where commands
// run, or that no backend works, or that they run on this machine with no
// isolation. An administrator also gets the reason each backend does not work.
export function createPageStatus(sdk, t) {
  const { React } = sdk
  const h = React.createElement
  // eslint-disable-next-line no-unused-vars
  const { useEffect, useState } = React

  // eslint-disable-next-line no-unused-vars
  const { Callout, List, ListRow, Code } = sdk.ui

  function SandboxPageStatus({ info }) {
    if (info.state === 'unknown') return <Callout tone="info" testId="sandbox-page-unknown">{t('sandbox.unknown')}</Callout>
    if (info.state === 'host') {
      return <Callout tone="warn" role="alert" testId="sandbox-page-host">{t('sandbox.host')}</Callout>
    }
    if (info.state === 'cannot') {
      return (
        <div className="sandbox-page__cannot" data-testid="sandbox-page-cannot">
          <Callout tone="error" role="alert">{t('sandbox.cannot')}</Callout>
          {info.reasons.length > 0 && (
            <div className="sandbox-page__reasons">
              <h2 className="sandbox-page__subtitle">{t('sandbox.reasons')}</h2>
              <List label={t('sandbox.reasons')}>
                {info.reasons.map(r => (
                  <ListRow key={r.name} testId="sandbox-page-reason" title={r.name} details={<Code>{r.reason}</Code>} detailsLabel={t('sandbox.settings.details')} />
                ))}
              </List>
            </div>
          )}
        </div>
      )
    }
    return <Callout tone="ok" testId="sandbox-page-isolated">{t('sandbox.isolated').replace('{{backend}}', info.backend)}</Callout>
  }

  function PageStatus() {
    const [info, setInfo] = useState(null)
    useEffect(() => {
      let live = true
      sdk.api.read({ arguments: { tool: 'status' } })
        .then(s => { if (live) setInfo(pageStatus(s)) })
        .catch(() => { if (live) setInfo(pageStatus(null)) })
      return () => { live = false }
    }, [])
    if (info === null) return <p aria-busy="true" data-testid="sandbox-page-loading">{t('sandbox.loading')}</p>
    return <SandboxPageStatus info={info} />
  }

  return { PageStatus, SandboxPageStatus }
}
