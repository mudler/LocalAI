// Settings, Advanced: the server's environment variables. Names come from the
// main process; values never do. A saved value shows as dots and can only be
// replaced or removed. Each change is saved at once (it is a secret, not a
// draft), and a running server needs a restart to see it.
import { useEffect, useId, useState } from 'react'
import { Button, StatusChip, Switch } from '@localai/ui-kit'
import { envNameProblem, MAX_ENV_VALUE } from '../../shared/envVars'
import type { EnvVarFailReason, EnvVarsView } from '../../shared/types'
import { useBridge } from '../bridge'
import { Field, fieldIds } from '../parts'
import { SecretStorageNotice } from '../SecretStorageNotice'
import { S } from '../strings'

const PROBLEM_REASON = { format: 'name-format', length: 'name-length', controlled: 'name-controlled' } as const

/** The reason a new variable cannot be saved, checked before the main process checks it again. */
export function localEnvProblem(name: string, value: string): EnvVarFailReason | undefined {
  const p = envNameProblem(name)
  if (p) return PROBLEM_REASON[p]
  return value.length > MAX_ENV_VALUE ? 'value-length' : undefined
}

export function EnvVarsField({
  plainFileOn,
  onPlainFile,
  onRestartNeeded,
}: {
  /** The saved opt-in (settings.secretsPlaintextFile). */
  plainFileOn: boolean
  onPlainFile: (on: boolean) => Promise<unknown>
  onRestartNeeded: () => void
}) {
  const b = useBridge()
  const ids = useId()
  const [view, setView] = useState<EnvVarsView>()
  const [adding, setAdding] = useState(false)
  const [name, setName] = useState('')
  const [value, setValue] = useState('')
  const [shown, setShown] = useState(false)
  const [replacing, setReplacing] = useState<string>()
  const [replaceValue, setReplaceValue] = useState('')
  const [error, setError] = useState<string>()
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    let live = true
    void b.getEnvVars?.().then((v) => live && setView(v)).catch(() => undefined)
    return () => {
      live = false
    }
  }, [b])

  if (!b.getEnvVars || !view) return null

  const take = (v: EnvVarsView) => {
    setView(v)
    if (v.restartRequired) onRestartNeeded()
  }

  const save = async (n: string, val: string, done: () => void) => {
    setError(undefined)
    const local = localEnvProblem(n, val)
    if (local) {
      setError(S.settings.envErrors[local])
      return
    }
    setBusy(true)
    try {
      const r = await b.setEnvVar?.(n, val)
      if (!r) return
      if (!r.ok) {
        setError(S.settings.envErrors[r.reason])
        return
      }
      take(r.view)
      done()
    } catch {
      setError(S.settings.envErrors.failed)
    } finally {
      setBusy(false)
    }
  }

  const resetAdd = () => {
    setAdding(false)
    setName('')
    setValue('')
    setShown(false)
  }
  const resetReplace = () => {
    setReplacing(undefined)
    setReplaceValue('')
  }

  const storage = view.storage
  const errId = `${ids}-err`

  return (
    <>
      <Field id={`${ids}-env`} name={S.settings.env} note={S.settings.envNote} wide>
        <div className="lnr-env" role="group" aria-labelledby={`${ids}-env-name`} data-testid="env-vars">
          {view.vars.length === 0 && !adding && <span className="dk-small dk-muted">{S.settings.envEmpty}</span>}
          {view.vars.map((v) => (
            <div key={v.name} className="lnr-row lnr-env__row">
              <span className="lnr-value dk-mono lnr-env__name">{v.name}</span>
              {replacing === v.name ? (
                <>
                  <input
                    className="dk-input dk-input--mono lnr-grow"
                    aria-label={S.settings.envNewValue(v.name)}
                    aria-describedby={error ? errId : undefined}
                    type="password"
                    value={replaceValue}
                    spellCheck={false}
                    autoComplete="off"
                    autoFocus
                    onChange={(e) => setReplaceValue(e.target.value)}
                    onKeyDown={(e) => e.key === 'Enter' && void save(v.name, replaceValue, resetReplace)}
                  />
                  <Button size="sm" variant="primary" busy={busy} onClick={() => void save(v.name, replaceValue, resetReplace)}>{S.settings.envSave}</Button>
                  <Button size="sm" variant="ghost" onClick={resetReplace}>{S.settings.envCancel}</Button>
                </>
              ) : (
                <>
                  <span className="lnr-value dk-mono lnr-grow" aria-label={S.settings.envHidden(v.name)} data-testid="env-masked">
                    {S.settings.envMasked}
                  </span>
                  {v.ignored && <StatusChip tone="warn">{S.settings.envIgnoredChip}</StatusChip>}
                  {v.sessionOnly && !v.ignored && <StatusChip tone="idle">{S.settings.envSessionOnly}</StatusChip>}
                  <Button size="sm" variant="ghost" aria-label={S.settings.envReplaceLabel(v.name)} onClick={() => setReplacing(v.name)}>
                    {S.settings.envReplace}
                  </Button>
                  <Button
                    size="sm"
                    variant="ghost"
                    icon="trash"
                    aria-label={S.settings.envRemove(v.name)}
                    onClick={async () => {
                      const r = await b.deleteEnvVar?.(v.name)
                      if (r) take(r)
                    }}
                  />
                </>
              )}
            </div>
          ))}
          {adding && (
            <div className="lnr-row lnr-env__row">
              <input
                className="dk-input dk-input--mono lnr-env__name"
                aria-label={S.settings.envName}
                aria-describedby={error ? errId : undefined}
                aria-invalid={error ? true : undefined}
                value={name}
                spellCheck={false}
                autoComplete="off"
                autoFocus
                onChange={(e) => setName(e.target.value)}
              />
              <input
                className="dk-input dk-input--mono lnr-grow"
                aria-label={S.settings.envValue}
                type={shown ? 'text' : 'password'}
                value={value}
                spellCheck={false}
                autoComplete="off"
                onChange={(e) => setValue(e.target.value)}
                onKeyDown={(e) => e.key === 'Enter' && void save(name.trim(), value, resetAdd)}
              />
              <Button
                size="sm"
                variant="ghost"
                icon={shown ? 'eye-off' : 'eye'}
                aria-label={shown ? S.settings.envHide(name) : S.settings.envShow(name)}
                onClick={() => setShown(!shown)}
              />
              <Button size="sm" variant="primary" busy={busy} onClick={() => void save(name.trim(), value, resetAdd)}>{S.settings.envSave}</Button>
              <Button size="sm" variant="ghost" onClick={() => (resetAdd(), setError(undefined))}>{S.settings.envCancel}</Button>
            </div>
          )}
          {view.vars.some((v) => v.ignored) && <span className="dk-small dk-muted">{S.settings.envIgnored}</span>}
          {error && <p id={errId} className="dk-field-error" role="alert">{error}</p>}
          {!adding && (
            <div>
              <Button size="sm" icon="plus" onClick={() => (resetReplace(), setAdding(true))}>{S.settings.envAdd}</Button>
            </div>
          )}
          <SecretStorageNotice storage={storage} />
        </div>
      </Field>
      {!storage.encryptionAvailable && storage.unreadable === 0 && (
        <Field id={`${ids}-plain`} name={S.settings.plainFile} note={S.settings.plainFileNote}>
          <span className="lnr-grow" />
          <Switch
            checked={plainFileOn || storage.plaintextOptIn}
            onChange={async (on) => {
              await onPlainFile(on)
              const v = await b.getEnvVars?.()
              if (v) setView(v)
            }}
            {...fieldIds(`${ids}-plain`)}
          />
        </Field>
      )}
    </>
  )
}
