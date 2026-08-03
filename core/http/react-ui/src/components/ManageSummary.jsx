// The Host page's headline figures.
//
// These were shadowed, clickable StatCards — a second dashboard language on a
// page that already has a rail, a pane and a tab bar. They are now the same
// hairline figure strip the Operate overview uses, so the two pages read as one
// system rather than as two dashboards that happen to share a console.
//
// Each cell still routes into the tab and filter it describes: a count is worth
// more when it is also the way to the thing counted. Counts are derived by the
// parent — this component stays purely presentational.
export default function ManageSummary({
  modelsCount,
  backendsCount,
  runningCount,
  updatesCount,
  onCardClick,
}) {
  const click = (tab, filter) => onCardClick && onCardClick(tab, filter)

  return (
    <div className="stat-strip manage-summary">
      <Figure
        label="Models installed"
        value={modelsCount}
        onClick={() => click('models', 'all')}
      />
      <Figure
        label="Backends installed"
        value={backendsCount}
        onClick={() => click('backends', 'all')}
      />
      <Figure
        label="Running now"
        value={runningCount}
        // Tone only when the number means something. A strip where every cell
        // is coloured has no emphasis left to spend.
        tone={runningCount > 0 ? 'success' : 'muted'}
        onClick={() => click('models', 'running')}
      />
      <Figure
        label="Updates available"
        value={updatesCount}
        tone={updatesCount > 0 ? 'warning' : 'muted'}
        onClick={() => click('backends', updatesCount > 0 ? 'upgradable' : 'all')}
      />
    </div>
  )
}

// Buttons in a plain container rather than a description list. A <button> is
// not valid inside a <dl>, and <dt>/<dd> are not valid inside a <button>: the
// browser re-parents both and the cells collapse to nothing. These cells are a
// set of controls, so saying so is also the honest markup.
function Figure({ label, value, tone = 'primary', onClick }) {
  return (
    <button type="button" className="stat-strip__cell" onClick={onClick}>
      <span className="stat-strip__label">{label}</span>
      <span className={`stat-strip__value stat-strip__value--${tone}`}>{value}</span>
    </button>
  )
}
