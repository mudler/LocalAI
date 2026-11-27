// SPDX-License-Identifier: MIT
package io.localai.client.p2p

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.io.File
import java.security.GeneralSecurityException

/** Stands in for the Keystore cipher: a marker byte and the bytes reversed. */
private class FakeCipher : CredentialCipher {
    var broken = false

    override fun encrypt(plain: ByteArray): ByteArray = byteArrayOf(MARK) + plain.reversedArray()

    override fun decrypt(blob: ByteArray): ByteArray {
        if (broken || blob.isEmpty() || blob[0] != MARK) throw GeneralSecurityException("cannot decrypt")
        return blob.copyOfRange(1, blob.size).reversedArray()
    }

    private companion object {
        const val MARK: Byte = 0x7f
    }
}

class DeviceCredentialsTest {
    @get:Rule
    val tmp = TemporaryFolder()

    private val peerA = "12D3KooWTestPeerA"
    private val peerB = "12D3KooWTestPeerB"

    private fun store(cipher: CredentialCipher = FakeCipher()): Pair<DeviceCredentials, File> {
        val dir = File(tmp.root, "credentials")
        return DeviceCredentials(dir, cipher) to dir
    }

    @Test
    fun storesAndLoadsByPeer() {
        val (s, _) = store()
        s.save(peerA, "credential-a")
        s.save(peerB, "credential-b")
        assertEquals("credential-a", s.load(peerA))
        assertEquals("credential-b", s.load(peerB))
        assertTrue(s.has(peerA))
        assertNull(s.load("12D3KooWUnknown"))
        assertFalse(s.has("12D3KooWUnknown"))
    }

    @Test
    fun savingAgainReplacesTheCredential() {
        val (s, _) = store()
        s.save(peerA, "old")
        s.save(peerA, "new")
        assertEquals("new", s.load(peerA))
    }

    @Test
    fun filesAreEncryptedAndNamedByAHashOfThePeer() {
        val (s, dir) = store()
        s.save(peerA, "credential-a")
        val files = dir.listFiles()!!.toList()
        assertEquals(listOf(DeviceCredentials.nameOf(peerA)), files.map { it.name })
        assertFalse(files[0].name.contains(peerA))
        assertFalse(String(files[0].readBytes(), Charsets.UTF_8).contains("credential-a"))
    }

    @Test
    fun deleteRemovesOnlyThatPeer() {
        val (s, _) = store()
        s.save(peerA, "credential-a")
        s.save(peerB, "credential-b")
        s.delete(peerA)
        assertNull(s.load(peerA))
        assertFalse(s.takeFresh(peerA))
        assertEquals("credential-b", s.load(peerB))
    }

    @Test
    fun retainKeepsOnlyTheSavedServers() {
        val (s, dir) = store()
        s.save(peerA, "credential-a")
        s.save(peerB, "credential-b")
        s.retain(listOf(peerB, "12D3KooWNoCredential"))
        assertNull(s.load(peerA))
        assertEquals("credential-b", s.load(peerB))
        assertEquals(1, dir.listFiles()!!.size)
        s.retain(emptyList())
        assertNull(s.load(peerB))
    }

    @Test
    fun aSavedPeerIsFreshOnce() {
        val (s, _) = store()
        assertFalse(s.takeFresh(peerA))
        s.save(peerA, "credential-a")
        assertTrue(s.takeFresh(peerA))
        assertFalse(s.takeFresh(peerA))
        // The credential stays for the sign-in page.
        assertEquals("credential-a", s.load(peerA))
    }

    @Test
    fun aSignOutIsKeptAcrossARestart() {
        val (s, dir) = store()
        s.save(peerA, "credential-a")
        s.save(peerB, "credential-b")
        assertFalse(s.signedOut(peerA))
        s.signOut(peerA)
        assertTrue(s.signedOut(peerA))
        assertFalse(s.signedOut(peerB))
        // The credential stays: a sign-out stops the automatic sign-in only.
        assertEquals("credential-a", s.load(peerA))
        // A cold start of the app: a new store on the same folder.
        val again = DeviceCredentials(dir, FakeCipher())
        assertTrue(again.signedOut(peerA))
        assertFalse(again.signedOut(peerB))
    }

    @Test
    fun aSignOutEndsTheFirstOpenPost() {
        val (s, _) = store()
        s.save(peerA, "credential-a")
        s.signOut(peerA)
        assertFalse(s.takeFresh(peerA))
    }

    @Test
    fun anOpenFromTheListTurnsTheSignInOnAgain() {
        val (s, dir) = store()
        s.save(peerA, "credential-a")
        assertFalse(s.allowAutoSignIn(peerA))
        s.signOut(peerA)
        assertTrue(s.allowAutoSignIn(peerA))
        assertFalse(s.signedOut(peerA))
        assertFalse(DeviceCredentials(dir, FakeCipher()).signedOut(peerA))
        assertEquals(listOf(DeviceCredentials.nameOf(peerA)), dir.listFiles()!!.map { it.name })
    }

    @Test
    fun aNewPairingTurnsTheSignInOnAgain() {
        val (s, _) = store()
        s.save(peerA, "credential-a")
        s.signOut(peerA)
        s.save(peerA, "credential-a2")
        assertFalse(s.signedOut(peerA))
        assertTrue(s.takeFresh(peerA))
    }

    @Test
    fun removingAServerDeletesItsSignOutToo() {
        val (s, dir) = store()
        s.save(peerA, "credential-a")
        s.save(peerB, "credential-b")
        s.signOut(peerA)
        s.signOut(peerB)
        s.delete(peerA)
        assertFalse(s.signedOut(peerA))
        // retain keeps the flag of a saved server and drops the others.
        s.retain(listOf(peerB))
        assertTrue(s.signedOut(peerB))
        assertEquals(
            setOf(DeviceCredentials.nameOf(peerB), DeviceCredentials.nameOf(peerB) + DeviceCredentials.OFF_SUFFIX),
            dir.listFiles()!!.map { it.name }.toSet(),
        )
        s.retain(emptyList())
        assertFalse(s.signedOut(peerB))
        assertEquals(0, dir.listFiles()!!.size)
    }

    @Test
    fun aSignOutHoldsInMemoryWhenTheFolderCannotBeWritten() {
        // The folder path is a file: the flag file cannot be made.
        val blocker = File(tmp.root, "blocked").apply { writeText("x") }
        val s = DeviceCredentials(blocker, FakeCipher())
        s.signOut(peerA)
        assertTrue(s.signedOut(peerA))
        assertTrue(s.allowAutoSignIn(peerA))
        assertFalse(s.signedOut(peerA))
    }

    @Test
    fun aCredentialThatDoesNotDecryptIsRemoved() {
        val cipher = FakeCipher()
        val (s, dir) = store(cipher)
        s.save(peerA, "credential-a")
        cipher.broken = true
        assertNull(s.load(peerA))
        assertEquals(0, dir.listFiles()!!.size)
    }

    @Test(expected = IllegalArgumentException::class)
    fun anEmptyCredentialIsRefused() {
        store().first.save(peerA, "")
    }

    @Test
    fun theCredentialIsTakenOutOfThePairingResult() {
        val result = """{"device_id":"d1","kind":"device","phrase":"apple banana","server":{"peer_id":"$peerA","name":"Studio"},""" +
            """"device_credential":"TEST-ONLY-NOT-A-SECRET","sign_in":true,"owner_name":"Ada"}"""
        val split = splitCredential(result)
        assertEquals("TEST-ONLY-NOT-A-SECRET", split.credential)
        assertEquals(peerA, split.peer)
        assertFalse(split.json.contains(CREDENTIAL_KEY))
        assertFalse(split.json.contains("TEST-ONLY-NOT-A-SECRET"))
        val o = JSONObject(split.json)
        assertEquals("apple banana", o.getString("phrase"))
        assertTrue(o.getBoolean("sign_in"))
        assertEquals("Ada", o.getString("owner_name"))
    }

    @Test
    fun anOldServerAnswerIsUnchanged() {
        val result = """{"device_id":"d1","kind":"device","phrase":"apple banana","server":{"peer_id":"$peerA","name":"Studio"}}"""
        val split = splitCredential(result)
        assertNull(split.credential)
        assertEquals(peerA, split.peer)
        assertEquals(JSONObject(result).toString(), split.json)
    }

    @Test
    fun aCredentialOfTheWrongTypeIsStillRemoved() {
        for (value in listOf("\"\"", "null", "42", "{\"x\":1}")) {
            val split = splitCredential("""{"phrase":"p","server":{"peer_id":"$peerA"},"device_credential":$value}""")
            assertNull(value, split.credential)
            assertFalse(value, JSONObject(split.json).has(CREDENTIAL_KEY))
        }
    }
}
