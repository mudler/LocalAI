import { useId, useState } from 'react'
import { Button, Callout, Page, StatusChip } from '@localai/ui-kit'
import type { WorkerConfig, WorkerStatus, WorkerView } from '../bridge'
import { useBridge } from '../bridge'
import { Card, Field } from '../parts'
import { S } from '../strings'

const STATUS_TONE: Record<WorkerStatus, 'ok' | 'warn' | 'error' | 'idle' | 'accent'> = {
  idle: 'idle',
  testing: 'accent',
  reachable: 'ok',
  unreachable: 'error',
  'waiting-approval': 'ok',
  connected: 'ok',
  failed: 'error',
}

/** Start is offered only with the fields a worker cannot run without. */
export function workerReady(c: WorkerConfig, tokenStored: boolean): boolean {
  return !!c.serverUrl.trim() && !!c.natsUrl.trim() && !!c.advertiseAddress.trim() && (tokenStored || !!c.token.trim())
}

export function WorkerSettingsScreen({ worker, onWorker }: { worker: WorkerView; onWorker: (w: WorkerView) => void }) {
  const b = useBridge()
  const ids = useId()
  const [mode, setMode] = useState(worker.mode)
  const [c, setC] = useState<WorkerConfig>({ ...worker.config, token: '' })
  const [busy, setBusy] = useState<'test' | 'start' | null>(null)
  const set = (k: keyof WorkerConfig) => (e: React.ChangeEvent<HTMLInputElement>) => setC({ ...c, [k]: e.target.value })
  const run = (k: 'test' | 'start', fn: () => Promise<WorkerView> | undefined) => async () => {
    setBusy(k)
    try {
      const r = await fn()
      if (r) onWorker(r)
    } finally {
      setBusy(null)
    }
  }
  const pickMode = async (m: 'local' | 'worker') => {
    setMode(m)
    const r = await b.setRunMode?.(m)
    if (r) onWorker(r)
  }
  const status = busy === 'test' ? 'testing' : worker.status

  const input = (k: keyof WorkerConfig, label: string, note?: string, opts: { mono?: boolean; secret?: boolean; optional?: boolean } = {}) => (
    <Field id={`${ids}-${k}`} name={<>{label}{opts.optional && <span className="dk-muted"> {S.worker.optional}</span>}</>} note={note}>
      <input
        className={`dk-input lnr-field__input${opts.mono === false ? '' : ' dk-input--mono'}`}
        type={opts.secret ? 'password' : 'text'}
        value={c[k]}
        placeholder={opts.secret && worker.tokenStored ? '••••••••••••••••' : undefined}
        onChange={set(k)}
        aria-labelledby={`${ids}-${k}-name`}
        aria-describedby={note ? `${ids}-${k}-note` : undefined}
        autoComplete="off"
        spellCheck={false}
      />
    </Field>
  )

  return (
    <Page className="lnr-screen" data-screen="worker-settings">
      <h1 className="lnr-h">{S.worker.settingsTitle}</h1>
      <p className="lnr-sub">{S.worker.settingsSub}</p>
      <div className="lnr-picks" role="radiogroup" aria-label={S.worker.modeLabel}>
        {(['local', 'worker'] as const).map((m) => (
          <label key={m} className={`dk-card dk-card--tight dk-card--interactive lnr-pick${mode === m ? ' lnr-pick--on' : ''}`}>
            <span className="lnr-grow">
              <strong>{m === 'local' ? S.worker.local : S.worker.join}</strong>
              <span className="dk-small dk-muted lnr-block">{m === 'local' ? S.worker.localNote : S.worker.joinNote}</span>
            </span>
            <input type="radio" className="dk-radio" name={`${ids}-mode`} checked={mode === m} onChange={() => void pickMode(m)} />
          </label>
        ))}
      </div>
      {mode === 'worker' && (
        <>
          <Card>
            <p className="dk-small dk-muted lnr-card__lead">{S.worker.reachNote}</p>
            {input('serverUrl', S.worker.serverUrl)}
            {input('natsUrl', S.worker.natsUrl)}
            {input('token', S.worker.token, worker.tokenStored ? `${S.worker.tokenNote}. ${S.worker.tokenKept}` : S.worker.tokenNote, { secret: true })}
            {input('advertiseAddress', S.worker.address, S.worker.addressNote)}
            {input('nodeName', S.worker.nodeName, undefined, { mono: false, optional: true })}
          </Card>
          {worker.message && worker.status === 'failed' && (
            <Callout tone="error" role="alert" title={S.worker.status.failed}>{worker.message}</Callout>
          )}
          <div className="lnr-row lnr-actions">
            <span role="status" aria-live="polite">
              <StatusChip tone={STATUS_TONE[status]} dot={status !== 'testing'} icon={status === 'testing' ? 'spinner' : undefined}>
                {S.worker.status[status]}
              </StatusChip>
            </span>
            <span className="lnr-grow" />
            {b.testWorker && (
              <Button busy={busy === 'test'} onClick={run('test', () => b.testWorker?.(c))}>{S.worker.test}</Button>
            )}
            {b.startWorker && (
              <Button variant="primary" busy={busy === 'start'} disabled={!workerReady(c, worker.tokenStored)} onClick={run('start', () => b.startWorker?.(c))}>
                {S.worker.start}
              </Button>
            )}
          </div>
        </>
      )}
    </Page>
  )
}

export function WorkerRunningScreen({ worker, onWorker }: { worker: WorkerView; onWorker: (w: WorkerView) => void }) {
  const b = useBridge()
  const [busy, setBusy] = useState(false)
  const status = worker.status
  const row = (label: string, value: string, note?: string, extra?: React.ReactNode, mono = true) => (
    <Field name={label} note={note}>
      <span className={`lnr-value lnr-grow${mono ? ' dk-mono' : ''}`}>{value}</span>
      {extra}
    </Field>
  )
  return (
    <Page className="lnr-screen" data-screen="worker">
      <h1 className="lnr-h">{S.worker.runningTitle}</h1>
      <p className="lnr-sub">{S.worker.runningSub}</p>
      <Card>
        <div className="lnr-row lnr-card__bar" role="status" aria-live="polite">
          <StatusChip tone={STATUS_TONE[status]} dot>{S.worker.status[status]}</StatusChip>
          <span className="dk-small dk-muted lnr-grow">{S.worker.joined(worker.config.serverUrl)}</span>
        </div>
        {row(S.worker.serverUrl, worker.config.serverUrl)}
        {row(S.worker.natsUrl, worker.config.natsUrl)}
        {row(S.worker.token, '••••••••••••••••', S.worker.tokenNote, worker.tokenStored ? <StatusChip tone="idle">{S.worker.remembered}</StatusChip> : undefined)}
        {row(S.worker.address, worker.config.advertiseAddress, S.worker.addressNote)}
        {worker.config.nodeName && row(S.worker.nodeName, worker.config.nodeName, undefined, undefined, false)}
      </Card>
      <div className="lnr-row lnr-actions">
        <span className="dk-small dk-muted">{S.worker.toWebui}</span>
        <span className="lnr-grow" />
        {b.stopWorker && (
          <Button
            variant="danger"
            busy={busy}
            onClick={async () => {
              setBusy(true)
              try {
                const r = await b.stopWorker?.()
                if (r) onWorker(r)
              } finally {
                setBusy(false)
              }
            }}
          >
            {S.worker.stop}
          </Button>
        )}
      </div>
    </Page>
  )
}
