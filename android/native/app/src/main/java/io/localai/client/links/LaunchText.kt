// SPDX-License-Identifier: MIT
package io.localai.client.links

/**
 * Delivers the launch text exactly once.
 *
 * Capacitor passes the launch intent to every plugin's handleOnNewIntent
 * when the bridge starts. The renderer reads that text with takeInitial, so
 * the same intent must not also become a `link` event, or the invite form
 * opens twice. [K] is the intent; it is compared by identity, so opening the
 * same link again later is still a new event.
 */
class LaunchText<K : Any> {
    private var launchKey: K? = null
    private var pending: String? = null

    /** Records the launch intent and its text (null when it carries none). */
    @Synchronized
    fun start(key: K, text: String?) {
        launchKey = key
        pending = text
    }

    /** The launch text, once. */
    @Synchronized
    fun take(): String? = pending.also { pending = null }

    /** The text to emit as a `link` event for [key], or null. */
    @Synchronized
    fun onNew(key: K, text: String?): String? {
        if (key === launchKey) {
            launchKey = null
            return null
        }
        return text
    }
}
