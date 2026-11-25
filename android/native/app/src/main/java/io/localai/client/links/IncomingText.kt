// SPDX-License-Identifier: MIT
package io.localai.client.links

/**
 * Picks the text the app hands to the renderer from an incoming intent.
 *
 * This only extracts text. Whether it is an invite is decided by the renderer
 * with `readInvite`, which shows a preview or "not an invite"; nothing here
 * may pair, because any app can send these intents.
 */
object IncomingText {
    /** Longer texts are dropped: no invite is this long, and the bridge should not carry it. */
    const val MAX_LENGTH = 4096

    private const val ACTION_VIEW = "android.intent.action.VIEW"
    private const val ACTION_SEND = "android.intent.action.SEND"
    private const val PAIR = "localai://pair"
    private const val INVITE_PREFIX = "localai://pair?"

    fun fromIntent(action: String?, data: String?, sharedText: String?): String? = when (action) {
        ACTION_VIEW -> data?.takeIf { it.length <= MAX_LENGTH && isPairUri(it) }
        ACTION_SEND -> sharedText?.takeIf { it.length <= MAX_LENGTH }?.let(::fromShared)
        else -> null
    }

    // The manifest admits only localai://pair, but the URI string is still
    // checked: an explicit intent reaches the activity without the filter.
    // A pair link without a query is passed on so the renderer can say it is
    // not a valid invite instead of the app doing nothing.
    private fun isPairUri(uri: String): Boolean {
        if (!uri.startsWith(PAIR)) return false
        return uri.length == PAIR.length || uri[PAIR.length] in "?/#"
    }

    // Messengers share an invite inside prose; the link ends at the first
    // whitespace. Text without a link goes over trimmed, so the renderer can
    // report that it is not an invite.
    private fun fromShared(text: String): String? {
        val start = text.indexOf(INVITE_PREFIX)
        if (start >= 0) {
            val end = (start until text.length).firstOrNull { text[it].isWhitespace() } ?: text.length
            return text.substring(start, end)
        }
        return text.trim().ifEmpty { null }
    }
}
