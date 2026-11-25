// SPDX-License-Identifier: MIT
package io.localai.client.p2p

import android.app.Activity
import android.app.Application
import android.os.Bundle

/**
 * Tells whether any activity of the app is on screen (started), the same
 * signal ProcessLifecycleOwner gives, without another library: the app's
 * activity handles its own configuration changes, so it is not recreated on
 * rotation and needs no debounce. [onChange] runs on the main thread.
 */
class AppVisibility(private val onChange: (foreground: Boolean) -> Unit) : Application.ActivityLifecycleCallbacks {
    private var started = 0

    val foreground: Boolean get() = started > 0

    override fun onActivityStarted(activity: Activity) {
        if (started++ == 0) onChange(true)
    }

    override fun onActivityStopped(activity: Activity) {
        if (started == 0) return
        if (--started == 0) onChange(false)
    }

    override fun onActivityCreated(activity: Activity, savedInstanceState: Bundle?) = Unit
    override fun onActivityResumed(activity: Activity) = Unit
    override fun onActivityPaused(activity: Activity) = Unit
    override fun onActivitySaveInstanceState(activity: Activity, outState: Bundle) = Unit
    override fun onActivityDestroyed(activity: Activity) = Unit
}
