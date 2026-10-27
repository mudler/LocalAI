import { pageStatus } from './lib/model.js'

// The state of the sandbox on the main page, for every user: where commands
// run, or that no backend works, or that they run on this machine with no
// isolation. An administrator also gets the reason each backend does not work.
export function createPageStatus(sdk, t) {
  const { React } = sdk
  const h = React.createElement
  // eslint-disable-next-line no-unused-vars
  const { useEffect, useState } = React

  function SandboxPageStatus({ info }) {
    if (info.state === 'unknown') return <p data-testid="sandbox-page-unknown">{t('sandbox.unknown')}</p>
    if (info.state === 'host') {
      return <p className="sandbox-page__notice" role="alert" data-testid="sandbox-page-host">{t('sandbox.host')}</p>
    }
    if (info.state === 'cannot') {
      return (
        <div data-testid="sandbox-page-cannot">
          <p className="sandbox-page__notice" role="alert">{t('sandbox.cannot')}</p>
          {info.reasons.length > 0 && (
            <div>
              <h2 className="sandbox-page__subtitle">{t('sandbox.reasons')}</h2>
              <ul className="sandbox-page__reasons">
                {info.reasons.map(r => <li key={r.name} data-testid="sandbox-page-reason"><strong>{r.name}</strong>: {r.reason}</li>)}
              </ul>
            </div>
          )}
        </div>
      )
    }
    return <p data-testid="sandbox-page-isolated">{t('sandbox.isolated').replace('{{backend}}', info.backend)}</p>
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
