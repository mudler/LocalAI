import { useState } from 'react'
import { apiUrl } from '../utils/basePath'

export default function LoadingSpinner({ size = 'md', className = '' }) {
  const sizeClass = size === 'sm' ? 'spinner-sm' : size === 'lg' ? 'spinner-lg' : 'spinner-md'
  const [imgFailed, setImgFailed] = useState(false)

  return (
    <div className={`spinner ${sizeClass} ${className}`}>
      {imgFailed ? (
        <div className="spinner-ring" />
      ) : (
        <img
          src={apiUrl('/static/logo.png')}
          alt=""
          className="spinner-logo"
          onError={() => setImgFailed(true)}
        />
      )}
    </div>
  )
}
