// SPDX-License-Identifier: MIT
package io.localai.client.webui

/**
 * The bottom edge of the WebUI view. All sizes are in px.
 *
 * The activity uses adjustResize, and Capacitor's SystemBars plugin pads the
 * activity's root view by the keyboard height while the keyboard is open. The
 * container of the WebUI view then already ends at the keyboard, but the window
 * insets still report the full keyboard height. Only the part of the keyboard
 * that still covers the container may become a margin; a second margin of the
 * full height leaves an empty band above the keyboard and a very short WebUI.
 */
internal object WebuiLayout {
    /**
     * The bottom margin of the view in its container.
     *
     * @param imeBottom the keyboard height from the bottom of the window, 0 when it is closed.
     * @param clearedBelow the part of the window below the container that is already kept clear.
     * @param shellBottom the shell's bottom strip.
     * @param barHeight the shell's status bar; the shell hides it while the keyboard is open.
     * @param navBottom the navigation bar inset the container does not clear yet.
     */
    fun bottomMargin(imeBottom: Int, clearedBelow: Int, shellBottom: Int, barHeight: Int, navBottom: Int): Int =
        if (imeBottom > 0) {
            (imeBottom - clearedBelow.coerceAtLeast(0)).coerceAtLeast(0)
        } else {
            shellBottom + barHeight + navBottom
        }
}
