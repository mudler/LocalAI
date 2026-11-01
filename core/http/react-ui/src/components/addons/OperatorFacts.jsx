/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useTranslation } from 'react-i18next'
import Button from '../ui/Button'
import { StateChip, Fact, UnknownNotice } from './AddonBits'
import { formatBytes, rangeText, shortDigest } from './addonState'
import { describeError } from './errors'
import { updateView, formatWhen, upgradeAvailability } from './updateState'
import { upgradeFailureWords } from './updateReview'
import RecoveryPanel from './RecoveryPanel'
import { addonUpgrades } from '../../utils/addonUpgrades'
import { reasonText } from './UpgradeControls'
import { UpgradeReason } from '../upgrade/UpgradeBits'

// What an administrator needs to run an add-on, shown in the preview pane under
// Details: health, trust, compatibility, storage, data recovery and updates.
// It reads the installed record of the add-on, which the host serves to
// administrators only; the host checks that again on every call.
export function OperatorFacts({ addon, api, language, actions, check, upgraded, upgradeOp, onCheck, onUpgrade }) {
  const { t } = useTranslation('addons')
  const working = busy(actions, addon, ['start', 'stop', 'enable', 'disable', 'uninstall']) || isUpgrading(upgradeOp)
  const compat = addon.compatibility || {}
  return (
    <div className="ad-facts-body" data-testid="addon-manage">
      <section className="ad-section" aria-labelledby="manage-trust">
        <h3 id="manage-trust" className="ad-section__title">{t('manage.trust')}</h3>
        <dl className="dk-kv ad-kv">
          <dt>{t('trust.verification')}</dt><dd>{t(`trust.mode.${addon.verification === 'sha256+sigstore' ? 'signed' : 'digest'}`)}</dd>
          <dt>{t('trust.library')}</dt><dd>{addon.sourceId || '—'}</dd>
          {addon.channel && (<><dt>{t('trust.channel')}</dt><dd>{addon.channel}</dd></>)}
          {addon.selection && (<><dt>{t('trust.selection')}</dt><dd>{addon.selection}</dd></>)}
          <dt>{t('trust.digest')}</dt><dd className="dk-mono ad-wrap" title={addon.digest}>{shortDigest(addon.digest)}</dd>
        </dl>
      </section>

      <section className="ad-section" aria-labelledby="manage-health">
        <h3 id="manage-health" className="ad-section__title">{t('manage.health')}</h3>
        <dl className="dk-kv ad-kv">
          <dt>{t('manage.desired')}</dt><dd>{addon.enabled ? t('installed.enabled') : t('installed.disabled')}</dd>
          <dt>{t('manage.observed')}</dt><dd><StateChip app={addon} /></dd>
          <dt>{t('manage.restarts')}</dt><dd>{addon.restarts}</dd>
          {addon.installedAt && formatWhen(addon.installedAt, language) && (
            <><dt>{t('manage.installedAt')}</dt><dd data-testid="manage-installed-at"><time dateTime={addon.installedAt}>{formatWhen(addon.installedAt, language)}</time></dd></>
          )}
        </dl>
        {addon.enabled && addon.state === 'failed' && <p className="ad-entry__note" data-tone="error" role="alert">{t('manage.failedNote', { count: addon.restarts })}</p>}
      </section>

      <section className="ad-section" aria-labelledby="manage-updates" data-testid="manage-updates">
        <h3 id="manage-updates" className="ad-section__title">{t('updates.title')}</h3>
        <UpdatePanel addon={addon} t={t} language={language} actions={actions} check={check} upgraded={upgraded} upgradeOp={upgradeOp} working={working} onCheck={onCheck} onUpgrade={onUpgrade} />
      </section>

      <section className="ad-section" aria-labelledby="manage-compat">
        <h3 id="manage-compat" className="ad-section__title">{t('manage.compatibility')}</h3>
        <dl className="dk-kv ad-kv">
          <dt>{t('trust.platform')}</dt><dd>{addon.platform.os}/{addon.platform.arch}</dd>
          <dt>{t('manage.host')}</dt><dd>{rangeText(compat.host)}</dd>
          <dt>{t('manage.api')}</dt><dd>{rangeText(compat.api)}</dd>
          <dt>{t('manage.protocol')}</dt><dd>{rangeText(compat.protocol)}</dd>
          <dt>{t('manage.dataFormat')}</dt><dd>{rangeText(compat.dataFormat)}</dd>
          <dt>{t('manage.tools')}</dt><dd>{addon.toolCount}{addon.tools?.length ? `: ${addon.tools.join(', ')}` : ''}</dd>
          <dt>{t('manage.interface')}</dt><dd>{addon.hasUI ? t('manage.hasUI') : t('manage.headless')}</dd>
        </dl>
      </section>

      <section className="ad-section" aria-labelledby="manage-storage">
        <h3 id="manage-storage" className="ad-section__title">{t('manage.storage')}</h3>
        <dl className="dk-kv ad-kv">
          <dt>{t('manage.data')}</dt><dd>{formatBytes(addon.storage?.bytes)}{addon.storage?.truncated ? ` ${t('manage.atLeast')}` : ''}</dd>
          <dt>{t('manage.generations')}</dt><dd>{addon.generations}</dd>
        </dl>
      </section>

      <section className="ad-section" aria-labelledby="manage-recovery" data-testid="manage-recovery">
        <h3 id="manage-recovery" className="ad-section__title">{t('recovery.title')}</h3>
        <RecoveryPanel api={api} addon={addon} />
      </section>

      <section className="ad-section" aria-labelledby="manage-logs">
        <h3 id="manage-logs" className="ad-section__title">{t('manage.logs')}</h3>
        <p className="ad-quiet">{t('manage.logsNone')}</p>
      </section>
    </div>
  )
}

// An upgrade of this add-on that is running or being canceled. It is kept by
// the upgrade store, so it goes on when this panel is closed.
function isUpgrading(op) {
  return Boolean(op) && (op.state === 'running' || op.state === 'cancelling')
}

function busy(actions, addon, verbs) {
  if (actions.unknown[addon.id]) return true
  return verbs.some(v => actions.state[`${v}:${addon.id}:${addon.generation}`]?.phase === 'working')
}

// The owner's update check of one add-on: what it says, how old it is, and the
// two actions. Check only reads; Upgrade opens a confirmation first.
function UpdatePanel({ addon, t, language, actions, check, upgraded, upgradeOp, working, onCheck, onUpgrade }) {
  const view = updateView(addon.update)
  const checking = check && check.phase === 'working'
  const upgrading = isUpgrading(upgradeOp)
  const failedUpgrade = upgradeOp && upgradeOp.state === 'failed' ? upgradeOp : null
  const checkedAt = formatWhen(view.checkedAt, language)
  const lastSuccess = formatWhen(view.lastSuccess, language)
  return (
    <>
      <p data-testid="update-state" data-state={view.state} className={view.state === 'check-failed' || view.state === 'integrity-error' ? 'ad-entry__note' : undefined} data-tone={view.tone === 'error' ? 'error' : undefined}>
        {t(`updates.state.${view.state}`, { version: view.version })}
      </p>
      {!view.canUpgrade && <UpgradeReason text={reasonText(upgradeAvailability(addon, { isAdmin: true }), t)} testId="update-why" />}
      <dl className="dk-kv ad-kv">
        <dt>{t('updates.installed')}</dt>
        <dd>{addon.version} <span className="dk-mono ad-wrap" title={addon.digest}>{shortDigest(addon.digest)}</span></dd>
        {view.version && (view.state === 'update-available' || view.state === 'incompatible' || view.state === 'integrity-error') && (
          <>
            <dt>{t('updates.candidate')}</dt>
            <dd data-testid="update-candidate">{view.version} {view.digest && <span className="dk-mono ad-wrap" title={view.digest}>{shortDigest(view.digest)}</span>}</dd>
          </>
        )}
        <dt>{t('trust.library')}</dt><dd>{addon.sourceId || '—'}{addon.channel ? ` · ${addon.channel}` : addon.selection ? ` · ${addon.selection}` : ''}</dd>
      </dl>
      {view.reasons.length > 0 && (
        <div data-testid="update-reasons">
          <p className="ad-quiet">{t('updates.reasons')}</p>
          <ul className="ad-reasons">{view.reasons.map(r => <li key={r}>{t(`updates.reason.${r}`)}</li>)}</ul>
        </div>
      )}
      {view.state === 'check-failed' && view.error && <p className="ad-entry__note" data-tone="error" role="status" data-testid="update-error">{t(`updates.class.${view.error}`)}</p>}
      <p className="ad-quiet" data-testid="update-times">
        {checkedAt ? t('updates.checkedAt', { when: checkedAt }) : t('updates.neverChecked')}
        {lastSuccess ? ` · ${t('updates.lastSuccess', { when: lastSuccess })}` : ''}
      </p>
      {view.stale && <p className="ad-quiet" data-testid="update-stale">{t('updates.stale')}</p>}
      {check && check.phase === 'done' && check.throttled && <p className="ad-quiet" role="status">{t('updates.throttled')}</p>}
      {check && check.phase === 'error' && <p className="ad-entry__note" data-tone="error" role="alert">{describeError(check.error, t)}</p>}
      {upgrading && !upgradeOp.waiting && <p className="ad-entry__note" role="status" data-testid="upgrade-progress">{t('updates.progress', { name: addon.title })}</p>}
      {upgrading && upgradeOp.waiting && (
        <p className="ad-entry__note" role="status" data-testid="upgrade-waiting">
          {t('updates.waitingService', { seconds: Math.max(1, Math.round(upgradeOp.waiting.nextMs / 1000)) })}{' '}
          <Button size="sm" variant="ghost" icon="refresh" onClick={() => addonUpgrades.checkNow(upgradeOp.jobID)} data-testid="upgrade-check-now">{t('updates.checkAgain')}</Button>
        </p>
      )}
      {upgraded && !working && <p className="ad-entry__note" data-tone="ok" role="status">{t('updates.done', { version: addon.version })}</p>}
      {actions.unknown[addon.id] && <UnknownNotice addonId={addon.id} actions={actions} />}
      {failedUpgrade && <p className="ad-entry__note" data-tone="error" role="alert" data-testid="upgrade-error">{upgradeFailureWords(failedUpgrade.errorKey, t)}</p>}
      <div className="ad-section__acts">
        <Button size="sm" icon="refresh" busy={checking} disabled={checking || working} onClick={onCheck} data-testid="update-check">
          {checking ? t('updates.checking') : t('updates.checkNow')}
        </Button>
        {view.canUpgrade && (
          <Button variant="primary" size="sm" icon="arrow-up" disabled={working} onClick={onUpgrade} data-testid="update-upgrade">
            {t('updates.upgrade')}
          </Button>
        )}
      </div>
    </>
  )
}
