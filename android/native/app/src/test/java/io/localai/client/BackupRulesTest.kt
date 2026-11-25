// SPDX-License-Identifier: MIT
package io.localai.client

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.File

/**
 * What must stay on the device: sign-ins (WebView data), the WebUI profile
 * names and the "notification permission asked" flag (a restored flag would
 * stop a new phone from ever asking). Both rule files, every section.
 */
class BackupRulesTest {
    private val excludes = Regex("""<exclude\s+domain="([a-z]+)"\s+path="([^"]+)"\s*/>""")
    private val wanted = setOf(
        "root" to "app_webview/",
        "sharedpref" to "webui_profiles.xml",
        "sharedpref" to "p2p_plugin.xml",
    )

    private fun xml(name: String) = File("src/main/res/xml/$name").readText()

    private fun excludesOf(text: String) = excludes.findAll(text).map { it.groupValues[1] to it.groupValues[2] }.toSet()

    @Test
    fun fullBackupContentExcludesTheDeviceOnlyData() {
        assertEquals(wanted, excludesOf(xml("backup_rules.xml")))
    }

    @Test
    fun cloudBackupAndDeviceTransferExcludeTheDeviceOnlyData() {
        val text = xml("data_extraction_rules.xml")
        for (section in listOf("cloud-backup", "device-transfer")) {
            val body = Regex("<$section>(.*?)</$section>", RegexOption.DOT_MATCHES_ALL).find(text)?.groupValues?.get(1)
            assertTrue("$section is missing", body != null)
            assertEquals(section, wanted, excludesOf(body!!))
        }
    }
}
