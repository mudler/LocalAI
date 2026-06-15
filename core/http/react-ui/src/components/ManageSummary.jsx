import StatCard from './StatCard'

// ManageSummary anchors the Manage page with the same StatCard pattern the
// Nodes dashboard uses, so the page reads as a real overview rather than
// "two tabs in a hat". Counts are derived in-memory by the parent — this
// component is purely presentational. Cards are clickable and route the
// user to the relevant tab + filter.
export default function ManageSummary({
  modelsCount,
  backendsCount,
  runningCount,
  updatesCount,
  onCardClick,
}) {
  const click = (tab, filter) => onCardClick && onCardClick(tab, filter)

  return (
    <div className="stat-grid manage-summary">
      <StatCard
        icon="fas fa-brain"
        label="Models Installed"
        value={modelsCount}
        onClick={() => click('models', 'all')}
      />
      <StatCard
        icon="fas fa-server"
        label="Backends Installed"
        value={backendsCount}
        onClick={() => click('backends', 'all')}
      />
      <StatCard
        icon="fas fa-circle-play"
        label="Currently Running"
        value={runningCount}
        accentVar={runningCount > 0 ? '--color-success' : undefined}
        onClick={() => click('models', 'running')}
      />
      <StatCard
        icon="fas fa-arrow-up"
        label="Updates Available"
        value={updatesCount}
        accentVar={updatesCount > 0 ? '--color-warning' : undefined}
        onClick={() => click('backends', updatesCount > 0 ? 'upgradable' : 'all')}
      />
    </div>
  )
}
