import { Tag } from './ui'
// MetaBadgeRow renders the System / User / Meta / Dev tags of a backend the
// same way everywhere, so the same backend type doesn't read "User" in one
// tab and "downloaded" in another.
export default function MetaBadgeRow({ isSystem, isMeta, isDevelopment }) {
  return (
    <div className="ui-tags">
      {isSystem ? (
        <Tag tone="accent" icon="shield" title="Bundled with the LocalAI runtime">System</Tag>
      ) : (
        <Tag icon="download" title="Installed from the gallery or external source">User</Tag>
      )}
      {isMeta && (
        <Tag tone="accent" icon="layers" title="Meta backend — selects a concrete variant per node">Meta</Tag>
      )}
      {isDevelopment && (
        <Tag tone="warn" icon="flask" title="Marked as development / pre-release by the gallery">Dev</Tag>
      )}
    </div>
  )
}
