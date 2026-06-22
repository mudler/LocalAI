// Pure landing-page resolver for the index route. Returns a target path, or ''
// meaning "render the default Home". Admin precedence is distributed > p2p >
// plain; non-admins always go to Chat (distributed/p2p are admin-only and
// invisible to them). Visibility gates are enforced elsewhere - this only
// chooses where /app lands.
export function resolveHome({ isAdmin, distributed, p2pEnabled }) {
  if (!isAdmin) return '/app/chat'
  if (distributed) return '/app/nodes'
  if (p2pEnabled) return '/app/p2p'
  return ''
}
