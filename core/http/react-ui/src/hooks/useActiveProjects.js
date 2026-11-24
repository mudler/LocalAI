import { useCallback, useEffect, useState } from 'react'
import { chatProjectsApi } from '../utils/api'
import { composerProjects } from '../utils/projects'

// The projects of the person for a field that picks one for an agent: the
// whole list (an agent may sit in an archived project, and the field must name
// it) and the active ones. The list is best effort: with no list the person
// can still create a project, so a failed read leaves both empty.
//   all      every project the person has
//   active   the active ones
//   ready    true once the first read ended, whatever its result
//   add      puts a project that was just made in the list
export function useActiveProjects() {
  const [all, setAll] = useState([])
  const [ready, setReady] = useState(false)
  useEffect(() => {
    let live = true
    chatProjectsApi.list()
      .then((r) => { if (live) setAll(r?.projects || []) })
      .catch(() => {})
      .finally(() => { if (live) setReady(true) })
    return () => { live = false }
  }, [])
  const add = useCallback((project) => setAll((prev) => [...prev.filter((p) => p.id !== project.id), project]), [])
  return { all, active: composerProjects(all), ready, add }
}
