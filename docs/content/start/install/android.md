---
title: "Android app"
description: "Install the LocalAI app for Android to use a LocalAI server from your phone, by address or over P2P with an invite link."
icon: phone_android
weight: 46
keywords: ["android", "apk", "phone", "mobile", "client", "p2p", "invite", "qr code", "sideload", "install unknown apps", "update"]
---

The LocalAI app for Android shows the web interface of a LocalAI server on your phone. It is a client only. Nothing runs on the phone: the server runs on another computer, at home, at work or in the cloud. The app keeps a list of servers, tests each one before it saves it, and opens its web interface in a locked view with a thin status bar. It reaches a server by its address, or over P2P with an invite link, also when the server is behind a home router.

To run a LocalAI server, use one of the server install methods in the [install guide]({{% relref "/start/install" %}}) or the [desktop app]({{% relref "/start/install/desktop" %}}).

The app needs Android 8.0 (API level 26) or later on a 64-bit ARM device (`arm64-v8a`). The server page needs Android System WebView (or Chrome) version 111 or later.

## Install the app

The app is not in an app store. You download the APK file from the LocalAI releases and install it yourself ("sideloading").

1. When the app is released, open the [latest release](https://github.com/mudler/LocalAI/releases/latest) in the browser on your phone.
2. Download `LocalAI-Android.apk`.
3. Open the downloaded file. Android asks if the browser may install unknown apps.
4. Select **Settings**, turn on **Allow from this source**, and go back.
5. Select **Install**.

Android asks for the consent in step 4 only once for each browser. The app cannot skip or change this step.

The release also has `LocalAI-Android.apk.sha256`. To check the download on a computer before you copy the file to the phone, run this command in the folder with both files:

```bash
sha256sum --check LocalAI-Android.apk.sha256
```

The command must print `OK`. If it does not, delete the files and download them again.

You can also install the file from a computer with `adb install LocalAI-Android.apk`.

### Android developer verification

Google is rolling out Android developer verification. On a certified Android device, an app must come from a registered developer. LocalAI publishes the APK only after the app and its signing key are registered.

## Add a server

On the first start, the app shows **Connect to a LocalAI server**. Add a server in one of these ways. To add more servers later, open **Settings > Servers**. The app keeps up to 20 servers. One of them is the default, and the app opens it at the next start.

### By address

1. Select **Add by address**.
2. Type the address of the server, for example `https://gpu.example.com`. Include the port if it is not the default, for example `http://192.168.1.20:8080`.
3. Select **Test**. The app checks the address, the certificate, and that the server is LocalAI.
4. Select **Connect**.

A server that asks you to sign in counts as reachable. If the address is plain `http`, the app shows **Not encrypted**. The app does not follow a redirect to another site.

### By invite link (P2P)

An invite link starts with `localai://pair`. The server owner makes it in the web interface of the server: open **Cluster**, then **Sharing**, then **Share this instance**. The server must have P2P sharing turned on. See [P2P]({{% relref "/administration/p2p" %}}). The app accepts a **device** invite and a **remote** invite. A device invite works once and for a short time.

You can give the invite to the app in four ways:

- **Paste it.** Select **Paste an invite link** and paste the link.
- **Scan the QR code.** Select **Scan an invite QR code** and point the camera at the code on the server's **Share this instance** dialog. The app asks for the camera permission the first time.
- **Open the link.** Select a `localai://pair` link in another app, for example in a message. The link opens the LocalAI app.
- **Share it.** Share the text of the invite from another app, and choose **LocalAI** in the share menu.

In every case, the app first shows a preview of the invite: the server name, the kind of invite, the routes and the time left. The app never pairs before you select **Pair**. If the text is not an invite, the app shows **This is not a LocalAI invite link** and does nothing else.

When the app pairs, it shows a four-word **confirmation phrase**. The server shows the same words. Make sure that the words match. If they do not match, someone else used the invite: tell the server owner.

After pairing, the app reaches the server at a local address on the phone, for example `http://127.0.0.2:41001`. Each P2P server has its own local address, so each server keeps its own sign-in. The server list shows **P2P** and how the app reaches the server: direct, hole punched, or through a relay.

While the app is connected to a P2P server, it asks the server for its current routes (its addresses and relays) when the connection opens and every 10 minutes after, and saves them. So the app still finds the server after the server's relays or addresses change.

## Sign in

The app does not save passwords or API keys. If the server asks you to sign in, you sign in on the server's own page, in the app. The app keeps the sign-in of each server separate from the others, and keeps it after a restart. Android does not copy the sign-ins to a backup or to a new phone.

These sign-in methods work:

- User name and password.
- API key.
- GitHub or OIDC sign-in. The sign-in page of the identity provider opens in the app, and the app keeps the session. While you sign in, the app shows the pages of the identity provider that the server's sign-in redirects to, for at most 10 minutes. When you are back on the server, other addresses open in your browser again.

### Sign in with the invite

A P2P invite can also sign you in. When the server gives a sign-in invite, the server answers the pairing with a device credential. The app keeps the credential in its private storage, encrypted with a key of the Android Keystore, and never shows it to the web pages. The app then opens the server signed in as the person who made the invite: you do not type a password.

When the server's sign-in page shows later, for example because the session ended, the app uses the credential again, one time. If the server refuses it, for example because the device was revoked, the app shows the sign-in page and does not try again until you open the server again. Sign in by hand then, or ask for a new invite.

If you sign out in the server's page, the app stops the automatic sign-in for that server. The sign-in page shows, and the app does not use the credential, also after you close and open the app again. Sign in by hand, or open the server again from the server list: the app then signs you in with the credential again. A new pairing with the server also turns the automatic sign-in on again. When you remove a server, the app deletes its credential. A server that does not give a credential works as before: you sign in on its page.

If an identity provider page opens in your browser instead, the provider moved to another address without a redirect. The sign-in does not complete in the app then: use a password or an API key on that server. Passkeys and security keys of the identity provider do not work in the app; choose another sign-in method of the provider.

## Certificates and private CAs

The app never skips a certificate error, and has no "continue anyway" choice. If the test of a server shows **The certificate is not trusted**, the server's certificate is expired, is for another name, is self-signed, or comes from a certificate authority (CA) that the phone does not trust.

The app trusts the CAs of Android and the CAs that you install yourself. If your server uses a private CA, install the certificate of that CA on the phone:

1. Copy the CA certificate file (`.crt` or `.pem`) to the phone.
2. Open the Android **Settings** and search for **CA certificate**. On most phones, the path is **Security > Encryption & credentials > Install a certificate > CA certificate**.
3. Select the file and confirm.
4. In the app, test the server again.

The names of the settings are different on some phones. A self-signed server certificate that is not a CA certificate does not work. Make a private CA, sign the server certificate with it, and install the CA.

## P2P in the background

Android stops the network connections of an app in the background. To keep a P2P server reachable, for example while a chat answer streams and you switch to another app, the app runs a foreground service. Android then shows a notification, for example **Connected to Home GPU over P2P**, with a **Disconnect** action.

- The service runs while at least one P2P server is saved and the app is on the screen.
- When the app leaves the screen, the service runs for 10 more minutes, then stops.
- When you open the app again, the app connects again. If the connection stopped while the app was away, the app loads the server page again, but only after the connection is open.
- To keep the connection open in the background, turn on **Keep P2P connection in the background** in Settings. It is off by default.
- **Disconnect** in the notification stops the service and turns that setting off.

On Android 13 and later, the app asks for the permission to show notifications the first time you add a P2P server. If you deny it, the P2P connection still works, but Android does not show the notification.

When the phone changes network, for example from Wi-Fi to mobile data or when a VPN starts, the app restarts the P2P connections. The local address of each server stays the same, so the page and the sign-in stay too.

Other apps on the phone can connect to the local address of a P2P server, as on a computer. They still need a sign-in to the server.

While the app's own P2P connection to a server is not open, another app can take the local address of that server. For this reason the app shows the page of a P2P server, and sends it your sign-in, only while its own connection listens on that address. At other times the server shows as not reachable. If another app holds the address, the P2P connection fails with "address already in use": close that app, or restart the phone. The app tries to connect again by itself.

## Updates

The app checks for a new version once a day, when you open it. Open **Settings > Updates** to see the installed version and the channel, to check by hand with **Check now**, or to turn off **Check automatically**.

When an update is available, the app downloads it in the background. Then it checks the size, the SHA-256 checksum, and that the update has the same signing certificate as the installed app. If a check fails, the app deletes the file and installs nothing. A pill **Update to X.Y.Z** appears in the status bar. Select it to open the update sheet.

The app never installs an update without a tap from you:

1. The first time, the sheet asks you to allow updates from LocalAI. Select **Open Android settings**, turn on **Allow from this source**, and go back to the app.
2. Select **Update now**. LocalAI closes and opens again on the new version. Your servers and sign-ins stay.

What you see after **Update now** depends on the Android version:

| Android | After **Update now** |
|---|---|
| 12 and later | The update installs with no more questions, in most cases. |
| 8 to 11 | Android shows its own dialog. Select **Update** (or **Install**). |

Android can show its dialog on Android 12 and later too. For example, it does so when another installer, such as an app store, installed the app first. If the dialog does not appear, select **Update now** again.

### Development app

The development app follows the rolling [`dev-master` pre-release](https://github.com/mudler/LocalAI/releases/tag/dev-master), made from the `master` branch. It is not supported. It can have bugs and change without notice.

The development app is a separate app (`io.localai.client.dev`), with its own name (**LocalAI Dev**), servers and sign-ins. It has the same icon as the stable app, and its status bar shows a **Development** chip. It installs next to the stable app (`io.localai.client`). The two apps never update each other. The development app updates itself only from `dev-master`, and its version looks like `5.0.2-dev.42`.

To get it, open **Settings > Updates** in the stable app and select **Get the development app**. The browser downloads `LocalAI-Android-dev.apk`, and you install it as in [Install the app](#install-the-app). You can also download it from the `dev-master` release.

Each pull request and each push to `master` that changes the app also builds a debug APK of the development app. It is a GitHub Actions artifact of the **Android client** workflow (`localai-android-pr` or `localai-android-dev`). A debug APK has a different signing key. Uninstall the development app before you install a debug APK, and the reverse.

## Permissions

| Permission | Why the app needs it |
|---|---|
| Internet, network state | To reach your servers, and to restart P2P connections when the network changes. |
| Notifications (Android 13 and later) | To show the notification of the P2P service. The app asks the first time you add a P2P server. |
| Foreground service | To keep P2P connections open while you use another app. |
| Install apps, update without user action | To install its own updates after you select **Update now**. |
| Camera | To scan invite QR codes, and for server pages that use the camera. Android asks when the app first needs it. |
| Microphone, audio settings | For server pages that use the microphone, for example voice input. Android asks when the app first needs it. |

When a server page asks for the microphone or the camera, the app also asks you for each server. Select **Remember for this server** to keep the answer.

## Where your data is

The app keeps the list of servers and its settings in its private storage. Android backup includes this list. The sign-ins of the servers and the P2P identity of the phone stay on the phone: Android does not back them up or copy them to a new phone.

After you restore a backup on a new phone, sign in to each server again. P2P servers do not connect, because the new phone has a new P2P identity. Remove each P2P server and pair it again with a new invite.

To remove the app and all its data, uninstall it in the Android settings. On Android 10 and later, files that you downloaded from a server page stay in the **Downloads** folder. On Android 8 and 9, the app saves downloads in its own folder (`Android/data/<app ID>/files/Download`), and Android deletes that folder when you uninstall the app: move the files that you want to keep first.

## Troubleshooting

### The server does not answer

While the default server does not answer, the app shows a recovery screen and tries again every 30 seconds. Check these points:

- The address is correct and includes `https://` or `http://`.
- The phone uses the right network or VPN. An address such as `192.168.1.20` works only on the same network as the server.
- The server is running. Open `https://gpu.example.com/readyz` in the browser on the phone.

Then select **Retry**, or choose another server.

### A P2P server does not connect

When a P2P connection fails, the app tries again by itself, after 1 second, then after longer waits, up to 1 minute. Make sure that P2P sharing is still on at the server, and that the server owner did not revoke this device. If the device was revoked, remove the server in the app and pair again.

If the phone was away from the server for a long time, the saved routes can be out of date. Ask the server owner for a new invite and scan or paste it. The app sees that the server is already saved and shows **Update routes** instead of **Pair**. It connects with the routes of the invite and does not use the invite code. It saves new routes only when the server gives them over that connection, so an invite that someone else wrote cannot change the saved routes. Then ask the server owner to cancel the unused invite on the server: **Cluster**, then **Sharing**.

### The server page does not open

The app needs Android System WebView (or Chrome) version 111 or later. Update **Android System WebView** in the app store of your phone.

### "P2P is not available in this build of the app"

This build of the app has no P2P library. Download the APK again from the releases page.
