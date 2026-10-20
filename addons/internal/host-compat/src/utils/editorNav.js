// Navigation context for the Model Editor.
//
// Many pages link into the Model Editor (Models, Manage, Chat, Talk, Agent
// Jobs, Middleware…). Its in-page Back button used to navigate to a hardcoded
// route, so it always dumped you on the same page regardless of where you came
// from. To fix that, every linker passes this object as react-router location
// state; the editor reads it and returns you to the exact page that linked
// here, labelled "Back to <label>".
//
// `location` is the source page's useLocation() value, so `from` captures the
// full path including any sub-route or query string — returning lands you
// where you actually were, not just on the section root.
export function fromState(location, label) {
  return { from: location.pathname + location.search, fromLabel: label }
}
