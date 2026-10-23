import { useTranslation } from 'react-i18next'
import ActionMenu from '../ActionMenu'

// The overflow menu of an add-on card or its preview: Show in sidebar,
// Details and Remove. Reordering a pinned add-on lives here too, so the card
// carries no bare arrows. `onDetails` is left out where the details are
// already on screen; `onRemove` is left out for someone who cannot remove.
export default function AddonMenu({ name, pinned, onPin, onMove, onDetails, onRemove, compact = true }) {
  const { t } = useTranslation('addons')
  const items = [
    { key: 'pin', icon: 'pin', label: pinned ? t('card.hideFromSidebar') : t('card.showInSidebar'), onClick: onPin },
    { key: 'earlier', icon: 'chevron-up', label: t('drawer.moveEarlier', { name }), hidden: !pinned || !onMove, onClick: () => onMove(-1) },
    { key: 'later', icon: 'chevron-down', label: t('drawer.moveLater', { name }), hidden: !pinned || !onMove, onClick: () => onMove(1) },
    { key: 'details', icon: 'info', label: t('card.details'), hidden: !onDetails, onClick: onDetails },
    { divider: true, key: 'divider', hidden: !onRemove },
    { key: 'remove', icon: 'trash', label: t('card.remove'), danger: true, hidden: !onRemove, onClick: onRemove },
  ]
  return <ActionMenu items={items} ariaLabel={t('card.menu', { name })} compact={compact} />
}
