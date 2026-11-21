// Settings, Advanced: registry credential rules. The list shows the registry,
// the kind, the user name and whether a secret is set; the secret itself never
// reaches the renderer. The secret input is a password field with no reveal
// button and is cleared after every save. A change applies after a restart.
import { useEffect, useId, useRef, useState } from 'react'
import { Button, Callout, StatusChip } from '@localai/ui-kit'
import { matchProblem, needsNewSecret, usernameProblem } from '../../shared/registryMatch'
import type { RegistryCredentialKind, RegistryCredentialTestResult, RegistryCredentialView, RegistryCredentialsView } from '../../shared/types'
import { useBridge } from '../bridge'
import { Field } from '../parts'
import { S } from '../strings'

interface Draft {
  /** Set while editing an existing rule. */
  id?: string
  match: string
  kind: RegistryCredentialKind
  username: string
  secret: string
  /** The rule being edited already has a stored secret. */
  hadSecret: boolean
  /** The kind and registry the stored secret was given for. */
  saved?: { kind: RegistryCredentialKind; match: string }
}

/** The stored secret can stay: there is one, and the kind and host are the same. */
const keepsSecret = (d: Draft): boolean => d.hadSecret && !!d.saved && !needsNewSecret(d.saved, d)

const EMPTY: Draft = { match: '', kind: 'basic', username: '', secret: '', hadSecret: false }

export function RegistryCredentialsField({ onRestartNeeded }: { onRestartNeeded?: () => void }) {
  const b = useBridge()
  const ids = useId()
  const [view, setView] = useState<RegistryCredentialsView>()
  const [draft, setDraft] = useState<Draft>()
  const [error, setError] = useState<string>()
  const [busy, setBusy] = useState(false)
  // Set at once, so two events in the same tick cannot both start a save.
  const saving = useRef(false)
  const [restart, setRestart] = useState(false)
  const [confirmId, setConfirmId] = useState<string>()
  const [results, setResults] = useState<Record<string, RegistryCredentialTestResult['result'] | 'testing' | 'failed'>>({})

  useEffect(() => {
    let live = true
    void b.getRegistryCredentials?.().then((v) => live && setView(v)).catch(() => undefined)
    return () => {
      live = false
    }
  }, [b])

  if (!b.getRegistryCredentials || !view) return null

  const reload = async () => {
    const v = await b.getRegistryCredentials?.()
    if (v) setView(v)
  }
  const needRestart = () => {
    setRestart(true)
    onRestartNeeded?.()
  }
  const close = () => {
    setDraft(undefined)
    setError(undefined)
  }
  const patch = (p: Partial<Draft>) => setDraft((d) => (d ? { ...d, ...p } : d))

  const save = async () => {
    if (!draft || saving.current) return
    setError(undefined)
    const match = draft.match.trim()
    const problem = matchProblem(match)
    if (problem) return setError(problem)
    if (draft.kind === 'basic') {
      const bad = usernameProblem(draft.username)
      if (bad) return setError(bad)
    }
    if (draft.secret === '' && !keepsSecret(draft)) return setError(draft.hadSecret ? S.settings.cred.secretAgain : S.settings.cred.needSecret)
    saving.current = true
    setBusy(true)
    try {
      const r = await b.saveRegistryCredential?.({
        ...(draft.id ? { id: draft.id } : {}),
        match,
        kind: draft.kind,
        ...(draft.kind === 'basic' ? { username: draft.username } : {}),
        // An empty secret on an edit means: keep the stored one.
        ...(draft.secret !== '' ? { secret: draft.secret } : {}),
      })
      // The secret is gone from the form whatever the answer was.
      patch({ secret: '' })
      if (!r) return
      if (!r.ok) {
        setError(r.reason)
        return
      }
      if (r.restartRequired) needRestart()
      close()
    } catch {
      patch({ secret: '' })
      setError(S.settings.cred.failed)
      return
    } finally {
      saving.current = false
      setBusy(false)
    }
    // The rule is saved; a list that cannot be refreshed is a separate, smaller problem.
    try {
      await reload()
    } catch {
      setError(S.settings.cred.savedNoRefresh)
    }
  }

  const test = async (id: string) => {
    setResults((r) => ({ ...r, [id]: 'testing' }))
    try {
      const r = await b.testRegistryCredential?.(id)
      setResults((m) => ({ ...m, [id]: r?.result ?? 'failed' }))
    } catch {
      setResults((m) => ({ ...m, [id]: 'failed' }))
    }
  }

  const remove = async (id: string) => {
    setConfirmId(undefined)
    try {
      const r = await b.removeRegistryCredential?.(id)
      if (r?.restartRequired) needRestart()
    } catch {
      // The list below shows what is left.
    }
    try {
      await reload()
    } catch {
      // Keep the list as it is.
    }
  }

  const startEdit = (r: RegistryCredentialView) => {
    setError(undefined)
    setDraft({ id: r.id, match: r.match, kind: r.kind, username: r.username ?? '', secret: '', hadSecret: r.secretSet, saved: { kind: r.kind, match: r.match } })
  }

  const cred = S.settings.cred
  const errId = `${ids}-err`
  const confirming = view.rules.find((r) => r.id === confirmId)

  return (
    <Field id={`${ids}-cred`} name={cred.title} note={cred.note} wide>
      <div className="lnr-cred" role="group" aria-labelledby={`${ids}-cred-name`} data-testid="registry-credentials">
        {view.userOverride && <Callout tone="warn" role="status" title={cred.override} />}
        {!view.userOverride && view.rules.length > 0 && <span className="dk-small dk-muted">{cred.replacesDefault}</span>}
        {view.rules.length === 0 && !draft && <span className="dk-small dk-muted">{cred.empty}</span>}
        {view.rules.map((r) => {
          const result = results[r.id]
          return (
            <div key={r.id} className="lnr-row lnr-cred__row" data-testid="registry-rule">
              <span className="lnr-value dk-mono lnr-grow">{r.match}</span>
              <StatusChip tone="idle">{r.kind === 'basic' ? cred.basic : cred.bearer}</StatusChip>
              {r.username !== undefined && <span className="dk-small dk-mono">{r.username}</span>}
              <StatusChip tone={r.secretSet ? 'accent' : 'warn'}>{r.secretSet ? cred.secretSet : cred.secretMissing}</StatusChip>
              {result && (
                <span className="dk-small" role="status" data-testid="registry-test-result">
                  {result === 'testing' ? cred.testing : cred.results[result]}
                </span>
              )}
              <Button size="sm" variant="ghost" aria-label={cred.testLabel(r.match)} disabled={result === 'testing'} onClick={() => void test(r.id)}>{cred.test}</Button>
              <Button size="sm" variant="ghost" aria-label={cred.editLabel(r.match)} onClick={() => startEdit(r)}>{cred.edit}</Button>
              <Button size="sm" variant="ghost" icon="trash" aria-label={cred.removeLabel(r.match)} onClick={() => setConfirmId(r.id)} />
            </div>
          )
        })}
        {confirming && (
          <Callout
            tone="warn"
            role="alertdialog"
            aria-label={cred.removeTitle}
            title={cred.removeTitle}
            actions={
              <>
                <Button size="sm" onClick={() => setConfirmId(undefined)}>{cred.cancel}</Button>
                <Button size="sm" variant="primary" onClick={() => void remove(confirming.id)}>{cred.removeConfirm}</Button>
              </>
            }
          >
            {cred.removeBody(confirming.match)}
          </Callout>
        )}
        {draft && (
          <div className="lnr-cred__form">
            <div className="lnr-row lnr-cred__row">
              <input
                className="dk-input dk-input--mono lnr-grow"
                aria-label={cred.registry}
                aria-describedby={error ? errId : undefined}
                aria-invalid={error ? true : undefined}
                placeholder={cred.registryHint}
                value={draft.match}
                spellCheck={false}
                autoComplete="off"
                autoFocus
                onChange={(e) => patch({ match: e.target.value })}
              />
              <span className="dk-select-wrap lnr-field__select">
                <select className="dk-select" aria-label={cred.kind} value={draft.kind} onChange={(e) => patch({ kind: e.target.value as RegistryCredentialKind })}>
                  <option value="basic">{cred.basic}</option>
                  <option value="bearer">{cred.bearer}</option>
                </select>
              </span>
            </div>
            <div className="lnr-row lnr-cred__row">
              {draft.kind === 'basic' && (
                <input
                  className="dk-input dk-input--mono"
                  aria-label={cred.username}
                  value={draft.username}
                  spellCheck={false}
                  autoComplete="off"
                  onChange={(e) => patch({ username: e.target.value })}
                />
              )}
              <input
                className="dk-input dk-input--mono lnr-grow"
                aria-label={draft.kind === 'basic' ? cred.secretBasic : cred.secretBearer}
                placeholder={keepsSecret(draft) ? cred.secretKeep : undefined}
                type="password"
                value={draft.secret}
                spellCheck={false}
                autoComplete="new-password"
                onChange={(e) => patch({ secret: e.target.value })}
                onKeyDown={(e) => e.key === 'Enter' && void save()}
              />
              <Button size="sm" variant="primary" busy={busy} onClick={() => void save()}>{cred.save}</Button>
              <Button size="sm" variant="ghost" onClick={close}>{cred.cancel}</Button>
            </div>
          </div>
        )}
        {error && <p id={errId} className="dk-field-error" role="alert">{error}</p>}
        {restart && <span className="dk-small" role="status" data-testid="registry-restart">{cred.restart}</span>}
        {!draft && (
          <div>
            <Button size="sm" icon="plus" onClick={() => (setError(undefined), setDraft({ ...EMPTY }))}>{cred.add}</Button>
          </div>
        )}
      </div>
    </Field>
  )
}
