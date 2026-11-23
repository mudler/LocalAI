/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useMemo, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { Button, ListPicker } from '../ui'
import { setupSummary } from '../../utils/setupSummary'
import './composerpickers.css'
import './setups.css'

// The Setup chip of the Home composer: the active setup's name (Setup when
// none) with an "edited" tag when the composer left it, and a ListPicker menu
// to pick another one, with Save as and Manage under the rows.
//
//   list, activeId   the saved setups and the one in use
//   edited           the composer differs from the active setup
//   inactiveReason   when set the chip is off and this is why (a saved agent
//                    decides the chat)
//   reasonId         the id of the visible text that gives inactiveReason
//   onPick(setup), onSaveAs(), onManage()
export default function SetupChip({ list, activeId, edited, inactiveReason = '', reasonId, onPick, onSaveAs, onManage }) {
  const { t } = useTranslation('chat')
  const active = list.find((s) => s.id === activeId)
  const pickerRef = useRef(null)
  // A footer action closes the menu first, as a pick does.
  const then = (fn) => () => { pickerRef.current?.close(); fn() }
  const items = useMemo(() => list.map((s) => ({
    id: s.id,
    label: s.name,
    description: setupSummary(s, t),
    secondary: s.model || t('setup.noModel'),
    setup: s,
  })), [list, t])

  return (
    <div className="setup-chip" data-testid="setup-chip">
      <ListPicker
        ref={pickerRef}
        items={items}
        value={active ? active.id : ''}
        onPick={(row) => onPick(row.setup)}
        openable
        heading={t('setup.menuTitle')}
        emptyText={t('setup.empty')}
        label={t('setup.menuTitle')}
        disabled={!!inactiveReason}
        describedBy={inactiveReason ? reasonId : undefined}
        menuClassName="setup-menu"
        testId="setup-chip"
        triggerProps={{ 'data-testid': 'setup-chip-button', title: inactiveReason || t('setup.title') }}
        triggerContent={(
          <>
            <Icon name="sliders" />
            <span className="home-chip__text setup-chip__name" data-testid="setup-chip-name">{active ? active.name : t('setup.chip')}</span>
            {active && edited && <span className="cp-tag" data-testid="setup-chip-edited">{t('setup.edited')}</span>}
          </>
        )}
        renderRow={(row, { selected }) => (
          <>
            <span className="home-menu__main" data-testid={`setup-option-${row.id}`}>
              <span className="home-menu__line"><span className="home-menu__label">{row.label}</span></span>
              <span className="home-menu__desc">{row.description}</span>
            </span>
            <small>{row.secondary}</small>
            {selected && <Icon name="check" className="home-menu__check" title={t('setup.active')} />}
          </>
        )}
        footer={(
          <div className="setup-menu__foot">
            <Button size="sm" variant="ghost" icon="plus" data-testid="setup-save-as" onClick={then(onSaveAs)}>{t('setup.saveAs')}</Button>
            {list.length > 0 && <Button size="sm" variant="ghost" icon="settings" data-testid="setup-manage" onClick={then(onManage)}>{t('setup.manage')}</Button>}
          </div>
        )}
      />
    </div>
  )
}
