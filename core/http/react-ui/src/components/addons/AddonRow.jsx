/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import Button from '../ui/Button'
import { apiUrl } from '../../utils/basePath'
import { AppIcon, BuiltinIcon, StateChip, UpdateChip, VersionText } from './AddonBits'
import AddonMenu from './AddonMenu'
import { addedByAddon, stateChip } from './addonState'

// One installed add-on as a row: identity tile, name and state, one line of
// what it does, and a meta line (page, tools, where it came from, version).
// The row body selects the add-on (its details open beside the list). On the
// right: the action that fits its state (Open, Upgrade), Pin, and the overflow
// menu with reordering, Details and Remove.
export default function AddonRow({ tile, pinned, selected = false, attention = '', source, updateVersion = '', onPin, onMove, onOpenDetail, onRemove, upgrade }) {
  const { t } = useTranslation('addons')
  const { t: tn } = useTranslation('nav')
  const title = tile.titleKey ? tn(tile.titleKey) : tile.title
  const app = tile.app
  const stateText = app ? t(`state.${stateChip(app).key}`) : t('meta.builtin')
  const added = app ? addedByAddon(app) : { page: tile.source === 'builtin', tools: 0 }
  const signed = source && source.verification === 'sha256+sigstore'
  return (
    <li className="ad-tile" data-testid="addon-tile" data-app={tile.id} data-state={tile.state} data-attention={attention || undefined} data-selected={selected ? 'true' : undefined}>
      <button
        type="button"
        className="ad-tile__open"
        aria-pressed={selected}
        aria-label={`${title}, ${stateText}`}
        onClick={() => onOpenDetail(tile.id)}
      >
        {tile.source === 'builtin'
          ? <BuiltinIcon name={tile.iconName} />
          : <AppIcon src={tile.icon ? apiUrl(tile.icon) : ''} id={tile.id} name={title} />}
        <span className="ad-tile__text">
          <span className="ad-tile__head">
            <span className="ad-tile__title">{title}</span>
            {app && <StateChip app={app} className="ad-tile__state" />}
            {app && (app.update === 'update-pending' || updateVersion) && <UpdateChip version={updateVersion} />}
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
            {source && source.sourceId && (
              <span className="ad-meta__item"><Icon name="shield" aria-hidden="true" /> {t(signed ? 'meta.signed' : 'meta.checked', { library: source.sourceId })}</span>
            )}
            {app && <span className="ad-meta__item"><VersionText version={app.version} /></span>}
          </span>
        </span>
      </button>
      <div className="ad-tile__side">
        {tile.target && (
          <Button size="sm" to={tile.target} aria-label={t('card.openNamed', { name: title })}>{t('apps.open')}</Button>
        )}
        <Button
          size="sm"
          variant="ghost"
          icon="pin"
          className="ad-tile__pin"
          aria-pressed={pinned}
          aria-label={pinned ? t('drawer.unpin', { name: title }) : t('drawer.pin', { name: title })}
          title={pinned ? t('card.hideFromSidebar') : t('card.showInSidebar')}
          onClick={() => onPin(tile.id)}
        />
        <AddonMenu
          name={title}
          pinned={pinned}
          onPin={() => onPin(tile.id)}
          onMove={pinned ? (delta) => onMove(tile.id, delta) : undefined}
          onDetails={() => onOpenDetail(tile.id)}
          onRemove={onRemove && app ? () => onRemove(tile) : undefined}
        />
      </div>
      {upgrade && <div className="ad-tile__upgrade" data-testid="tile-upgrade">{upgrade}</div>}
    </li>
  )
}

