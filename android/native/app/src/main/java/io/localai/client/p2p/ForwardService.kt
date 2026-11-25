// SPDX-License-Identifier: MIT
package io.localai.client.p2p

import android.annotation.SuppressLint
import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.Build
import android.os.Handler
import android.os.IBinder
import android.os.Looper
import android.system.Os
import android.util.Log
import androidx.core.app.NotificationCompat
import androidx.core.content.ContextCompat
import io.localai.client.R
import java.io.File
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.CopyOnWriteArrayList

/**
 * Process-wide P2P state shared by the plugin (which lives with the
 * activity) and the foreground service (which owns the forwarders). Fields
 * without a concurrent type are touched on the main thread only.
 */
@SuppressLint("StaticFieldLeak") // holds the application context only
internal object P2PRuntime {
    private const val TAG = "P2P"
    private val main = Handler(Looper.getMainLooper())
    // The application context, which lives as long as the process.
    @SuppressLint("StaticFieldLeak")
    private lateinit var app: Context

    val engine: P2PEngine = GoP2PEngine()
    lateinit var identityPath: String
        private set

    var forwards: List<Forward> = emptyList()
        private set
    // Set in the service's onCreate and cleared in its onDestroy.
    @SuppressLint("StaticFieldLeak")
    private var service: ForwardService? = null
    private var startRequested = false
    private var userStopped = false
    private val states = ConcurrentHashMap<String, String>()
    private val listeners = CopyOnWriteArrayList<(String, String) -> Unit>()

    @Synchronized
    fun init(context: Context) {
        if (::app.isInitialized) return
        app = context.applicationContext
        identityPath = identityFile(app).path
    }

    /** noBackupFilesDir/p2p/identity.key: never backed up, so a restored phone pairs again. */
    private fun identityFile(ctx: Context): File {
        val dir = File(ctx.noBackupFilesDir, "p2p")
        dir.mkdirs()
        try {
            Os.chmod(dir.path, "700".toInt(8))
        } catch (e: Exception) {
            Log.w(TAG, "cannot restrict the identity folder", e)
        }
        return File(dir, "identity.key")
    }

    fun addListener(l: (String, String) -> Unit) = listeners.add(l)
    fun removeListener(l: (String, String) -> Unit) = listeners.remove(l)

    fun lastStates(): Map<String, String> = HashMap(states)

    /** A state line from the supervisor, on any thread. */
    fun onLine(id: String, line: String) {
        states[id] = line
        for (l in listeners) l(id, line)
    }

    fun setForwards(list: List<Forward>) = main.post {
        forwards = list
        userStopped = false
        val ids = list.map { it.id }.toSet()
        states.keys.retainAll(ids)
        service?.applyForwards(list)
        evaluate()
    }

    fun attach(s: ForwardService) {
        service = s
        startRequested = false
    }

    fun detach(s: ForwardService) {
        if (service === s) service = null
    }

    /** The notification's Disconnect action: stop until the app hands over a list again. */
    fun disconnect() {
        userStopped = true
        stop()
    }

    private fun evaluate() {
        if (forwards.isEmpty()) {
            if (service != null || startRequested) stop()
        } else if (service == null && !startRequested && !userStopped) {
            start()
        }
    }

    private fun start() {
        try {
            ContextCompat.startForegroundService(app, Intent(app, ForwardService::class.java))
            startRequested = true
        } catch (e: IllegalStateException) {
            // Android 12+ refuses a foreground service started from the background.
            Log.w(TAG, "cannot start the P2P service now", e)
        }
    }

    private fun stop() {
        startRequested = false
        app.stopService(Intent(app, ForwardService::class.java))
    }
}

/**
 * The foreground service (type specialUse) that owns the forwarders, so the
 * loopback ports keep working while the user is in another app.
 */
class ForwardService : Service() {
    companion object {
        const val ACTION_DISCONNECT = "io.localai.client.p2p.DISCONNECT"
        private const val CHANNEL = "p2p"
        private const val NOTIFICATION_ID = 41
        private const val TAG = "P2P"
    }

    private lateinit var scheduler: ExecutorScheduler
    private lateinit var supervisor: ForwarderSupervisor

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onCreate() {
        super.onCreate()
        P2PRuntime.init(this)
        scheduler = ExecutorScheduler("p2p-forwarders")
        supervisor = ForwarderSupervisor(P2PRuntime.engine, P2PRuntime.identityPath, scheduler, P2PRuntime::onLine)
        P2PRuntime.attach(this)
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_DISCONNECT) {
            P2PRuntime.disconnect()
            stopSelf()
            return START_NOT_STICKY
        }
        val list = P2PRuntime.forwards
        // Every startForegroundService call must be answered with startForeground.
        if (!goForeground(list) || list.isEmpty()) {
            stopSelf()
            return START_NOT_STICKY
        }
        supervisor.setForwards(list)
        // Not sticky: a restart by the system would be a start from the background.
        return START_NOT_STICKY
    }

    fun applyForwards(list: List<Forward>) {
        supervisor.setForwards(list)
        if (list.isNotEmpty()) getSystemService(NotificationManager::class.java).notify(NOTIFICATION_ID, notification(list))
    }

    fun restartAll() = supervisor.restartAll()

    override fun onDestroy() {
        P2PRuntime.detach(this)
        supervisor.stopAll()
        scheduler.shutdown()
        super.onDestroy()
    }

    private fun goForeground(list: List<Forward>): Boolean = try {
        val n = notification(list)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) {
            startForeground(NOTIFICATION_ID, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE)
        } else {
            startForeground(NOTIFICATION_ID, n)
        }
        true
    } catch (e: Exception) {
        Log.w(TAG, "cannot run the P2P service in the foreground", e)
        false
    }

    private fun notification(list: List<Forward>): Notification {
        val nm = getSystemService(NotificationManager::class.java)
        if (nm.getNotificationChannel(CHANNEL) == null) {
            val ch = NotificationChannel(CHANNEL, getString(R.string.p2p_channel_name), NotificationManager.IMPORTANCE_LOW)
            ch.description = getString(R.string.p2p_channel_description)
            ch.setShowBadge(false)
            nm.createNotificationChannel(ch)
        }
        val one = list.singleOrNull()?.name?.takeIf { it.isNotBlank() }
        val text = if (one != null) {
            getString(R.string.p2p_notification_one, one)
        } else {
            resources.getQuantityString(R.plurals.p2p_notification_many, list.size, list.size)
        }
        val flags = PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT
        val open = packageManager.getLaunchIntentForPackage(packageName)?.let { PendingIntent.getActivity(this, 0, it, flags) }
        val disconnect = PendingIntent.getService(this, 1, Intent(this, ForwardService::class.java).setAction(ACTION_DISCONNECT), flags)
        return NotificationCompat.Builder(this, CHANNEL)
            .setSmallIcon(R.drawable.ic_stat_p2p)
            .setContentTitle(getString(R.string.p2p_notification_title))
            .setContentText(text)
            .setOngoing(true)
            .setSilent(true)
            .setOnlyAlertOnce(true)
            .setShowWhen(false)
            .setCategory(NotificationCompat.CATEGORY_SERVICE)
            .setForegroundServiceBehavior(NotificationCompat.FOREGROUND_SERVICE_IMMEDIATE)
            .setContentIntent(open)
            .addAction(0, getString(R.string.p2p_disconnect), disconnect)
            .build()
    }
}
