// Sizes of the window layout, shared by the main process (which places the
// views) and the shell renderer (which draws the status bar into the space the
// main process leaves for it). Constants only: no Electron or Node import.

/** Height of the slim status bar at the bottom of the window, in CSS pixels. */
export const STATUS_BAR_HEIGHT = 30

/** Diameter of the status dot shown when the bar is hidden. */
export const CORNER_DOT_SIZE = 12

/**
 * Side of the square overlay that holds the dot. Larger than the dot so the
 * ring around it and a comfortable click target fit.
 */
export const CORNER_DOT_BOX = 28

/** Gap between the dot box and the right and bottom edges of the window. */
export const CORNER_DOT_MARGIN = 6

/** The window cannot be made smaller than this (content size). */
export const MIN_WINDOW_WIDTH = 720
export const MIN_WINDOW_HEIGHT = 480

/** First size of the window. */
export const DEFAULT_WINDOW_WIDTH = 1100
export const DEFAULT_WINDOW_HEIGHT = 720
