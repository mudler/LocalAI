// Saving the setups document with one retry on a conflict. Pure, so the node
// unit tests load it.

const copy = (v) => JSON.parse(JSON.stringify(v ?? null))

// saveWithRetry applies change to a fresh copy of current and saves the result
// with replace. On a conflict (another tab wrote first) it applies change once
// more to the newer data of the answer and saves on top of the newer version;
// a second conflict, or any other error, is thrown. It resolves to the data
// that was saved. save is the save of useWellKnown. version is the version
// current was read at: the first save sends it, so a base that is out of date
// meets a conflict and takes the retry, instead of overwriting a newer file.
export async function saveWithRetry(save, current, change, version) {
  let base = current
  for (let attempt = 0; ; attempt++) {
    const next = change(copy(base))
    try {
      await save(next, { replace: true, version })
      return next
    } catch (e) {
      if (!e?.conflict || attempt > 0) throw e
      base = e.current?.data
      version = e.current?.version
    }
  }
}
