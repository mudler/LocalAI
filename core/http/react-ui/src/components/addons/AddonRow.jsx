/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import Button from '../ui/Button'
import StatusChip from '../ui/StatusChip'
import { apiUrl } from '../../utils/basePath'
import { AppIcon, ApprovalChip, BuiltinIcon, OperationChip, StateChip, UpdateChip, VersionText } from './AddonBits'
import AddonMenu from './AddonMenu'
import { addedByAddon, stateChip } from './addonState'
import { isLocalFile } from './fileInstall'

// One installed add-on as a row: its icon, its name with one plain state, one
// line of what it does, and a quiet line of facts (page, tools, version). The
// row body selects the add-on (its details open beside the list). On the right
// is the one action that fits the state, and the overflow menu with Pin,
// reordering, Details and Remove. The state is one chip: a running operation,
// a decision waiting for an administrator, an update, a connection that needs
// setup, else the process state.
export default function AddonRow({ tile, pinned, selected = false, attention = '', needsSetup = false, updateVersion = '', waitingApproval = false, sourceId = '', onPin, onMove, onOpenDetail, onRemove, upgrade, upgradeInline = false }) {
  const { t } = useTranslation('addons')
  const { t: tn } = useTranslation('nav')
  const title = tile.titleKey ? tn(tile.titleKey) : tile.title
  const app = tile.app
  const added = app ? addedByAddon(app) : { page: tile.source === 'builtin', tools: 0 }
  const updating = Boolean(app && !app.operation && !waitingApproval && (app.update === 'update-pending' || updateVersion))
  const setup = Boolean(app && needsSetup && !app.operation && !waitingApproval && attention !== 'failed')
  const stateText = !app ? t('meta.builtin') : setup ? t('state.needsSetup') : t(`state.${stateChip(app).key}`)

  let chip = null
  if (app) {
    if (app.operation) chip = <OperationChip app={app} className="ad-tile__state" />
    else if (waitingApproval) chip = <ApprovalChip />
    else if (attention === 'failed') chip = <StateChip app={app} className="ad-tile__state" />
    else if (setup) chip = <span className="ad-state ad-tile__state" data-state="needs-setup" data-testid="needs-setup-chip"><StatusChip tone="warn" label={t("state.needsSetup")} /></span>
    else if (updating) chip = <UpdateChip version={updateVersion} />
    else chip = <StateChip app={app} className="ad-tile__state" />
  }

  // The one action. A failure or a missing setup leads to the details; an
  // update the administrator can start is the upgrade control; a running page
  // opens.
  let action = null
  if (attention === 'failed' || setup) {
    action = <Button size="sm" onClick={() => onOpenDetail(tile.id)} aria-label={t(setup ? 'card.setUpNamed' : 'card.reviewNamed', { name: title })} data-testid="tile-fix">{t(setup ? 'card.setUp' : 'card.review')}</Button>
  } else if (upgrade && upgradeInline) {
    action = <span className="ad-tile__act" data-testid="tile-upgrade">{upgrade}</span>
  } else if (tile.target) {
    action = <Button size="sm" to={tile.target} aria-label={t('card.openNamed', { name: title })}>{t('apps.open')}</Button>
  }

  return (
    <li className="ad-tile" data-testid="addon-tile" data-app={tile.id} data-state={tile.state} data-attention={attention || (setup ? 'setup' : undefined)} data-selected={selected ? 'true' : undefined}>
      <button
        type="button"
        className="ad-tile__open"
        aria-pressed={selected}
        aria-label={`${title}, ${stateText}`}
        onClick={() => onOpenDetail(tile.id)}
      >
        {tile.source === 'builtin'
          ? <BuiltinIcon name={tile.iconName} size={44} />
          : <AppIcon src={tile.icon ? apiUrl(tile.icon) : ''} id={tile.id} name={title} size={44} />}
        <span className="ad-tile__text">
          <span className="ad-tile__head">
            <span className="ad-tile__title">{title}</span>
            {chip}
          </span>
          {tile.description && <span className="ad-tile__desc">{tile.description}</span>}
          <span className="ad-meta">
            {(added.page || added.tools > 0) && (
              <span className="ad-meta__group" data-testid="tile-adds">
                {added.page && <span className="ad-meta__item"><Icon name="file" aria-hidden="true" /> {t('card.addsPage')}</span>}
                {added.tools > 0 && <span className="ad-meta__item"><Icon name="wrench" aria-hidden="true" /> {t('card.chatTools', { count: added.tools })}</span>}
              </span>
            )}
            {!app && <span className="ad-meta__item">{t('meta.builtinLong')}</span>}
            {app && <span className="ad-meta__item"><VersionText version={app.version} /></span>}
            {app && isLocalFile(sourceId) && <span className="ad-meta__item" data-source={sourceId}><Icon name="shield" aria-hidden="true" /> {t('file.badge')}</span>}
          </span>
        </span>
      </button>
      <div className="ad-tile__side">
        {action}
        <AddonMenu
          name={title}
          pinned={pinned}
          onPin={() => onPin(tile.id)}
          onMove={pinned ? (delta) => onMove(tile.id, delta) : undefined}
          onDetails={() => onOpenDetail(tile.id)}
          onRemove={onRemove && app ? () => onRemove(tile) : undefined}
        />
      </div>
      {upgrade && !upgradeInline && <div className="ad-tile__upgrade" data-testid="tile-upgrade">{upgrade}</div>}
    </li>
  )
}
