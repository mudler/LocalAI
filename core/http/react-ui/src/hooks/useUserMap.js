import { useState, useEffect } from 'react'
import { useAuth } from '../context/AuthContext'
import { adminUsersApi } from '../utils/api'

/**
 * Hook that fetches all users and returns a map of userId -> { name, email, avatarUrl }.
 * Only fetches when the current user is admin and auth is enabled.
 */
export function useUserMap() {
  const { isAdmin, authEnabled } = useAuth()
  const [userMap, setUserMap] = useState({})

  useEffect(() => {
    if (!isAdmin || !authEnabled) return
    let cancelled = false
    adminUsersApi.list().then(data => {
      if (cancelled) return
      const users = Array.isArray(data) ? data : (data?.users || [])
      const map = {}
      for (const u of users) {
        map[u.id] = { name: u.name || u.email || u.id, email: u.email, avatarUrl: u.avatar_url }
      }
      setUserMap(map)
    }).catch(() => {})
    return () => { cancelled = true }
  }, [isAdmin, authEnabled])

  return userMap
}
