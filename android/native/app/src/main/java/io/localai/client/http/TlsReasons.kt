// SPDX-License-Identifier: MIT
package io.localai.client.http

import java.io.InterruptedIOException
import java.net.ConnectException
import java.net.Socket
import java.net.SocketTimeoutException
import java.net.UnknownHostException
import java.security.cert.CertPathValidatorException
import java.security.cert.CertificateException
import java.security.cert.CertificateExpiredException
import java.security.cert.CertificateNotYetValidException
import java.security.cert.X509Certificate
import javax.net.ssl.SSLEngine
import javax.net.ssl.SSLHandshakeException
import javax.net.ssl.SSLPeerUnverifiedException
import javax.net.ssl.X509ExtendedTrustManager
import javax.net.ssl.X509TrustManager

// The reason strings are the TlsReason and UnreachableReason values of
// launcher/src/core/testClassify.ts; the renderer explains each one.
object TlsReason {
    const val EXPIRED = "expired"
    const val NOT_YET_VALID = "not-yet-valid"
    const val SELF_SIGNED = "self-signed"
    const val NAME_MISMATCH = "name-mismatch"
    const val UNTRUSTED = "untrusted"
}

object UnreachableReason {
    const val TIMEOUT = "timeout"
    const val REFUSED = "refused"
    const val DNS = "dns"
    const val NETWORK = "network"
}

/**
 * The platform trust manager refused [chain]. It carries the chain because the
 * platform's own exception often does not, and the test card needs to tell a
 * self-signed certificate from one issued by an unknown CA.
 */
class RefusedChainException(val chain: List<X509Certificate>, cause: CertificateException) :
    CertificateException(cause.message, cause)

/**
 * Delegates every decision to [delegate] (on Android, the trust manager of the
 * network security config: system and user CAs). It only adds the server's
 * chain to a refusal; it never accepts what the delegate refuses.
 */
class ChainRecordingTrustManager(private val delegate: X509TrustManager) : X509ExtendedTrustManager() {
    private val extended = delegate as? X509ExtendedTrustManager

    override fun getAcceptedIssuers(): Array<X509Certificate> = delegate.acceptedIssuers

    override fun checkClientTrusted(chain: Array<X509Certificate>, authType: String) =
        delegate.checkClientTrusted(chain, authType)

    override fun checkClientTrusted(chain: Array<X509Certificate>, authType: String, socket: Socket?) =
        extended?.checkClientTrusted(chain, authType, socket) ?: delegate.checkClientTrusted(chain, authType)

    override fun checkClientTrusted(chain: Array<X509Certificate>, authType: String, engine: SSLEngine?) =
        extended?.checkClientTrusted(chain, authType, engine) ?: delegate.checkClientTrusted(chain, authType)

    override fun checkServerTrusted(chain: Array<X509Certificate>, authType: String) =
        recording(chain) { delegate.checkServerTrusted(chain, authType) }

    override fun checkServerTrusted(chain: Array<X509Certificate>, authType: String, socket: Socket?) =
        recording(chain) {
            extended?.checkServerTrusted(chain, authType, socket) ?: delegate.checkServerTrusted(chain, authType)
        }

    override fun checkServerTrusted(chain: Array<X509Certificate>, authType: String, engine: SSLEngine?) =
        recording(chain) {
            extended?.checkServerTrusted(chain, authType, engine) ?: delegate.checkServerTrusted(chain, authType)
        }

    private inline fun recording(chain: Array<X509Certificate>, check: () -> Unit) {
        try {
            check()
        } catch (e: RefusedChainException) {
            throw e
        } catch (e: CertificateException) {
            throw RefusedChainException(chain.toList(), e)
        }
    }
}

private fun X509Certificate.isSelfSigned() = issuerX500Principal == subjectX500Principal

/** Maps a failed request to the reasons of the connection test. */
fun classifyIoError(e: Throwable): ProbeResult {
    val causes = generateSequence(e) { t -> t.cause?.takeIf { it !== t } }.take(32).toList()
    val validator = causes.filterIsInstance<CertPathValidatorException>()

    // OkHttp's host name check fails after the chain was trusted.
    if (causes.any { it is SSLPeerUnverifiedException }) return ProbeResult.Tls(TlsReason.NAME_MISMATCH)
    if (causes.any { it is CertificateExpiredException } ||
        validator.any { it.reason == CertPathValidatorException.BasicReason.EXPIRED }
    ) {
        return ProbeResult.Tls(TlsReason.EXPIRED)
    }
    if (causes.any { it is CertificateNotYetValidException } ||
        validator.any { it.reason == CertPathValidatorException.BasicReason.NOT_YET_VALID }
    ) {
        return ProbeResult.Tls(TlsReason.NOT_YET_VALID)
    }
    if (causes.any { it is SSLHandshakeException || it is CertificateException || it is CertPathValidatorException }) {
        val leaf = causes.filterIsInstance<RefusedChainException>().firstOrNull()?.chain?.firstOrNull()
            ?: validator.firstNotNullOfOrNull { it.certPath?.certificates?.firstOrNull() as? X509Certificate }
        return ProbeResult.Tls(if (leaf?.isSelfSigned() == true) TlsReason.SELF_SIGNED else TlsReason.UNTRUSTED)
    }

    for (t in causes) {
        when (t) {
            is UnknownHostException -> return ProbeResult.Unreachable(UnreachableReason.DNS)
            // Android reports more than a refusal as ConnectException; the errno is in the message.
            is ConnectException -> return ProbeResult.Unreachable(connectReason(t.message.orEmpty()))
            is SocketTimeoutException -> return ProbeResult.Unreachable(UnreachableReason.TIMEOUT)
            // OkHttp's call timeout.
            is InterruptedIOException ->
                if (t.message == "timeout") return ProbeResult.Unreachable(UnreachableReason.TIMEOUT)
        }
    }
    return ProbeResult.Unreachable(UnreachableReason.NETWORK)
}

private fun connectReason(message: String): String = when {
    message.contains("ETIMEDOUT") || message.contains("timed out", ignoreCase = true) -> UnreachableReason.TIMEOUT
    message.contains("ENETUNREACH") || message.contains("EHOSTUNREACH") ||
        message.contains("unreachable", ignoreCase = true) -> UnreachableReason.NETWORK
    else -> UnreachableReason.REFUSED
}
