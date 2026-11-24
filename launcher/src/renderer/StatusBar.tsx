import { useId, useRef } from 'react'
import { Button, StatusChip } from '@localai/ui-kit'
import type { SandboxView, ServerRowView, ServersView, UpdateChannelName } from '../shared/types'
import type { ShellScreenId } from './bridge'
import { BarMenu, menuItems, type MenuAction } from './BarMenu'
import { Popover } from './Popover'
import { SandboxGuide } from './SandboxGuide'
import { ServerSwitcher } from './ServerSwitcher'
import { S } from './strings'
import type { BarActions, ChipView, DotTone, UpdateOffer } from './viewModel'
import logo from '../../build/icon.png'

export type BarPopover = 'menu' | 'guide' | 'about' | 'switcher' | null

export interface StatusBarProps {
  chip: ChipView
  actions: BarActions
  offer: UpdateOffer
  sandboxOff: boolean
  sandbox?: SandboxView
  /** The bar screen on show, highlighted in the bar. */
  active: ShellScreenId | null
  popover: BarPopover
  setPopover: (p: BarPopover) => void
  launcherVersion?: string
  serverVersion?: string
  canOpenInBrowser: boolean
  onStop: () => void
  onRestart: () => void
  onLogs: () => void
  onUpdate: () => void
  onMenu: (a: MenuAction) => void
  onSandbox: (s: SandboxView) => void
  onBrowserInstead?: () => void
  /** The saved servers; without them (an older bridge) the bar has no switcher. */
  servers?: ServersView
  current?: ServerRowView
  /** The Development channel is on and the window shows This computer. */
  devChip?: boolean
  /** A remote is shown and the local server runs beside it. */
  keepLocalNote?: boolean
  channel?: UpdateChannelName
  devBuild?: string
  onSwitch?: (id: string) => void
  onAddServer?: () => void
  onManageServers?: () => void
}

/** The slim bar at the bottom of the window: state, the few verbs, and the menu. */
export function StatusBar(p: StatusBarProps) {
  const menuBtn = useRef<HTMLButtonElement>(null)
  const switchBtn = useRef<HTMLButtonElement>(null)
  const sandboxBtn = useRef<HTMLButtonElement>(null)
  const menuId = useId()
  const aboutId = useId()
  const menuActive = p.active === 'settings' || p.active === 'connect' || p.active === 'worker-settings'
  const close = () => p.setPopover(null)

  return (
    <footer className="lnr-bar" aria-label={S.bar.label}>
      <img className="lnr-bar__logo" src={logo} alt="" />
      {p.servers && p.current && (
        <ServerSwitcher
          rows={p.servers.rows}
          current={p.current}
          open={p.popover === 'switcher'}
          setOpen={(v) => p.setPopover(v ? 'switcher' : null)}
          buttonRef={switchBtn}
          onPick={(id) => p.onSwitch?.(id)}
          onAdd={() => p.onAddServer?.()}
          onManage={() => p.onManageServers?.()}
        />
      )}
      <span className="lnr-bar__status" data-testid="bar-status" data-insecure={p.chip.insecure ? '' : undefined}>
        <StatusChip
          tone={p.chip.tone}
          dot={!p.chip.icon && !p.chip.busy}
          icon={p.chip.icon ?? (p.chip.busy ? 'spinner' : undefined)}
          className="lnr-bar__chip"
        >
          {p.chip.label}
        </StatusChip>
        {p.chip.address && <span className="lnr-bar__addr dk-mono" data-tone={p.chip.tone}>{p.chip.address}</span>}
      </span>
      {p.devChip && (
        <StatusChip tone="warn" icon="flask" className="lnr-bar__dev" title={S.bar.developmentTitle} testId="dev-chip">{S.chip.development}</StatusChip>
      )}
      {p.keepLocalNote && <span className="dk-small dk-muted" data-testid="keep-local-note">{S.bar.localRunning}</span>}
      {p.offer && (
        <Button size="sm" variant="secondary" icon="arrow-up" onClick={p.onUpdate} data-testid="bar-update">
          {p.offer.kind === 'server' ? S.bar.updateTo(p.offer.version) : S.bar.restartToUpdate}
        </Button>
      )}
      {p.sandboxOff && (
        <span className="lnr-anchor">
          <button
            ref={sandboxBtn}
            type="button"
            className="lnr-chipbtn"
            aria-haspopup="dialog"
            aria-expanded={p.popover === 'guide'}
            title={S.bar.sandboxChipTitle}
            onClick={() => p.setPopover(p.popover === 'guide' ? null : 'guide')}
          >
            <StatusChip tone="warn" icon="shield">{S.chip.sandboxOff}</StatusChip>
          </button>
          <SandboxGuide
            open={p.popover === 'guide'}
            onClose={close}
            trigger={sandboxBtn}
            sandbox={p.sandbox}
            onSandbox={p.onSandbox}
            onBrowserInstead={p.onBrowserInstead}
          />
        </span>
      )}
      <span className="lnr-grow" />
      {p.actions.stopRestart && (
        <>
          <Button size="sm" variant="ghost" onClick={p.onStop}>{S.bar.stop}</Button>
          <Button size="sm" variant="ghost" onClick={p.onRestart}>{S.bar.restart}</Button>
        </>
      )}
      {p.actions.logs && (
        <Button
          size="sm"
          variant={p.active === 'logs' ? 'secondary' : 'ghost'}
          icon="terminal"
          aria-current={p.active === 'logs' ? 'page' : undefined}
          onClick={p.onLogs}
        >
          {S.bar.logs}
        </Button>
      )}
      <span className="lnr-anchor">
        <Button
          ref={menuBtn}
          size="sm"
          variant={menuActive || p.popover === 'menu' || p.popover === 'about' ? 'secondary' : 'ghost'}
          icon="more"
          aria-label={S.bar.menu}
          aria-haspopup="menu"
          aria-expanded={p.popover === 'menu'}
          aria-controls={p.popover === 'menu' ? menuId : undefined}
          onClick={() => p.setPopover(p.popover === 'menu' ? null : 'menu')}
          onKeyDown={(e) => {
            if (e.key === 'ArrowUp' || e.key === 'ArrowDown') {
              e.preventDefault()
              p.setPopover('menu')
            }
          }}
        />
        <BarMenu
          id={menuId}
          open={p.popover === 'menu'}
          onClose={close}
          trigger={menuBtn}
          items={menuItems({ remote: false, canOpenInBrowser: p.canOpenInBrowser, serverVersion: p.serverVersion })}
          onPick={(a) => {
            if (a === 'about') {
              p.setPopover('about')
              return
            }
            p.onMenu(a)
            menuBtn.current?.focus()
          }}
        />
        <Popover open={p.popover === 'about'} onClose={close} trigger={menuBtn} kind="popover" className="dk-popover--end lnr-about" role="dialog" labelledBy={aboutId}>
          <strong id={aboutId}>{S.about.title}</strong>
          <dl className="lnr-about__list dk-small">
            <dt className="dk-muted">{S.about.server}</dt>
            <dd className="dk-mono">{p.serverVersion ?? S.about.unknown}</dd>
            <dt className="dk-muted">{S.about.launcher}</dt>
            <dd className="dk-mono">{p.launcherVersion ?? S.about.unknown}</dd>
            {p.channel && (
              <>
                <dt className="dk-muted">{S.about.channel}</dt>
                <dd>{S.about.channels[p.channel]}</dd>
              </>
            )}
          </dl>
          {p.devChip && <StatusChip tone="warn" icon="flask">{S.chip.development}</StatusChip>}
          <Button size="sm" onClick={() => { close(); menuBtn.current?.focus() }} autoFocus>{S.about.close}</Button>
        </Popover>
      </span>
    </footer>
  )
}

const DOT_CLASS: Record<DotTone, string> = { ok: 'dk-dot--ok', warn: 'dk-dot--warn', error: 'dk-dot--error', idle: '' }

/** The corner dot of a hidden bar. It tells the state by colour and brings the bar back. */
export function CornerDot({ tone, stateLabel, onShow }: { tone: DotTone; stateLabel: string; onShow: () => void }) {
  return (
    <span className="dk-tip lnr-corner">
      <button type="button" className="lnr-corner__btn" aria-label={S.bar.dotLabel(stateLabel)} onClick={onShow} data-tone={tone} data-testid="corner-dot">
        <span className={`dk-dot lnr-corner__dot ${DOT_CLASS[tone]}`.trim()} aria-hidden="true" />
      </button>
      <span className="dk-tooltip" role="tooltip">{S.bar.showBar}</span>
    </span>
  )
}
