// freezeClock stops the page clock that page.clock.install() left running. From
// here only page.clock.fastForward moves it, so a timer of the page (a wait, a
// debounce) cannot fire because the machine is slow: the test decides when.
// pauseAt refuses a time that has already passed, and a slow machine can pass
// it while the call is on its way, so it asks again with a wider margin.
export async function freezeClock(page) {
  for (let margin = 250; ; margin *= 2) {
    try {
      await page.clock.pauseAt((await page.evaluate(() => Date.now())) + margin)
      return
    } catch (err) {
      if (margin > 1000 || !/past/.test(String(err.message))) throw err
    }
  }
}
