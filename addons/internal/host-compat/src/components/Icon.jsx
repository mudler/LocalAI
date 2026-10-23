import { getSdk } from '../runtime.js'

// The host's icon, through the SDK. A name the SDK does not list draws the
// neutral glyph (see ICON_NAMES in the host SDK).
export default function Icon(props) {
  const Host = getSdk().Icon
  return <Host {...props} />
}
