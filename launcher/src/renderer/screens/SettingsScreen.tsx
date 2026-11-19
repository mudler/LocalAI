import { useEffect, useId, useRef, useState } from 'react'
import { Button, Callout, EmptyState, Page, StatusChip, Switch } from '@localai/ui-kit'
import type { FolderKind, LauncherSettings, LoginItemView, ServerLogLevel } from '../../shared/types'
import { useBridge, type NetworkView } from '../bridge'
import { Card, Field, fieldIds } from '../parts'
import { EnvVarsField } from './EnvVarsField'
import { ServersTab } from './ServersTab'
import { UpdatesTab } from './UpdatesTab'
import { S } from '../strings'
import type { ShellControls, ShellData } from '../useLauncher'
import { addressOf, draftOf, draftPatch, sandboxRow, serverVersion, timeAgo, type SettingsDraft } from '../viewModel'

export type SettingsTab = 'general' | 'servers' | 'updates' | 'network' | 'performance' | 'advanced'
const TABS: SettingsTab[] = ['general', 'servers', 'updates', 'network', 'performance', 'advanced']
const LOG_LEVELS: ServerLogLevel[] = ['error', 'warn', 'info', 'debug', 'trace']

export function SettingsScreen({
  data,
  controls,
  onOpenGuide,
  onConnect,
  onWorker,
  onSwitch,
  serversOpen,
  initialTab = 'general',
  now = Date.now(),
}: {
  data: ShellData
  controls: ShellControls
  onOpenGuide: () => void
  onConnect: () => void
  onWorker: () => void
  /** Makes a saved server (or `local`) the default and opens it. */
  onSwitch: (id: string) => void
  /** Open the add or edit sheet of Settings > Servers at once (from the switcher or the recovery screen). */
  serversOpen?: { kind: 'add' } | { kind: 'edit'; id: string }
  initialTab?: SettingsTab
  now?: number
}) {
  const b = useBridge()
  const ids = useId()
  const [tab, setTab] = useState<SettingsTab>(initialTab)
  const tabRefs = useRef<(HTMLButtonElement | null)[]>([])
  const settings = data.settings
  const [edits, setEdits] = useState<Partial<SettingsDraft>>({})
  const [restartNote, setRestartNote] = useState(false)
  const [net, setNet] = useState<NetworkView>()
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string>()
  const [loginNote, setLoginNote] = useState<string>()
  const [checking, setChecking] = useState(false)

  // The network row shows whether other devices are allowed; read again after a save, since an address can turn the key on.
  const netKey = settings?.address
  useEffect(() => {
    let live = true
    void b.getNetwork?.().then((n) => live && setNet(n)).catch(() => undefined)
    return () => {
      live = false
    }
  }, [b, netKey])

  if (!settings) return <Page className="lnr-screen" aria-busy="true" data-screen="settings" />

  const draft: SettingsDraft = { ...draftOf(settings), ...edits }
  const patch = draftPatch(settings, draft)
  const changes = Object.keys(patch).length
  const running = data.server.state === 'ready' || data.server.state === 'starting'

  const saveNow = async (p: Partial<LauncherSettings>) => {
    setError(undefined)
    try {
      const r = await b.saveSettings(p)
      controls.setSettings(r.settings)
      if (r.loginItem) setLoginNote(loginItemNote(r.loginItem))
      if (r.restartRequired) setRestartNote(true)
      return r
    } catch {
      setError(S.settings.saveFailed)
      return undefined
    }
  }
  const apply = async () => {
    setSaving(true)
    try {
      const r = await saveNow(patch)
      if (!r) return
      setEdits({})
      if (running && r.restartRequired) {
        await b.restart()
        setRestartNote(false)
      }
    } finally {
      setSaving(false)
    }
  }
  const discard = () => setEdits({})
  const edit = <K extends keyof SettingsDraft>(k: K, v: SettingsDraft[K]) => setEdits((e) => ({ ...e, [k]: v }))
  // The main process opens the picker, checks the folder and saves it; the renderer never sends a path.
  const pick = async (kind: FolderKind) => {
    setError(undefined)
    try {
      const r = await b.pickFolder?.(kind)
      if (!r || r.status === 'cancelled') return
      if (r.status === 'refused') {
        setError(S.settings.folderRefused[r.reason])
        return
      }
      controls.setSettings(r.settings)
      if (r.restartRequired) setRestartNote(true)
    } catch {
      setError(S.settings.saveFailed)
    }
  }
  const folder = (id: string, value: string, kind: FolderKind) => (
    <>
      <input
        className="dk-input dk-input--mono lnr-field__input"
        value={value}
        readOnly
        aria-readonly="true"
        aria-labelledby={`${id}-name`}
        aria-describedby={`${id}-note`}
        spellCheck={false}
        data-testid={`folder-${kind}`}
      />
      {b.pickFolder && <Button size="sm" onClick={() => void pick(kind)}>{S.settings.change}</Button>}
    </>
  )

  const onTabKey = (e: React.KeyboardEvent, i: number) => {
    const n = e.key === 'ArrowRight' ? i + 1 : e.key === 'ArrowLeft' ? i - 1 : e.key === 'Home' ? 0 : e.key === 'End' ? TABS.length - 1 : null
    if (n === null) return
    e.preventDefault()
    const to = (n + TABS.length) % TABS.length
    setTab(TABS[to])
    tabRefs.current[to]?.focus()
  }

  const sb = sandboxRow(data)
  const origin = addressOf(data.server.endpoint)

  const sw = (id: string, checked: boolean, onChange: (v: boolean) => void) => (
    <Switch checked={checked} onChange={onChange} {...fieldIds(id)} />
  )

  const general = (
    <>
      <Card title={S.settings.window}>
        <Field id={`${ids}-bar`} name={S.settings.showBar} note={S.settings.showBarNote}>
          <span className="lnr-grow" />
          {sw(`${ids}-bar`, data.statusBarSaved ?? true, async (v) => controls.setStatusBarSaved(await b.setStatusBarVisible(v)))}
        </Field>
        {b.setOpenInBrowser && data.windowSettings && (
          <Field id={`${ids}-browser`} name={S.settings.browser} note={S.settings.browserNote}>
            <span className="lnr-grow" />
            {sw(`${ids}-browser`, data.windowSettings.openInBrowser, async (v) => {
              const r = await b.setOpenInBrowser?.(v)
              if (r) controls.setWindowSettings(r)
            })}
          </Field>
        )}
        {b.setDesktopNotifications && data.windowSettings && (
          <Field id={`${ids}-notifications`} name={S.settings.notifications} note={S.settings.notificationsNote}>
            <span className="lnr-grow" />
            {sw(`${ids}-notifications`, data.windowSettings.desktopNotifications, async (v) => {
              const r = await b.setDesktopNotifications?.(v)
              if (r) controls.setWindowSettings(r)
            })}
          </Field>
        )}
        {b.resetWebuiPermissions && data.windowSettings && (
          <Field name={S.settings.permissions} note={S.settings.permissionsNote(origin)}>
            <span className="lnr-grow dk-small dk-muted">{S.settings.mediaChoice(data.windowSettings.media)}</span>
            <Button
              size="sm"
              disabled={!data.windowSettings.anyRemembered}
              onClick={async () => {
                await b.resetWebuiPermissions?.()
                const r = await b.getWindowSettings?.()
                if (r) controls.setWindowSettings(r)
              }}
            >
              {S.settings.reset}
            </Button>
          </Field>
        )}
        <Field name={S.settings.sandbox} note={S.settings.sandboxNote}>
          <span className="lnr-grow lnr-row lnr-row--tight">
            <StatusChip tone={sb.tone} dot>{sb.label}</StatusChip>
            <span className="dk-small dk-muted">{sb.note}</span>
          </span>
          {sb.action && <Button size="sm" onClick={onOpenGuide}>{S.settings.setUpSandbox}</Button>}
        </Field>
      </Card>

      <Card title={S.settings.startup}>
        <Field id={`${ids}-autostart`} name={S.settings.autoStart} note={S.settings.autoStartNote}>
          <span className="lnr-grow" />
          {sw(`${ids}-autostart`, settings.autoStartServer, (v) => void saveNow({ autoStartServer: v }))}
        </Field>
        <Field id={`${ids}-login`} name={S.settings.login} note={loginNote ?? S.settings.loginNote}>
          <span className="lnr-grow" />
          {sw(`${ids}-login`, settings.startOnBoot, (v) => void saveNow({ startOnBoot: v }))}
        </Field>
        <Field id={`${ids}-models`} name={S.settings.models} note={S.settings.modelsNote}>
          {folder(`${ids}-models`, settings.modelsPath, 'models')}
        </Field>
      </Card>
    </>
  )

  const network = (
    <Card title={S.settings.network}>
      <Field id={`${ids}-addr`} name={S.settings.address} note={S.settings.addressNote}>
        <input
          className="dk-input dk-input--mono lnr-field__input"
          value={draft.address}
          onChange={(e) => edit('address', e.target.value)}
          aria-labelledby={`${ids}-addr-name`}
          aria-describedby={`${ids}-addr-note`}
          spellCheck={false}
        />
      </Field>
      <Field name={S.settings.otherDevices} note={S.settings.otherDevicesNote}>
        <span className="lnr-grow">
          {net && (
            <StatusChip tone={net.exposed ? (net.apiKeySet ? 'accent' : 'error') : 'idle'} dot testId="network-status">
              {net.exposed ? (net.apiKeySet ? S.settings.otherDevicesOn : S.settings.otherDevicesOnNoKey) : S.settings.otherDevicesOff}
            </StatusChip>
          )}
        </span>
        <Button size="sm" onClick={onConnect}>{S.settings.openConnect}</Button>
      </Field>
    </Card>
  )

  const performance = <EmptyState variant="unavailable" icon="gauge" title={S.settings.perfTitle} body={S.settings.perfBody} level={3} />

  const advanced = (
    <Card title={S.settings.advanced}>
      <Field id={`${ids}-level`} name={S.settings.logLevel} note={S.settings.logLevelNote}>
        <span className="lnr-grow" />
        <span className="dk-select-wrap lnr-field__select">
          <select
            className="dk-select"
            value={draft.logLevel}
            onChange={(e) => edit('logLevel', e.target.value as ServerLogLevel)}
            aria-labelledby={`${ids}-level-name`}
            aria-describedby={`${ids}-level-note`}
          >
            {LOG_LEVELS.map((l) => (
              <option key={l} value={l}>{l}</option>
            ))}
          </select>
        </span>
      </Field>
      <Field id={`${ids}-backends`} name={S.settings.backends} note={S.settings.backendsNote}>
        {folder(`${ids}-backends`, settings.backendsPath, 'backends')}
      </Field>
      <EnvVarsField
        plainFileOn={settings.secretsPlaintextFile}
        onPlainFile={(on) => saveNow({ secretsPlaintextFile: on })}
        onRestartNeeded={() => setRestartNote(true)}
      />
      {b.getWorker && (
        <Field name={S.settings.runMode} note={data.worker?.mode === 'worker' ? S.settings.runModeWorker : S.settings.runModeLocal}>
          <span className="lnr-grow" />
          <Button size="sm" onClick={onWorker}>{S.settings.changeMode}</Button>
        </Field>
      )}
    </Card>
  )

  const servers = data.servers ? <ServersTab view={data.servers} onSwitch={onSwitch} open={serversOpen} /> : null
  const updates = <UpdatesTab data={data} controls={controls} now={now} />
  const panels: Record<SettingsTab, React.ReactNode> = { general, servers, updates, network, performance, advanced }

  return (
    <Page className="lnr-screen" data-screen="settings">
      <h1 className="lnr-h">{S.settings.title}</h1>
      {data.info?.configNotice && (
        <Callout tone="warn" title={S.banner.configTitle} className="lnr-screen__banner">
          {S.banner.configBody}
        </Callout>
      )}
      <div className="dk-tabs lnr-tabs" role="tablist" aria-label={S.settings.tabsLabel}>
        {TABS.map((t, i) => (
          <button
            key={t}
            ref={(el) => {
              tabRefs.current[i] = el
            }}
            type="button"
            role="tab"
            id={`${ids}-tab-${t}`}
            className="dk-tab"
            aria-selected={tab === t}
            aria-controls={`${ids}-panel`}
            tabIndex={tab === t ? 0 : -1}
            onClick={() => setTab(t)}
            onKeyDown={(e) => onTabKey(e, i)}
          >
            {S.settings.tabs[t]}
          </button>
        ))}
      </div>
      <div id={`${ids}-panel`} role="tabpanel" aria-labelledby={`${ids}-tab-${tab}`} className="lnr-stack">
        {error && <Callout tone="error" role="alert" title={error} />}
        {restartNote && changes === 0 && (
          <Callout
            tone="info"
            role="status"
            title={S.settings.savedRestart}
            actions={running && <Button size="sm" onClick={async () => { await b.restart(); setRestartNote(false) }}>{S.settings.restartNow}</Button>}
          />
        )}
        {panels[tab]}
      </div>
      {changes > 0 && (
        <div className="lnr-row lnr-actions" role="region" aria-label={S.settings.title}>
          <StatusChip tone="warn">{running ? S.settings.pending(changes) : S.settings.unsaved(changes)}</StatusChip>
          <span className="lnr-grow" />
          <Button onClick={discard}>{S.settings.discard}</Button>
          <Button variant="primary" busy={saving} onClick={apply} data-testid="settings-apply">
            {running ? S.settings.applyRestart : S.settings.save}
          </Button>
        </div>
      )}
    </Page>
  )
}

/** What the Launch at login row says after a change that did not take effect; undefined after one that did. */
function loginItemNote(r: LoginItemView): string | undefined {
  if (r.status === 'ok') return undefined
  if (r.status === 'unavailable') return S.settings.loginUnavailable
  return r.reason === 'requires-approval' ? S.settings.loginApproval : S.settings.loginFailed
}
