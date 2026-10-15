package com.github.kr328.clash.turn

import com.github.kr328.clash.BaseActivity
import com.github.kr328.clash.common.util.intent
import com.github.kr328.clash.common.util.ticker
import com.github.kr328.clash.core.model.TurnStatus
import com.github.kr328.clash.design.TurnDesign
import com.github.kr328.clash.service.util.importedDir
import com.github.kr328.clash.util.withProfile
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.isActive
import kotlinx.coroutines.selects.select
import kotlinx.coroutines.withContext
import java.io.File

/**
 * The WL screen: what the VK TURN clients of the active profile are doing.
 *
 * The clients run in the service process, which keeps [TurnStatus.FILE_NAME] next to the
 * profile; this screen is in the app process and reads that file. There is nothing here to
 * start or stop — a client lives exactly as long as the tunnel it feeds — so the screen polls
 * the file and offers the one action a human can be needed for: answering VK's captcha.
 */
class TurnActivity : BaseActivity<TurnDesign>() {
    override suspend fun main() {
        val design = TurnDesign(this)

        setContentDesign(design)

        // 1.5s: the clients change state on their own schedule (a stream reconnects, a captcha
        // appears), and both the file and the diff are small.
        val ticker = ticker(1500)

        // The core writes the file whenever something changed, so its timestamp is the cheap
        // "anything new?" — re-patching identical content would rebuild every card and throw
        // away the reader's scroll position.
        var lastModified = -1L

        suspend fun refresh() {
            val snapshot = withContext(Dispatchers.IO) {
                val dir = activeProfileDir()

                val file = dir?.let { File(it, TurnStatus.FILE_NAME) }
                val modified = if (file != null && file.isFile) file.lastModified() else 0L

                if (modified == lastModified) return@withContext null

                val status = if (modified == 0L) null else TurnStatus.readFrom(dir!!)

                modified to status
            } ?: return

            lastModified = snapshot.first

            design.patch(snapshot.second)
        }

        refresh()

        while (isActive) {
            select<Unit> {
                events.onReceive {
                    when (it) {
                        // A profile change swaps the whole set of clients; start and stop move
                        // them between states. Either way what is on screen is stale.
                        Event.ActivityStart,
                        Event.ClashStart,
                        Event.ClashStop,
                        Event.ProfileChanged,
                        Event.ProfileLoaded -> launch {
                            lastModified = -1L
                            refresh()
                        }

                        else -> Unit
                    }
                }
                design.requests.onReceive {
                    when (it) {
                        TurnDesign.Request.Close -> finish()
                        TurnDesign.Request.OpenCaptcha -> design.captchaUrl?.let { url ->
                            startActivity(
                                TurnCaptchaActivity::class.intent
                                    .putExtra(TurnCaptchaActivity.EXTRA_URL, url)
                            )
                        }
                    }
                }
                if (activityStarted) {
                    ticker.onReceive {
                        refresh()
                    }
                }
            }
        }
    }

    /** The active profile's directory, or null when there is no active profile. */
    private suspend fun activeProfileDir(): File? {
        val active = runCatching { withProfile { queryActive() } }.getOrNull() ?: return null

        return File(importedDir, active.uuid.toString())
    }
}
