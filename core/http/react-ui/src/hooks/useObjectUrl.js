import { useEffect, useState } from 'react'

// useObjectUrl — derive a blob/object URL from a Blob/File source. Revokes
// the previous URL when the source changes and on unmount, so callers don't
// have to manage URL.createObjectURL lifecycles by hand. Returns null when
// `source` is falsy.
export default function useObjectUrl(source) {
  const [url, setUrl] = useState(null)
  useEffect(() => {
    if (!source) {
      setUrl(null)
      return
    }
    const next = URL.createObjectURL(source)
    setUrl(next)
    return () => URL.revokeObjectURL(next)
  }, [source])
  return url
}
