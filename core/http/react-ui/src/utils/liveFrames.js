import { createFrameSlots, MAX_LIVE_FRAMES } from './pageTiles.js'

// The live page frames of the whole tab: the tiles on Home, the tiles in the
// slots of ui.json, the page area of Home and the tab in front of the dock
// beside a chat share one budget of MAX_LIVE_FRAMES.
// A tile that gets no slot shows as a static card that runs on a click (take),
// which stops the frame that started first.
export const liveFrames = createFrameSlots(MAX_LIVE_FRAMES)
