/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useId, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { clusterApi, nodesApi } from '../../utils/api'
import { copyToClipboard } from '../../utils/clipboard'
import { usePolling } from '../../hooks/usePolling'
import { useImageSelector } from '../ImageSelector'
import { joinCommand, shortPeer } from '../../utils/swarm'
import {
  COMMAND_TABS, DEFAULT_MANY, EXPIRY_CHOICES, MAX_USES, WAYS, availability, chosenWeights, clampUses, commandOf, countdown, maskCommand, maskToken,
  needsInvite, needsToken, secondsUntil, usesLeft,
} from '../../utils/addWorker'
import { nodeConnections } from '../../utils/workerRoster'
import { P2P_SETTINGS_PATH } from '../p2p/P2PTabs'
import { Button, Callout, Segmented, StatusChip, Tabs } from '../ui'
import Icon from '../Icon'
import CommandBlock from './CommandBlock'
import ConnectionChips from './ConnectionChips'
import NodeState from './NodeState'

const WAY_ICON = { nats: 'bolt', websocket: 'globe', p2p: 'share' }

// Seconds of the clock, once a second, for a countdown.
function useNow() {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(timer)
  }, [])
  return now
}

function Countdown({ until, testId }) {
  const now = useNow()
  return <span className="dk-mono" data-testid={testId}>{countdown(secondsUntil(until, now))}</span>
}

// ---- Step 1: choose the connections ---------------------------------------------

function WayCard({ way, checked, onToggle, info, weight }) {
  const { t } = useTranslation('cluster')
  const id = useId()
  const tone = info.state === 'ready' ? 'ok' : info.state === 'unreachable' ? 'warn' : 'idle'
  return (
    <div className="aw-card" data-way={way} data-checked={checked ? 'true' : 'false'} data-disabled={info.available ? undefined : 'true'} data-testid={`way-${way}`}>
      <div className="aw-card__head">
        <input id={id} type="checkbox" className="dk-check aw-card__check" checked={checked} disabled={!info.available} onChange={() => onToggle(way)} />
        <label htmlFor={id} className="aw-card__name">
          <span className="aw-card__icon" aria-hidden="true"><Icon name={WAY_ICON[way]} /></span>
          <span className="aw-card__title">
            <b>{t(`add.way.${way}.name`)}</b>
            <span className="aw-card__tag">{t(`add.way.${way}.tag`)}</span>
          </span>
        </label>
      </div>
      <p className="aw-card__about">{t(`add.way.${way}.about`)}</p>
      <dl className="aw-card__facts">
        <div><dt>{t('add.availability')}</dt><dd><StatusChip tone={tone} dot>{t(`add.way.${way}.${info.state}`)}</StatusChip></dd></div>
        <div><dt>{t('add.needs')}</dt><dd>{t(`add.way.${way}.needs`)}</dd></div>
      </dl>
      {!info.available && info.needs === 'cluster' && (
        <Callout tone="warn" testId={`way-${way}-why`}>{t('add.way.nats.offNote')} <Link className="dk-link" to="/app/settings?group=cluster">{t('add.way.nats.offLink')}</Link></Callout>
      )}
      {!info.available && info.needs === 'sharing' && (
        <Callout tone="warn" testId={`way-${way}-why`}>{t('add.way.p2p.offNote')} <Link className="dk-link" to={P2P_SETTINGS_PATH}>{t('add.way.p2p.offLink')}</Link></Callout>
      )}
      <p className="aw-card__weight">{t('add.weight', { weight })}</p>
    </div>
  )
}

export function ChooseStep({ settings, p2pMode, ways, onWays, onContinue, onSplit }) {
  const { t } = useTranslation('cluster')
  const weights = Object.fromEntries(chosenWeights(WAYS, settings).map(entry => [entry.way, entry.weight]))
  const toggle = way => onWays(ways.includes(way) ? ways.filter(w => w !== way) : [...ways, way])
  return (
    <section className="aw-panel" aria-labelledby="aw-choose" data-testid="add-step-choose">
      <h2 className="aw-panel__title" id="aw-choose">{t('add.choose.title')}</h2>
      <p className="aw-panel__note">{t('add.choose.note')}</p>
      <div className="aw-cards">
        {WAYS.map(way => (
          <WayCard key={way} way={way} checked={ways.includes(way)} onToggle={toggle} info={availability(way, settings, p2pMode)} weight={weights[way]} />
        ))}
      </div>
      <div className="aw-foot">
        {ways.length === 0 && <span className="aw-foot__note" role="status">{t('add.choose.none')}</span>}
        <Button variant="primary" iconEnd="arrow-right" disabled={ways.length === 0} onClick={onContinue} data-testid="add-continue">{t('add.continue')}</Button>
      </div>
      <div className="aw-split">
        <Icon name="boxes" />
        <p><b>{t('add.split.question')}</b> {t('add.split.answer')}</p>
        <Button variant="ghost" size="sm" onClick={onSplit} data-testid="add-split-way">{t('add.split.action')}</Button>
      </div>
    </section>
  )
}

// ---- Step 2: run the command ----------------------------------------------------

// The commands of a join token in tabs, the token hidden on the page and whole
// in the copy.
function CommandTabs({ minted, expired, revealed, addToast }) {
  const { t } = useTranslation('cluster')
  const [tab, setTab] = useState('install')
  const [windows, setWindows] = useState(false)
  const command = commandOf(minted.commands, tab, windows)
  const shown = revealed ? command : maskCommand(command, minted.token)
  const current = COMMAND_TABS.find(entry => entry.id === tab)
  return (
    <div className="aw-cmd" data-testid="command-tabs" data-expired={expired ? 'true' : undefined}>
      <div className="aw-cmd__bar">
        <Tabs label={t('add.tabs.label')} idPrefix="aw" panelId="aw-tabpanel" value={tab} onChange={setTab} itemTestId="tab-" items={COMMAND_TABS.map(entry => ({ id: entry.id, label: t(`add.tabs.${entry.id}`) }))} />
      </div>
      <div id="aw-tabpanel" role="tabpanel" aria-labelledby={`aw-tab-${tab}`} className="aw-cmd__panel">
        {current.alt && (
          <Segmented
            mode="radio" label={t('add.os.label')} value={windows ? 'windows' : 'unix'} onChange={id => setWindows(id === 'windows')}
            options={[{ id: 'unix', label: t('add.os.unix') }, { id: 'windows', label: t('add.os.windows') }]}
          />
        )}
        <CommandBlock command={command} shown={shown} label={t(`add.tabs.${tab}`)} addToast={addToast} disabled={expired} />
        {tab === 'compose' || tab === 'kubernetes' ? <p className="aw-hint">{t(`add.tabNote.${tab}`)}</p> : null}
      </div>
    </div>
  )
}

// The join token: masked with a copy button, when it expires, how many workers
// may use it, and a way to make a new one.
function TokenPanel({ token, uses, onUses, ttl, onTtl, expired, revealed, onReveal, addToast }) {
  const { t } = useTranslation('cluster')
  const minted = token.minted
  const many = uses > 1
  const [manyText, setManyText] = useState(String(many ? uses : DEFAULT_MANY))
  useEffect(() => { if (many) setManyText(String(uses)) }, [many, uses])
  const copyToken = async () => {
    const ok = await copyToClipboard(minted.token)
    addToast?.(ok ? t('add.copied') : t('add.copyFailed'), ok ? 'success' : 'error', 2000)
  }
  return (
    <aside className="aw-token" aria-label={t('add.token.title')} data-testid="join-token">
      <h3 className="aw-token__title"><Icon name="key" /> {t('add.token.title')}</h3>
      <div className="aw-token__box">
        <code className="dk-mono" data-testid="token-text">{revealed ? minted.token : maskToken(minted.token)}</code>
        <Button size="sm" variant="ghost" icon={revealed ? 'eye-off' : 'eye'} aria-label={revealed ? t('add.token.hide') : t('add.token.show')} aria-pressed={revealed} onClick={onReveal} data-testid="token-reveal" />
        <Button size="sm" variant="ghost" icon="copy" aria-label={t('add.token.copy')} onClick={copyToken} data-testid="token-copy" />
      </div>
      <dl className="aw-token__facts">
        <div><dt>{t('add.token.expires')}</dt><dd data-testid="token-expiry">{expired ? t('add.token.expired') : <>{t('add.token.in')} <Countdown until={minted.expires_at} testId="token-countdown" /></>}</dd></div>
        <div><dt>{t('add.token.connections')}</dt><dd>{(minted.routes || []).map(route => t(`conn.name.${route}`)).join(', ') || t('add.token.any')}</dd></div>
      </dl>
      <div className="aw-token__field">
        <span className="aw-label" id="aw-uses-label">{t('add.token.uses')}</span>
        <Segmented
          mode="radio" labelledBy="aw-uses-label" value={many ? 'many' : 'one'}
          onChange={id => onUses(id === 'one' ? 1 : clampUses(manyText))}
          options={[{ id: 'one', label: t('add.token.one') }, { id: 'many', label: t('add.token.many') }]}
        />
        {many && (
          <label className="aw-token__count">
            <span className="aw-hint">{t('add.token.count')}</span>
            <input
              className="dk-input dk-input--mono" inputMode="numeric" value={manyText} data-testid="token-uses" aria-label={t('add.token.count')}
              onChange={event => setManyText(event.target.value)}
              onBlur={() => { const next = clampUses(manyText); setManyText(String(next)); onUses(next) }}
              onKeyDown={event => { if (event.key === 'Enter') event.currentTarget.blur() }}
            />
            <span className="aw-hint">{t('add.token.max', { max: MAX_USES })}</span>
          </label>
        )}
        <p className="aw-hint">{t('add.token.usesHint')}</p>
      </div>
      <div className="aw-token__field">
        <label className="aw-label" htmlFor="aw-ttl">{t('add.token.valid')}</label>
        <span className="dk-select-wrap">
          <select id="aw-ttl" className="dk-select" value={ttl} onChange={event => onTtl(Number(event.target.value))} data-testid="token-ttl">
            {EXPIRY_CHOICES.map(seconds => <option key={seconds} value={seconds}>{t(`add.token.ttl.${seconds}`)}</option>)}
          </select>
        </span>
      </div>
      <Button icon="refresh" busy={token.status === 'minting'} onClick={token.mintAgain} data-testid="mint-again">{t('add.token.again')}</Button>
    </aside>
  )
}

// The P2P invite of a worker that joins over P2P: its link inside the command
// of the existing join, with when it expires.
function InvitePanel({ invite, addToast }) {
  const { t } = useTranslation('cluster')
  const [flavor, setFlavor] = useState('docker')
  const hardware = useImageSelector('cpu')
  const command = joinCommand({ flavor, option: hardware.option, dev: hardware.dev, link: invite.minted?.link })
  return (
    <section className="aw-invite" aria-labelledby="aw-invite-title" data-testid="invite-panel">
      <h3 className="aw-invite__title" id="aw-invite-title"><Icon name="share" /> {t('add.invite.title')}</h3>
      <p className="aw-hint">{t('add.invite.note')}</p>
      {invite.mode !== null && invite.mode !== 'share' && (
        <Callout tone="warn" testId="invite-needs-share">{t('add.way.p2p.offNote')} <Link className="dk-link" to={P2P_SETTINGS_PATH}>{t('add.way.p2p.offLink')}</Link></Callout>
      )}
      {invite.error && <Callout tone="error" role="alert">{invite.error}</Callout>}
      {invite.minted && (
        <>
          <div className="aw-invite__meta" data-testid="worker-invite">
            <StatusChip tone="ok" dot>{t('add.invite.ready')}</StatusChip>
            <span>{t('add.invite.single')} · {t('add.token.expires')} <Countdown until={invite.minted.expires_at} testId="invite-countdown" /></span>
            <Button size="sm" variant="ghost" icon="refresh" busy={invite.busy} onClick={invite.mint} data-testid="invite-again">{t('add.token.again')}</Button>
          </div>
          <div className="aw-invite__choices">
            {flavor === 'docker' && (
              <Segmented
                mode="radio" label={t('add.invite.hardware')} value={hardware.selected} onChange={hardware.setSelected}
                options={hardware.options.map(option => ({ id: option.key, label: option.label }))}
              />
            )}
            <Segmented mode="radio" label={t('add.invite.how')} value={flavor} onChange={setFlavor} options={[{ id: 'docker', label: t('add.tabs.docker') }, { id: 'cli', label: t('add.invite.cli') }]} />
          </div>
          <CommandBlock command={command} label={t('add.invite.title')} addToast={addToast} />
        </>
      )}
      {!invite.minted && invite.mode === 'share' && <p className="aw-hint" role="status">{t('add.invite.making')}</p>}
    </section>
  )
}

export function CommandStep({ settings, ways, token, invite, uses, onUses, ttl, onTtl, addToast, onBack, onNext }) {
  const { t } = useTranslation('cluster')
  const [revealed, setRevealed] = useState(false)
  const now = useNow()
  const minted = token.minted
  const expired = Boolean(minted) && secondsUntil(minted.expires_at, now) === 0
  const names = chosenWeights(ways, settings)
  const list = new Intl.ListFormat(undefined, { style: 'long', type: 'conjunction' })
  return (
    <section className="aw-panel" aria-labelledby="aw-run" data-testid="add-step-run">
      <h2 className="aw-panel__title" id="aw-run">{t('add.run.title')}</h2>
      <div className="aw-summary" data-testid="add-summary">
        <Icon name="nodes" />
        <p>
          <b>{t('add.run.attach', { ways: list.format(names.map(entry => t(`conn.name.${entry.way}`))) })}</b>
          <span className="aw-summary__weights"> · {t('add.run.weights', { weights: list.format(names.map(entry => String(entry.weight))) })}</span>
        </p>
        <Button variant="ghost" size="sm" onClick={onBack} data-testid="add-change">{t('add.run.change')}</Button>
      </div>

      {needsToken(ways) && token.status === 'error' && (
        <Callout tone="error" role="alert" testId="mint-error" actions={<Button size="sm" icon="refresh" onClick={token.mintAgain}>{t('add.token.retry')}</Button>}>{t('add.token.failed', { message: token.error })}</Callout>
      )}
      {needsToken(ways) && !minted && token.status !== 'error' && <p className="aw-hint" role="status">{t('add.token.making')}</p>}
      {needsToken(ways) && minted && (
        <div className="aw-run">
          <div className="aw-run__cmd">
            {expired && <Callout tone="warn" role="status" testId="token-expired" actions={<Button size="sm" variant="primary" icon="refresh" onClick={token.mintAgain}>{t('add.token.again')}</Button>}>{t('add.token.expiredNote')}</Callout>}
            <CommandTabs minted={minted} expired={expired} revealed={revealed} addToast={addToast} />
            <p className="aw-hint"><Icon name="lock" /> {revealed ? t('add.token.shownNote') : t('add.token.hiddenNote')}</p>
          </div>
          <TokenPanel token={token} uses={uses} onUses={onUses} ttl={ttl} onTtl={onTtl} expired={expired} revealed={revealed} onReveal={() => setRevealed(value => !value)} addToast={addToast} />
        </div>
      )}
      {needsInvite(ways) && <InvitePanel invite={invite} addToast={addToast} />}

      <div className="aw-foot aw-foot--split">
        <Button onClick={onBack} data-testid="add-back">{t('add.back')}</Button>
        <Button variant="primary" iconEnd="arrow-right" onClick={onNext} data-testid="add-ran">{t('add.run.ran')}</Button>
      </div>
    </section>
  )
}

// ---- Step 3: wait for the worker -------------------------------------------------

export function WaitStep({ nodes, baseline, token, ways, addToast, onApproved, onBack, onAnother }) {
  const { t } = useTranslation('cluster')
  const { t: ts } = useTranslation('swarm')
  const [approving, setApproving] = useState(null)
  const [startedAt] = useState(() => Date.now())
  const now = useNow()
  const [rows, setRows] = useState(null)
  const minted = token.minted
  const arrived = useMemo(() => (Array.isArray(nodes) && baseline ? nodes.filter(node => !baseline.has(node.id)) : []), [nodes, baseline])

  // The token's own row says how many uses are left.
  usePolling(async () => {
    if (!minted) return
    try { setRows(await clusterApi.joinTokens()) } catch { setRows(null) }
  }, 3000, { enabled: Boolean(minted) })
  const row = Array.isArray(rows) && minted ? rows.find(entry => entry.id === minted.id) : null
  const left = minted ? usesLeft(row, arrived.length, minted.max_uses) : null
  const seconds = minted ? secondsUntil(minted.expires_at, now) : 0

  const approve = async node => {
    setApproving(node.id)
    try {
      await nodesApi.approve(node.id)
      addToast?.(ts('toast.approved', { name: node.name }), 'success')
      await onApproved?.()
    } catch (error) {
      addToast?.(error.message, 'error')
    } finally {
      setApproving(null)
    }
  }

  return (
    <section className="aw-panel" aria-labelledby="aw-wait" data-testid="add-step-wait">
      <h2 className="aw-panel__title" id="aw-wait">{arrived.length > 0 ? t('add.wait.found', { count: arrived.length }) : t('add.wait.title')}</h2>
      {arrived.length === 0 && (
        <div className="aw-listen" role="status" data-testid="listening">
          <span className="aw-listen__dot" aria-hidden="true" />
          <div className="aw-listen__text">
            <b>{t('add.wait.listening')}</b>
            <span>{t('add.wait.listeningNote')}</span>
          </div>
          {minted && (
            <StatusChip tone={seconds === 0 ? 'warn' : 'accent'} dot testId="token-valid">
              {seconds === 0 ? t('add.token.expired') : t('add.wait.valid', { time: countdown(seconds) })}
            </StatusChip>
          )}
        </div>
      )}
      {arrived.map(node => (
        <div key={node.id} className="sw-found" data-testid="found-node">
          <div className="sw-found__text">
            <Link className="dk-table-name dk-mono sw-name" to={`/app/nodes/${encodeURIComponent(node.id)}`}>{node.name}</Link>
            <span className="dk-mono sw-found__addr" title={node.peer_id || undefined}>{node.peer_id ? shortPeer(node.peer_id) : node.address}</span>
            <ConnectionChips connections={nodeConnections(node)} />
            <NodeState node={node} />
          </div>
          <div className="sw-found__acts">
            {node.status === 'pending' && <Button size="sm" variant="primary" busy={approving === node.id} onClick={() => approve(node)}>{ts('actions.approve')}</Button>}
            <Button size="sm" to={`/app/nodes/${encodeURIComponent(node.id)}`}>{t('add.wait.open')}</Button>
          </div>
        </div>
      ))}
      {arrived.length > 0 && minted && (
        <p className="aw-hint" data-testid="uses-left">{left > 0 && seconds > 0 ? t('add.wait.usesLeft', { count: left }) : t('add.wait.usedUp')}</p>
      )}
      {arrived.some(node => node.status === 'pending' && node.peer_id) && <p className="aw-hint">{t('add.wait.checkPeer')}</p>}
      {arrived.length === 0 && <p className="aw-hint">{needsInvite(ways) && !needsToken(ways) ? t('add.wait.stuckInvite') : t('add.wait.stuck')}</p>}
      <div className="aw-foot aw-foot--split">
        <Button onClick={onBack} data-testid="add-back">{t('add.back')}</Button>
        {arrived.length > 0
          ? (
            <div className="aw-foot__acts">
              <Button onClick={onAnother} data-testid="add-another">{t('add.wait.another')}</Button>
              <Button variant="primary" to="/app/nodes" data-testid="add-done">{t('add.wait.done')}</Button>
            </div>
          )
          : null}
      </div>
    </section>
  )
}
