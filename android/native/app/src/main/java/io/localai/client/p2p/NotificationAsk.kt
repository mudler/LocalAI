// SPDX-License-Identifier: MIT
package io.localai.client.p2p

/** What to do when a P2P server is added and the service notification needs POST_NOTIFICATIONS. */
enum class NotificationAsk { GRANTED, ASK, DENIED }

/** Android 13 (API 33) made POST_NOTIFICATIONS a runtime permission. */
const val NOTIFICATIONS_RUNTIME_SDK = 33

/**
 * The system dialog comes at most once per install: the first time a P2P
 * server is added. After that the app only reports the state; the person
 * can still turn notifications on in the system settings.
 */
fun notificationAsk(sdkInt: Int, granted: Boolean, askedBefore: Boolean): NotificationAsk = when {
    sdkInt < NOTIFICATIONS_RUNTIME_SDK || granted -> NotificationAsk.GRANTED
    askedBefore -> NotificationAsk.DENIED
    else -> NotificationAsk.ASK
}
