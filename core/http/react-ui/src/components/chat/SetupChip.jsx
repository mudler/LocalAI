/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { Button } from '../ui'
import { SwitchHead, SwitchPopover } from './SwitchList'
import { setupSummary } from '../../utils/setupSummary'
import './setups.css'

// The Setup chip of the Home composer: the active setup's name (Setup when
// none), "edited" when the composer left it, and a menu to pick another one,
// save the current state or manage the list.
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
  const label = (
    <>
      <span className="setup-chip__name" data-testid="setup-chip-name">{active ? active.name : t('setup.chip')}</span>
      {active && edited && <span className="setup-chip__edited" data-testid="setup-chip-edited">{t('setup.edited')}</span>}
      <Icon name="chevron-down" className="setup-chip__caret" />
    </>
  )
  return (
    <SwitchPopover
      icon="sliders"
      label={label}
      title={inactiveReason || t('setup.title')}
      count={0}
      testId="setup-chip"
      menuClassName="swl-menu--wide"
      disabled={!!inactiveReason}
      describedBy={inactiveReason ? reasonId : undefined}
    >
      {(close) => (
        <>
          <SwitchHead title={t('setup.menuTitle')} />
          <div className="swl-list">
            {list.length === 0 && <p className="setup-empty">{t('setup.empty')}</p>}
            {list.map((s) => (
              <button
                key={s.id}
                type="button"
                aria-current={s.id === activeId ? 'true' : undefined}
                className="swl-row swl-row--stacked setup-row"
                data-testid={`setup-option-${s.id}`}
                onClick={() => { onPick(s); close() }}
              >
                <span className="swl-row__text">
                  <span className="swl-row__top"><span className="swl-row__name">{s.name}</span></span>
                  <span className="swl-row__desc setup-row__desc">{s.model || t('setup.noModel')}</span>
                  <span className="swl-row__desc">{setupSummary(s, t)}</span>
                </span>
                {s.id === activeId && <Icon name="check-circle" className="setup-row__check" title={t('setup.active')} />}
              </button>
            ))}
          </div>
          <div className="swl-foot">
            <Button size="sm" variant="ghost" icon="plus" data-testid="setup-save-as" onClick={() => { close(); onSaveAs() }}>{t('setup.saveAs')}</Button>
            {list.length > 0 && <Button size="sm" variant="ghost" icon="settings" data-testid="setup-manage" onClick={() => { close(); onManage() }}>{t('setup.manage')}</Button>}
          </div>
        </>
      )}
    </SwitchPopover>
  )
}
