// SimplePowerSwitch is the segmented control in the import form header that
// flips between Simple and Power modes. It is deliberately dumb — the
// parent owns the `mode` state and localStorage persistence. Styling reuses
// the existing `.segmented` / `.segmented__item` classes shared with the
// Sound page so the form matches the rest of the app visually.

export default function SimplePowerSwitch({ value, onChange, disabled = false }) {
  const pick = (next) => {
    if (disabled) return
    if (next === value) return
    onChange?.(next)
  }

  return (
    <div
      className="segmented"
      role="tablist"
      aria-label="Import form mode"
      data-testid="simple-power-switch"
      style={{ marginBottom: 0 }}
    >
      <button
        type="button"
        role="tab"
        aria-selected={value === 'simple'}
        className={`segmented__item${value === 'simple' ? ' is-active' : ''}`}
        onClick={() => pick('simple')}
        disabled={disabled}
        data-testid="mode-simple"
        title="Simple mode — just a URI + Import button"
      >
        <i className="fas fa-magic" aria-hidden="true" />
        Simple
      </button>
      <button
        type="button"
        role="tab"
        aria-selected={value === 'power'}
        className={`segmented__item${value === 'power' ? ' is-active' : ''}`}
        onClick={() => pick('power')}
        disabled={disabled}
        data-testid="mode-power"
        title="Advanced mode — full preferences + YAML editor"
      >
        <i className="fas fa-sliders" aria-hidden="true" />
        Advanced
      </button>
    </div>
  )
}
