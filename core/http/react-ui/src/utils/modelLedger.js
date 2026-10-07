// Numbers behind the Models ledger: how a model fits this machine, and how
// much disk the models volume has left. Pure functions, so the rules sit in one
// place and can be read without rendering anything.

const GB = 1024 * 1024 * 1024

// A model "fits" when it needs no more than this share of the memory the page
// measures against. The same limit the VRAM chart draws, so a row and the chart
// never disagree.
export const FIT_LIMIT = 0.95

// The models disk turns amber when less than a tenth of it is free, or when
// less than 20 GB is free. Either one alone is enough: 10 percent of a 4 TB
// disk is still 400 GB, which is not a warning, and 20 GB free on a 100 GB
// disk is the same warning at a different size.
export const LOW_DISK_FRACTION = 0.1
export const LOW_DISK_BYTES = 20 * GB

function positive(value) {
  return typeof value === 'number' && Number.isFinite(value) && value > 0 ? value : 0
}

// How one model sits in the memory this page measures against.
//
//   budget        what modelBudget() returns: the memory a model may occupy.
//   ramAvailable  system RAM that could take the layers that do not fit on the
//                 GPU. Null when unknown (a cluster reading has no RAM figure),
//                 which turns a spill into a plain "over".
//
// Returns null while the estimate or the budget is missing: a row with no
// reading must not claim to fit or not to fit.
//
//   fits   needs no more than the limit. amount = room left.
//   spill  too big for the GPU, but the rest would run from RAM. amount = the
//          part that runs on the CPU.
//   over   too big for the GPU and RAM together, or for RAM on a host with no
//          GPU. amount = how far over.
export function fitFor(vramBytes, budget, ramAvailable = null) {
  const need = positive(vramBytes)
  const total = positive(budget?.totalMemory)
  if (!need || !total) return null
  const limit = total * FIT_LIMIT
  const fraction = Math.min(1, need / total)
  if (need <= limit) {
    return { state: 'fits', amount: limit - need, need, limit, total, fraction }
  }
  const overflow = need - limit
  const ram = budget.hasGpu && budget.scope !== 'cluster' ? positive(ramAvailable) : 0
  if (ram > 0 && overflow <= ram) {
    return { state: 'spill', amount: overflow, need, limit, total, fraction }
  }
  return { state: 'over', amount: overflow - ram, need, limit, total, fraction }
}

// The state of the models volume, or null when the page cannot say honestly.
//
// Hidden, not zeroed, when:
//  - the resources reading carries no disk block (an older server, or a failed
//    read; the server omits the block rather than report 0 bytes);
//  - the server is a cluster controller. Models live on the workers, and the
//    controller's own volume says nothing about where an install lands.
export function diskState(resources) {
  const disk = resources?.disk
  if (!disk || resources?.cluster?.enabled) return null
  const total = positive(disk.total)
  if (!total) return null
  const free = Math.max(0, Math.min(total, positive(disk.available)))
  const models = positive(resources.storage_size)
  const used = Math.max(0, total - free)
  return {
    total,
    free,
    used,
    // The models directory can sit on a bigger volume than its own contents;
    // never claim more than the disk holds.
    models: Math.min(models, used),
    other: Math.max(0, used - Math.min(models, used)),
    low: free < total * LOW_DISK_FRACTION || free < LOW_DISK_BYTES,
  }
}

// What the disk has left after an install of `sizeBytes`. Null when either
// number is unknown. Negative means the download does not fit.
export function leavesFree(disk, sizeBytes) {
  const size = positive(sizeBytes)
  if (!disk || !size) return null
  return disk.free - size
}

// "16.7" for 16.7 GB. Smaller than a tenth of a GB would read as "0.0", which
// says nothing, so it is "<0.1".
export function gbNumber(bytes) {
  const value = positive(bytes) / GB
  if (value > 0 && value < 0.1) return '<0.1'
  if (value >= 100) return String(Math.round(value))
  return value.toFixed(1)
}

export function gbLabel(bytes) {
  return `${gbNumber(bytes)} GB`
}

// CSS custom property for a bar's fill, so the width lives in the stylesheet.
export function fitStyle(fraction) {
  return { '--ledger-fit': `${Math.round(Math.max(0, Math.min(1, fraction)) * 100)}%` }
}
