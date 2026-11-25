// Settings > Updates (mock screens 30 to 33): the server and launcher updates,
// and the update channel of This computer.
import { useEffect, useId, useState } from 'react'
import { Button, Callout, Icon, Switch } from '@localai/ui-kit'
import type { UpdateChannelName } from '../../shared/types'
import { useBridge } from '../bridge'
import { Dialog } from '../Dialog'
import { Card, Field, fieldIds } from '../parts'
import { S } from '../strings'
import type { ShellControls, ShellData } from '../useLauncher'
import { serverVersion, timeAgo } from '../viewModel'
import { ApkUpdateSheet } from './ApkUpdateSheet'

const CHANNELS: UpdateChannelName[] = ['stable', 'rc', 'dev']

export function UpdatesTab({ data, controls, now }: { data: ShellData; controls: ShellControls; now: number }) {
  if (data.capabilities.selfUpdate === 'apk') return <ApkUpdatesTab data={data} controls={controls} now={now} />
  return <DesktopUpdatesTab data={data} controls={controls} now={now} />
}

/**
 * The Android app (no release-candidate row): the app
 * version, its channel, the checks, and for the stable app the way to the
 * development app. One channel per app: the development app has its own
 * application ID, so there is nothing to switch.
 */
function ApkUpdatesTab({ data, controls, now }: { data: ShellData; controls: ShellControls; now: number }) {
  const b = useBridge()
  const ids = useId()
  const [sheet, setSheet] = useState(false)
  const info = data.updateInfo
  const up = data.updater
  const phase = up?.phase ?? 'idle'
  const channel = info?.channel ?? 'stable'
  // A check (Check now, or the daily one on resume) saves its time: read it again when the phase settles.
  const { setUpdateInfo } = controls
  useEffect(() => {
    if (phase === 'checking') return
    let live = true
    void b.getUpdateInfo?.().then(
      (i) => {
        if (live && i) setUpdateInfo(i)
      },
      () => undefined,
    )
    return () => {
      live = false
    }
  }, [b, phase, setUpdateInfo])
  const offerVersion = phase === 'available' || phase === 'downloaded' || phase === 'downloading' ? up?.version : undefined
  const phaseText = (() => {
    const t = S.apkUpdate.phase[phase]
    return typeof t === 'function' ? (phase === 'downloading' ? (t as (p?: number) => string)(up?.percent) : (t as (v?: string) => string)(up?.version)) : t
  })()
  const checked = info?.checkedAt ? S.settings.checkedAgo(timeAgo(info.checkedAt, now)) : undefined
  // "Checked 2 hours ago · up to date" when the check succeeded; otherwise the updater's own words.
  const note =
    phase === 'error' && up?.message
      ? up.message
      : phase === 'not-available' && checked
        ? `${checked} · ${S.apkUpdate.upToDate}`
        : phase === 'idle' && checked
          ? checked
          : phaseText

  return (
    <>
      <Card title={S.settings.updates}>
        {data.info && (
          <Field name={S.apkUpdate.appVersion(data.info.version)} note={note}>
            <span className="lnr-grow" />
            <Button size="sm" busy={phase === 'checking'} disabled={phase === 'downloading'} onClick={() => void b.checkForLauncherUpdate()} data-testid="apk-check">
              {S.settings.checkNow}
            </Button>
            {offerVersion && b.getApkUpdate && (
              <Button size="sm" variant="primary" onClick={() => setSheet(true)} data-testid="apk-open-sheet">
                {S.apkUpdate.updateTo(offerVersion)}
              </Button>
            )}
          </Field>
        )}
        <Field name={S.apkUpdate.channelName} note={channel === 'dev' ? S.apkUpdate.devChannelNote : S.apkUpdate.channelNote}>
          <span className="lnr-grow" />
          <strong data-testid="apk-channel">{S.apkUpdate.channel[channel]}</strong>
        </Field>
        {b.setAutoCheck && info && (
          <Field id={`${ids}-auto`} name={S.settings.autoCheck} note={S.apkUpdate.autoCheckNote}>
            <span className="lnr-grow" />
            <Switch
              checked={info.autoCheck}
              onChange={async (v) => {
                const r = await b.setAutoCheck?.(v)
                if (r) controls.setUpdateInfo(r)
              }}
              {...fieldIds(`${ids}-auto`)}
            />
          </Field>
        )}
        {channel === 'stable' && b.getDevApp && (
          <Field name={S.apkUpdate.devApp} note={S.apkUpdate.devAppNote}>
            <span className="lnr-grow" />
            <Button size="sm" icon="flask" onClick={() => void b.getDevApp?.()} data-testid="get-dev-app">
              {S.apkUpdate.getDevApp}
            </Button>
          </Field>
        )}
      </Card>
      {sheet && data.info && (
        <ApkUpdateSheet installed={data.info.version} channel={S.apkUpdate.channelShort[channel]} updater={up} onClose={() => setSheet(false)} />
      )}
    </>
  )
}

function DesktopUpdatesTab({ data, controls, now }: { data: ShellData; controls: ShellControls; now: number }) {
  const b = useBridge()
  const ids = useId()
  const [checking, setChecking] = useState(false)
  const [dialog, setDialog] = useState<'confirm-dev' | 'back-to-stable' | null>(null)
  const [understood, setUnderstood] = useState(false)
  const info = data.updateInfo
  // The server rows and the channels update a local server through the desktop updater; other apps show the launcher row only.
  const serverRows = data.capabilities.localServer && data.capabilities.selfUpdate === 'electron'
  const channel: UpdateChannelName = info?.channel ?? 'stable'
  const dev = channel === 'dev'
  // The live server list decides; the flag in the update info can be stale after the default changes.
  const remote = data.servers ? !data.servers.rows.some((r) => r.isDefault && r.kind === 'local') : !!info?.remoteDefault
  const defaultName = data.servers?.rows.find((r) => r.isDefault)?.name ?? ''
  const install = data.install
  const version = serverVersion(install, data.installedVersion)
  const offer = install?.kind === 'update-available' ? install.version : undefined
  const checkedAt = info?.checkedAt
  const checkedText = checking || install?.kind === 'checking' ? S.settings.checking : checkedAt ? S.settings.checkedAgo(timeAgo(checkedAt, now)) : undefined
  const up = data.updater
  const phase = up?.phase ?? 'idle'
  const launcherNote = (() => {
    const t = S.settings.launcherPhase[phase]
    return typeof t === 'function' ? (phase === 'downloading' ? (t as (p?: number) => string)(up?.percent) : (t as (v?: string) => string)(up?.version)) : t
  })()

  const pick = async (c: UpdateChannelName) => {
    if (c === channel) return
    if (c === 'stable' && dev) return setDialog('back-to-stable')
    const r = await b.setUpdateChannel?.(c)
    if (!r) return
    if (r.ok) controls.setUpdateInfo(r.info)
    else {
      setUnderstood(false)
      setDialog('confirm-dev')
    }
  }
  const turnOnDev = async () => {
    const r = await b.setUpdateChannel?.('dev', true)
    setDialog(null)
    if (r?.ok) controls.setUpdateInfo(r.info)
  }
  const backToStable = async () => {
    setDialog(null)
    const r = await b.backToStable?.()
    if (r) controls.setUpdateInfo(r)
  }

  const label: Record<UpdateChannelName, { name: string; note: string }> = {
    stable: { name: S.channel.stable, note: S.channel.stableNote },
    rc: { name: S.channel.rc, note: S.channel.rcNote },
    dev: { name: S.channel.dev, note: S.channel.devNote },
  }

  const serverNote = remote
    ? S.channel.notChecked(defaultName)
    : dev
      ? S.channel.devBuildNote(undefined, checkedText, offer)
      : [checkedText, offer ? S.settings.available(offer) : install?.kind === 'up-to-date' ? S.settings.upToDate : undefined].filter(Boolean).join(' · ')

  return (
    <>
      {serverRows && remote && (
        <Callout tone="info" icon="info" title={S.channel.remoteTitle(defaultName)} testId="channel-remote-strip">
          <span className="lnr-block">{S.channel.remoteBody}</span>
        </Callout>
      )}
      {serverRows && dev && !remote && (
        <Callout tone="warn" icon="flask" title={S.channel.onDevTitle} testId="channel-dev-strip">
          <span className="lnr-block">{S.channel.onDevBody}</span>
        </Callout>
      )}

      <Card title={S.settings.updates}>
        {serverRows && (
          <Field name={dev && info?.devBuild ? S.channel.devBuild(info.devBuild) : S.settings.serverVersion(version)} note={serverNote || undefined}>
            <span className="lnr-grow" />
            <Button
              size="sm"
              busy={checking}
              disabled={remote}
              onClick={async () => {
                setChecking(true)
                try {
                  if (b.checkServerUpdate) controls.setInstall(await b.checkServerUpdate())
                  if (b.getUpdateInfo) controls.setUpdateInfo(await b.getUpdateInfo())
                } finally {
                  setChecking(false)
                }
              }}
            >
              {S.settings.checkNow}
            </Button>
            {offer && !remote && (
              <Button size="sm" variant="primary" onClick={() => void b.installServer?.({ stopFirst: true })}>
                {S.settings.updateTo(offer)}
              </Button>
            )}
          </Field>
        )}
        {b.setAutoCheck && info && (
          <Field id={`${ids}-auto`} name={S.settings.autoCheck} note={S.settings.autoCheckNote}>
            <span className="lnr-grow" />
            <Switch
              checked={info.autoCheck}
              onChange={async (v) => {
                const r = await b.setAutoCheck?.(v)
                if (r) controls.setUpdateInfo(r)
              }}
              {...fieldIds(`${ids}-auto`)}
            />
          </Field>
        )}
        {serverRows && (
          <Field name={S.settings.previous} note={S.settings.previousNote(info?.previousVersion)}>
            <span className="lnr-grow" />
            <Button size="sm" disabled={remote} onClick={() => void b.rollbackServer?.({ stopFirst: true })}>{S.settings.rollBack}</Button>
          </Field>
        )}
        {data.info && (
          <Field name={S.settings.launcherVersion(data.info.version)} note={launcherNote}>
            <span className="lnr-grow" />
            {phase === 'downloaded' && b.installLauncherUpdate ? (
              <Button size="sm" onClick={() => void b.installLauncherUpdate?.()}>{S.settings.restartToUpdate}</Button>
            ) : (
              phase !== 'unsupported' && (
                <Button size="sm" busy={phase === 'checking'} onClick={() => void b.checkForLauncherUpdate()}>{S.settings.checkNow}</Button>
              )
            )}
          </Field>
        )}
      </Card>

      {serverRows && b.setUpdateChannel && info && (
        <Card title={S.channel.title}>
          <p className="dk-small dk-muted lnr-card__lead">{S.channel.sub}</p>
          <div role="radiogroup" aria-label={S.channel.groupLabel} className="lnr-radios">
            {CHANNELS.map((c) => (
              <label key={c} className={`lnr-radio${channel === c ? ' is-on' : ''}`} data-testid={`channel-${c}`}>
                <input type="radio" name={`${ids}-channel`} checked={channel === c} onChange={() => void pick(c)} />
                <span className="lnr-radio__dot" aria-hidden="true" />
                <span className="lnr-radio__text">
                  <span className="lnr-radio__name">
                    <strong>{label[c].name}</strong>
                    {c === 'stable' && <span className="lnr-badge lnr-badge--ok">{S.channel.defaultBadge}</span>}
                    {c === 'dev' && <span className="lnr-badge lnr-badge--warn"><Icon name="flask" /> {S.channel.notSupported}</span>}
                  </span>
                  <span className="lnr-radio__note dk-small dk-muted">{label[c].note}</span>
                </span>
              </label>
            ))}
          </div>
          <p className="lnr-effect dk-small dk-muted"><Icon name="info" /> <span>{S.channel.devEffect}</span></p>
          {dev && (
            <Field name={S.channel.leave} note={S.channel.leaveNote('')}>
              <span className="lnr-grow" />
              <Button variant="primary" onClick={() => setDialog('back-to-stable')} data-testid="back-to-stable">{S.channel.backToStable}</Button>
            </Field>
          )}
        </Card>
      )}

      {dialog === 'confirm-dev' && (
        <Dialog title={<><Icon name="flask" /> {S.channel.confirmTitle}</>} titleId={`${ids}-dev`} onClose={() => setDialog(null)} initialFocus="[data-cancel]" testId="confirm-dev" className="lnr-dialog--wide">
          <p className="lnr-dialog__p"><strong>{S.channel.confirmLead}</strong>{S.channel.confirmLead2}</p>
          <h3 className="dk-eyebrow">{S.channel.whatChanges}</h3>
          <ol className="lnr-steps lnr-dialog__steps">
            {[S.channel.change1, S.channel.change2, S.channel.change3, S.channel.change4(version)].map((t, i) => (
              <li key={i} className="lnr-step"><div className="lnr-step__row"><span className="lnr-step__mark lnr-step__mark--active" aria-hidden="true">{i + 1}</span><span>{t}</span></div></li>
            ))}
          </ol>
          <label className="lnr-check">
            <input type="checkbox" checked={understood} onChange={(e) => setUnderstood(e.target.checked)} data-testid="understand" />
            <strong>{S.channel.understand}</strong>
          </label>
          <p className="dk-small dk-muted">{S.channel.exit}</p>
          <div className="lnr-row lnr-dialog__actions">
            <span className="lnr-grow" />
            <Button data-cancel onClick={() => setDialog(null)}>{S.channel.cancel}</Button>
            <Button variant="primary" disabled={!understood} onClick={() => void turnOnDev()} data-testid="turn-on-dev">{S.channel.turnOn}</Button>
          </div>
        </Dialog>
      )}
      {dialog === 'back-to-stable' && (
        <Dialog title={S.channel.backTitle} titleId={`${ids}-back`} onClose={() => setDialog(null)} initialFocus="[data-confirm]" testId="back-dialog" className="lnr-dialog--wide">
          <p className="lnr-dialog__p">{S.channel.backLead}</p>
          <ul className="lnr-ticks">
            {[S.channel.back1(info?.devBuild), S.channel.back2, S.channel.back3].map((t) => (
              <li key={t}><span className="lnr-step__mark lnr-step__mark--done" aria-hidden="true"><Icon name="check" /></span><span>{t}</span></li>
            ))}
          </ul>
          <Callout tone="warn" icon="warning">{S.channel.backWarn}</Callout>
          <div className="lnr-row lnr-dialog__actions">
            <span className="lnr-grow" />
            <Button onClick={() => setDialog(null)}>{S.channel.cancel}</Button>
            <Button variant="primary" data-confirm onClick={() => void backToStable()} data-testid="back-confirm">{S.channel.backConfirm}</Button>
          </div>
        </Dialog>
      )}
    </>
  )
}
