import { useContext, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { addonsApi } from '../utils/addonsClient'
import { apiUrl } from '../utils/basePath'
import { AuthContext } from '../context/authState'
import { artifactDownloadPath, artifactFacts, artifactStatusPath, formatBytes, validArtifactId } from './artifactFacts'

const POLL_MS = 500
const POLL_LIMIT = 240

// The host's card for one private file result. It loads the record from the
// host by identifier and draws name, size, expiry and the authenticated
// download of the host route. It never draws a name, a link or a path the
// add-on or the model said. The card forgets what it loaded when the person
// signs out or the host stops answering for the file.
export default function ArtifactCard({ artifactId }) {
  const { t, i18n } = useTranslation('addons')
  const auth = useContext(AuthContext)
  const principal = auth && auth.user ? String(auth.user.id || auth.user.email || '') : ''
  const featureOn = !auth || !auth.authEnabled || (typeof auth.hasFeature === 'function' && auth.hasFeature('addons'))
  const signedOut = !!auth && !auth.loading && !!auth.authEnabled && !auth.user
  const withheld = signedOut || !featureOn

  const [record, setRecord] = useState(null)
  const [phase, setPhase] = useState('loading')
  const [now, setNow] = useState(() => Date.now())
  const [saving, setSaving] = useState('idle')
  const alive = useRef(true)

  useEffect(() => {
    alive.current = true
    setRecord(null)
    if (withheld || !validArtifactId(artifactId)) { setPhase(withheld ? 'withdrawn' : 'unavailable'); return undefined }
    setPhase('loading')
    const controller = new AbortController()
    let timer = null
    let polls = 0
    const load = async () => {
      try {
        const r = await addonsApi.call(artifactStatusPath(artifactId), { signal: controller.signal })
        if (!r || r.id !== artifactId) { setRecord(null); setPhase('unavailable'); return }
        setRecord(r)
        setPhase('ready')
        setNow(Date.now())
        // A transfer still running is asked about again, a bounded number of
        // times; a complete, failed or cancelled one is final.
        if (r.state === 'transferring' && polls++ < POLL_LIMIT) timer = setTimeout(load, POLL_MS)
      } catch (e) {
        if (e && e.name === 'AbortError') return
        setRecord(null)
        setPhase(e && (e.status === 401 || e.status === 403 || e.kind === 'denied' || e.kind === 'unauthenticated') ? 'withdrawn' : 'unavailable')
      }
    }
    load()
    return () => { alive.current = false; controller.abort(); if (timer) clearTimeout(timer) }
  }, [artifactId, principal, withheld])

  const facts = useMemo(() => artifactFacts(record, now), [record, now])
  useEffect(() => {
    if (!facts.expiresAt || facts.expired) return undefined
    const timer = setTimeout(() => setNow(Date.now()), Math.max(250, Math.min(facts.expiresAt - Date.now() + 50, 2 ** 30)))
    return () => clearTimeout(timer)
  }, [facts.expiresAt, facts.expired])

  // The download is the host route, fetched with the person's own session so
  // a refusal (access to the source was withdrawn, the file expired) is told
  // here and not left to a browser error page. The href stays for a copy of
  // the link; it is the same route and needs the same session.
  const save = async (e) => {
    e.preventDefault()
    if (saving === 'busy' || !validArtifactId(artifactId)) return
    setSaving('busy')
    try {
      const r = await fetch(apiUrl(artifactDownloadPath(artifactId)), { credentials: 'same-origin' })
      if (!r.ok) { if (alive.current) setSaving(r.status === 401 || r.status === 403 ? 'denied' : 'failed'); return }
      const blob = await r.blob()
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = facts.name || 'download'
      a.rel = 'noopener'
      document.body.appendChild(a)
      a.click()
      a.remove()
      setTimeout(() => URL.revokeObjectURL(url), 10_000)
      if (alive.current) setSaving('idle')
    } catch {
      if (alive.current) setSaving('failed')
    }
  }

  const state = (() => {
    if (!record) return phase
    if (facts.expired) return 'expired'
    return ['transferring', 'complete', 'failed', 'cancelled'].includes(facts.state) ? facts.state : 'unknown'
  })()

  return (
    <div className="ad-approval ad-pcard ad-artifact" data-testid="artifact-card" data-state={state} data-artifact={validArtifactId(artifactId) ? artifactId : ''}>
      <p className="ad-approval__from">{t('artifact.title')}</p>
      {phase === 'loading' && !record && <p role="status" data-testid="artifact-loading">{t('artifact.loading')}</p>}
      {phase === 'withdrawn' && !record && <p role="alert" data-testid="artifact-withdrawn">{t('artifact.withdrawn')}</p>}
      {phase === 'unavailable' && !record && <p role="alert" data-testid="artifact-unavailable">{t('artifact.unavailable')}</p>}
      {record && (
        <>
          <dl className="ad-approval__facts ad-pcard__facts">
            <div><dt>{t('artifact.name')}</dt><dd data-testid="artifact-name">{facts.name}</dd></div>
            {facts.size !== null && <div><dt>{t('artifact.size')}</dt><dd data-testid="artifact-size">{formatBytes(facts.size)}</dd></div>}
            {facts.expiresAt && <div><dt>{t('artifact.expires')}</dt><dd data-testid="artifact-expires"><time dateTime={new Date(facts.expiresAt).toISOString()}>{new Date(facts.expiresAt).toLocaleString(i18n.language)}</time></dd></div>}
            <div><dt>{t('artifact.state')}</dt><dd data-testid="artifact-state">{t(`artifact.states.${state}`)}</dd></div>
          </dl>
          {state === 'transferring' && <p role="status" data-testid="artifact-progress">{t('artifact.preparing')}</p>}
          {state === 'failed' && <p role="alert" data-testid="artifact-failed">{t('artifact.failed')}</p>}
          {state === 'expired' && <p role="status" data-testid="artifact-expired">{t('artifact.expired')}</p>}
          {facts.downloadable && (
            <div className="ad-approval__foot">
              <a className="dk-btn dk-btn--primary" data-testid="artifact-download" href={apiUrl(artifactDownloadPath(facts.id))} download={facts.name || undefined} rel="noopener"
                aria-busy={saving === 'busy' ? 'true' : undefined} onClick={save}>
                {t('artifact.download')}
              </a>
            </div>
          )}
          {saving === 'denied' && <p role="alert" data-testid="artifact-download-denied">{t('artifact.downloadDenied')}</p>}
          {saving === 'failed' && <p role="alert" data-testid="artifact-download-failed">{t('artifact.downloadFailed')}</p>}
        </>
      )}
    </div>
  )
}
