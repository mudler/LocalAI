/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { LOOK_OPTIONS, LOOK_DEFAULTS } from '../../utils/look'

// Choice is one value of a list as a pressed card, in a radio group: the
// sample on top, the name under it.
function Choices({ label, list, value, onPick, disabled, testId, render }) {
  return (
    <div className="wk-choices" role="radiogroup" aria-label={label} data-testid={testId}>
      {list.map(v => (
        <button
          key={v} type="button" role="radio" aria-checked={value === v} className="wk-choice" disabled={disabled}
          data-testid={`${testId}-${v}`} onClick={() => onPick(v)}
        >
          {render(v)}
        </button>
      ))}
    </div>
  )
}

// LookField edits the look of ui.json: font, corners, message style, width and
// code colours, each from its fixed list. A value equal to the default is
// left out of the file, so the file holds only what the user changed. Keys
// the editor does not know stay in the object.
export default function LookField({ id, name, value, onChange, disabled, describedBy }) {
  const { t } = useTranslation('workspace')
  const v = useMemo(() => (value && typeof value === 'object' && !Array.isArray(value) ? value : {}), [value])
  const cur = (k) => (LOOK_OPTIONS[k].includes(v[k]) ? v[k] : LOOK_DEFAULTS[k])
  const set = (k, val) => {
    const next = { ...v }
    if (val === LOOK_DEFAULTS[k]) delete next[k]
    else next[k] = val
    onChange(next)
  }
  const opt = (k, val) => t(`wellKnown.look.${k}.${val}`)
  const head = (k) => <span className="wk-look__label">{t(`wellKnown.look.${k}.label`)}</span>

  return (
    <div className="wk-look" id={id} aria-describedby={describedBy} data-testid={`wk-field-${name}-look`}>
      {head('font')}
      <Choices
        label={t('wellKnown.look.font.label')} list={LOOK_OPTIONS.font} value={cur('font')} onPick={(x) => set('font', x)} disabled={disabled} testId="wk-look-font"
        render={(f) => (<><span className={`wk-choice__aa wk-font--${f}`} aria-hidden="true">Aa</span><span>{opt('font', f)}</span></>)}
      />

      <div className="wk-look__pair">
        <div>
          {head('radius')}
          <div className="dk-segmented" role="radiogroup" aria-label={t('wellKnown.look.radius.label')} data-testid="wk-look-radius">
            {LOOK_OPTIONS.radius.map(r => (
              <button key={r} type="button" role="radio" aria-checked={cur('radius') === r} className="dk-seg" disabled={disabled} data-testid={`wk-look-radius-${r}`} onClick={() => set('radius', r)}>{opt('radius', r)}</button>
            ))}
          </div>
        </div>
        <div>
          {head('messageWidth')}
          <div className="dk-segmented" role="radiogroup" aria-label={t('wellKnown.look.messageWidth.label')} data-testid="wk-look-width">
            {LOOK_OPTIONS.messageWidth.map(w => (
              <button key={w} type="button" role="radio" aria-checked={cur('messageWidth') === w} className="dk-seg" disabled={disabled} data-testid={`wk-look-width-${w}`} onClick={() => set('messageWidth', w)}>{opt('messageWidth', w)}</button>
            ))}
          </div>
        </div>
      </div>

      {head('bubble')}
      <Choices
        label={t('wellKnown.look.bubble.label')} list={LOOK_OPTIONS.bubble} value={cur('bubble')} onPick={(x) => set('bubble', x)} disabled={disabled} testId="wk-look-bubble"
        render={(b) => (<><span className={`wk-choice__bubble wk-bubble--${b}`} aria-hidden="true" /><span>{opt('bubble', b)}</span></>)}
      />

      {head('codeTheme')}
      <Choices
        label={t('wellKnown.look.codeTheme.label')} list={LOOK_OPTIONS.codeTheme} value={cur('codeTheme')} onPick={(x) => set('codeTheme', x)} disabled={disabled} testId="wk-look-code"
        render={(c) => (<><span className={`wk-choice__code wk-code--${c}`} aria-hidden="true"><span className="wk-code__k">def</span> run()</span><span>{opt('codeTheme', c)}</span></>)}
      />
      <p className="wk-look__note">{t('wellKnown.look.note')}</p>
    </div>
  )
}
