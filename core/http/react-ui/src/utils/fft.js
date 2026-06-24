// Minimal in-place iterative radix-2 Cooley–Tukey FFT.
//
// The AudioTransform spectrogram only needs forward transforms of short real
// frames (≤2048 samples), so a compact ~30-line implementation beats pulling
// in a dependency and shipping it in the bundle. `re` and `im` are mutated in
// place; `n = re.length` must be a power of two (the caller picks fftSize).
export function fftRadix2(re, im) {
  const n = re.length
  if (n <= 1) return

  // Bit-reversal permutation: reorder samples so the butterfly stage below can
  // run in place.
  for (let i = 1, j = 0; i < n; i++) {
    let bit = n >> 1
    for (; j & bit; bit >>= 1) j ^= bit
    j ^= bit
    if (i < j) {
      const tr = re[i]; re[i] = re[j]; re[j] = tr
      const ti = im[i]; im[i] = im[j]; im[j] = ti
    }
  }

  // Butterflies, doubling the transform length each pass.
  for (let len = 2; len <= n; len <<= 1) {
    const half = len >> 1
    const ang = (-2 * Math.PI) / len
    const wpr = Math.cos(ang)
    const wpi = Math.sin(ang)
    for (let i = 0; i < n; i += len) {
      let wr = 1
      let wi = 0
      for (let k = 0; k < half; k++) {
        const a = i + k
        const b = a + half
        const tr = wr * re[b] - wi * im[b]
        const ti = wr * im[b] + wi * re[b]
        re[b] = re[a] - tr
        im[b] = im[a] - ti
        re[a] += tr
        im[a] += ti
        const nwr = wr * wpr - wi * wpi
        wi = wr * wpi + wi * wpr
        wr = nwr
      }
    }
  }
}
