// The one place that imports the workspace change feed, so the well-known file
// surfaces do not depend on where the feed lives.
export { useWorkspaceFeed, invalidate } from './useWorkspaceFeed'
