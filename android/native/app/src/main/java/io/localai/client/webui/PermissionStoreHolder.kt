// SPDX-License-Identifier: MIT
package io.localai.client.webui

import org.json.JSONException
import org.json.JSONObject

/**
 * The remembered WebUI permission choices, as the client core sends them: the
 * JSON of the TypeScript PermissionStore (origin to { "media": "allow" | "deny" }).
 * Read like parsePermissionStore: anything malformed is dropped, never trusted,
 * and text that is not a JSON object empties the store.
 *
 * Set from the plugin thread, read from WebView callbacks on the UI thread.
 */
class PermissionStoreHolder {
    @Volatile
    private var store: Map<String, Map<String, String>> = emptyMap()

    fun set(json: String) {
        store = parse(json)
    }

    /** The remembered media choice of `origin`, "allow" or "deny", or null. */
    fun recall(origin: String): String? = store[origin]?.get(MEDIA)

    fun asMap(): Map<String, Map<String, String>> = store

    private fun parse(json: String): Map<String, Map<String, String>> {
        val root = try {
            JSONObject(json)
        } catch (_: JSONException) {
            return emptyMap()
        }
        val out = LinkedHashMap<String, Map<String, String>>()
        for (origin in root.keys()) {
            if (WebuiPolicy.httpOrigin(origin) != origin) continue
            val choices = root.optJSONObject(origin) ?: continue
            val media = choices.opt(MEDIA)
            if (media == ALLOW || media == DENY) out[origin] = mapOf(MEDIA to media as String)
        }
        return out
    }

    private companion object {
        const val MEDIA = "media"
        const val ALLOW = "allow"
        const val DENY = "deny"
    }
}
