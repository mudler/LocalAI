/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
// The parts that the pages of the Google add-ons (and any connector add-on
// with a write mode and an approval card) share. The classes are the cn-
// ones of connector.css; only the test ids carry the name of the add-on.
import { Field, Notice, RadioCards } from './controls'

// WriteModeField is the write mode choice and the note under it. modes lists
// the modes offered and modeOf turns the stored value into one of them.
export function WriteModeField({ ns, form, setForm, t, modes, modeOf }) {
  const mode = modeOf(form.write_mode)
  return (
    <div data-testid={`${ns}-write-mode`} className="cn-modeblock">
      <Field label={t('writeMode.label')} hint={t('writeMode.hint')}>
        {() => (
          <RadioCards name="write-mode" legend={t('writeMode.label')} value={mode} onChange={(m) => setForm({ ...form, write_mode: m })}
            options={modes.map((m) => ({
              value: m,
              label: (<span className="cn-modecard"><span className="cn-modecard__title">{t(`writeMode.${m}`)}</span><span className="cn-modecard__line">{t(`writeMode.line.${m}`)}</span></span>),
            }))} />
        )}
      </Field>
      <Notice tone={mode === 'off' ? 'info' : 'warn'} role="note" testId={`${ns}-write-note`} className="cn-note">{t(`writeMode.note.${mode}`)}</Notice>
    </div>
  )
}

// WriteModeChip is the small label of a write mode in a connection row.
export function WriteModeChip({ ns, mode, t }) {
  return <span className={`cn-chip cn-chip--${mode}`} data-testid={`${ns}-row-write`}>{t(`writeMode.row.${mode}`)}</span>
}

// ProposalFrame draws the details of a change that waits for the person,
// inside the approval frame of the host. The frame owns Confirm and Cancel.
export function ProposalFrame({ ns, title, children }) {
  return (
    <div className="cn-prop" data-testid={`${ns}-proposal`}>
      <h3 className="cn-prop__title">{title}</h3>
      {children}
    </div>
  )
}

// ProposalFacts is the list of labelled facts of a proposal.
export function ProposalFacts({ children }) {
  return <dl className="cn-prop__facts">{children}</dl>
}

export function ProposalRow({ label, children, testId }) {
  return (
    <div className="cn-prop__row" data-testid={testId}>
      <dt>{label}</dt>
      <dd>{children}</dd>
    </div>
  )
}

// ProposalText is the exact text that will be saved or sent, drawn as text.
export function ProposalText({ ns, label, children }) {
  return (
    <>
      <p className="cn-prop__label" id={`${ns}-prop-text`}>{label}</p>
      <blockquote className="cn-prop__text" aria-labelledby={`${ns}-prop-text`} data-testid={`${ns}-proposal-text`}>{children}</blockquote>
    </>
  )
}
