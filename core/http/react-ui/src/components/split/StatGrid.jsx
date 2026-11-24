// StatGrid is the headline numbers at the top of a detail pane.
//
// A description list rather than a row of divs, because that is what it is:
// each cell is a term and its value, and a screen reader should be able to say
// so. Values are tabular-figured in CSS so two panes read as the same table
// even though they are not one.
//
// stats: [{ label, value, tone }] - tone is 'ok' | 'bad' | 'warn', reserved for
// the cell whose value changes what you would do next. A grid where every cell
// is coloured has no emphasis left to spend.
export default function StatGrid({ stats }) {
  const shown = stats.filter(Boolean)
  if (shown.length === 0) return null
  return (
    <dl className="stat-grid">
      {shown.map(s => (
        <div className="stat-grid__item" key={s.label}>
          <dt>{s.label}</dt>
          <dd className={s.tone ? `stat-grid__value--${s.tone}` : undefined}>{s.value}</dd>
        </div>
      ))}
    </dl>
  )
}
