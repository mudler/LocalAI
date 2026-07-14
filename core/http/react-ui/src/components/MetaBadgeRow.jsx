// MetaBadgeRow renders the System / User / Meta / Dev badge cluster the same
// way everywhere — Manage tabs and (in future) Install gallery. The badges
// already exist as classes; this component locks down the icons + labels so
// the same backend type doesn't read "User" in one tab and "downloaded" in
// another.
export default function MetaBadgeRow({ isSystem, isMeta, isDevelopment }) {
  return (
    <div className="badge-row">
      {isSystem ? (
        <span className="badge badge-info" title="Bundled with the LocalAI runtime">
          <i className="fas fa-shield-alt" /> System
        </span>
      ) : (
        <span className="badge badge-success" title="Installed from the gallery or external source">
          <i className="fas fa-download" /> User
        </span>
      )}
      {isMeta && (
        <span className="badge badge-accent" title="Meta backend — selects a concrete variant per node">
          <i className="fas fa-layer-group" /> Meta
        </span>
      )}
      {isDevelopment && (
        <span className="badge badge-warning" title="Marked as development / pre-release by the gallery">
          <i className="fas fa-flask" /> Dev
        </span>
      )}
    </div>
  )
}
