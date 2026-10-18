package com.github.kr328.clash.turn

import android.annotation.SuppressLint
import android.os.Bundle
import android.webkit.WebResourceRequest
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.appcompat.app.AppCompatActivity
import androidx.core.view.ViewCompat
import androidx.core.view.WindowInsetsCompat
import androidx.core.view.updatePadding
import com.github.kr328.clash.common.constants.Intents
import java.net.URI

/**
 * VK's captcha page, inside the app.
 *
 * VK sometimes answers a call-link request with a captcha, and answering it is the one step of
 * the WL setup that needs a human. The client serves that page itself: it rewrites VK's page so
 * that every request goes through its own loopback server, which is also where it reads the
 * answer from. So all this screen does is show a URL on `127.0.0.1` — the page tells the client
 * when it is done, and the client carries on.
 *
 * The URL comes from the status the core wrote, never from outside, and anything that is not
 * loopback is refused: this WebView exists for the one page the client serves.
 */
class TurnCaptchaActivity : AppCompatActivity() {
    @SuppressLint("SetJavaScriptEnabled")
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        val url = intent?.getStringExtra(EXTRA_URL)

        if (url == null || !isLoopback(url)) {
            finish()

            return
        }

        val webView = WebView(this).apply {
            settings.javaScriptEnabled = true
            settings.domStorageEnabled = true

            webViewClient = object : WebViewClient() {
                override fun shouldOverrideUrlLoading(view: WebView, request: WebResourceRequest): Boolean {
                    // The page rewrites its own links to its loopback server; a navigation that
                    // leaves loopback is not the page we came to show.
                    return !isLoopback(request.url.toString())
                }
            }

            loadUrl(url)
        }

        // targetSdk 35 draws edge to edge: without this the captcha page's own header would sit
        // under the status bar, and it is the part with the image and the input field.
        ViewCompat.setOnApplyWindowInsetsListener(webView) { view, insets ->
            val bars = insets.getInsets(
                WindowInsetsCompat.Type.systemBars() or WindowInsetsCompat.Type.displayCutout()
            )

            view.updatePadding(bars.left, bars.top, bars.right, bars.bottom)

            insets
        }

        setContentView(webView)
    }

    /** The client's captcha page is only ever served on loopback. */
    private fun isLoopback(url: String): Boolean = runCatching {
        val host = URI(url).host ?: return false

        host == "127.0.0.1" || host == "::1" || host.equals("localhost", ignoreCase = true)
    }.getOrDefault(false)

    companion object {
        /** The page to show; posted by the service's WL notification too ([Intents]). */
        const val EXTRA_URL = Intents.EXTRA_TURN_CAPTCHA_URL
    }
}
