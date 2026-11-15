import { forwardRef } from 'react'
import { Link } from 'react-router-dom'
import KitButton from '../../../packages/ui-kit/src/Button.jsx'

// The kit's Button (packages/ui-kit) with the app's router link, so
// <Button to="/app/x"> renders a router <Link>. Props: see the kit file.
const Button = forwardRef(function Button(props, ref) {
  return <KitButton ref={ref} linkComponent={Link} {...props} />
})

export default Button
