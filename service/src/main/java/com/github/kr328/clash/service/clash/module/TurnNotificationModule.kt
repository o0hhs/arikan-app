package com.github.kr328.clash.service.clash.module

import android.app.PendingIntent
import android.app.Service
import android.content.Intent
import androidx.core.app.NotificationChannelCompat
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import com.github.kr328.clash.common.compat.getColorCompat
import com.github.kr328.clash.common.compat.pendingIntentFlags
import com.github.kr328.clash.common.constants.Intents
import com.github.kr328.clash.core.Clash
import com.github.kr328.clash.core.model.TurnStatus
import com.github.kr328.clash.service.R
import com.github.kr328.clash.service.data.ImportedDao
import com.github.kr328.clash.service.store.ServiceStore
import com.github.kr328.clash.service.util.importedDir
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.selects.onTimeout
import kotlinx.coroutines.selects.select
import kotlinx.coroutines.withContext
import java.io.File

/**
 * Tells the user what the WL clients of the active profile are doing, when there is something
 * to tell (see docs/turn.md).
 *
 * The clients run in this process and write down what they are doing in `turn-status.json` next
 * to the profile. Nothing else would notice that one of them is waiting for a captcha a human
 * has to answer, or that none of them can get in at all — which is exactly the state where
 * "the subscription works but nothing loads" comes from. So those two states get a
 * notification, and so does a client that has been trying without a single stream for long
 * enough that "it is still starting" stopped being a comfortable explanation.
 *
 * While the clients are working, or before they have had a chance to fail, this stays quiet.
 */
class TurnNotificationModule(service: Service) : Module<Unit>(service) {
    override suspend fun run() {
        createNotificationChannel(service)

        val stateChanged = receiveBroadcast(capacity = Channel.CONFLATED) {
            addAction(Intents.ACTION_PROFILE_LOADED)
            addAction(Intents.ACTION_PROFILE_CHANGED)
            addAction(Intents.ACTION_CLASH_STARTED)
            addAction(Intents.ACTION_TURN_REQUEST_CAPTCHA)
        }

        val manager = NotificationManagerCompat.from(service)

        // The "still trying" note appears only after this long without a stream: a normal start
        // takes a few seconds, and a notification that shows up for every connect is noise.
        var tryingSince: Long? = null

        // What the notification currently says, so a poll that changes nothing does not re-post.
        var shownKey: String? = null

        try {
            while (true) {
                select<Unit> {
                    stateChanged.onReceive { intent ->
                        // The screen asks for a fresh challenge when the user presses "answer the
                        // captcha": the clients hold the lockout, and this is the only way past it
                        // from another process.
                        if (intent.action == Intents.ACTION_TURN_REQUEST_CAPTCHA) {
                            Clash.requestTurnCaptcha()
                        }
                    }
                    onTimeout(PollIntervalMillis) { }
                }

                val status = readStatus()
                val note = noteFor(status, tryingSince)

                when {
                    note == null -> {
                        if (status == null || status.nodes.none { it.state == STATE_STARTING }) {
                            tryingSince = null
                        } else if (tryingSince == null) {
                            tryingSince = System.currentTimeMillis()
                        }

                        if (shownKey != null) {
                            manager.cancel(NOTIFICATION_ID)

                            shownKey = null
                        }
                    }

                    note.key == shownKey -> Unit

                    else -> {
                        manager.notify(NOTIFICATION_ID, buildNotification(note))

                        shownKey = note.key
                    }
                }
            }
        } finally {
            withContext(NonCancellable) {
                manager.cancel(NOTIFICATION_ID)
            }
        }
    }

    /** The status the core wrote for the active profile, or null when it wrote none yet. */
    private suspend fun readStatus(): TurnStatus? {
        val current = ServiceStore(service).activeProfile ?: return null
        val active = runCatching { ImportedDao().queryByUUID(current) }.getOrNull() ?: return null

        return runCatching {
            TurnStatus.readFrom(File(service.importedDir, active.uuid.toString()))
        }.getOrNull()
    }

    private fun noteFor(status: TurnStatus?, tryingSince: Long?): Note? {
        if (status == null) return null

        val nodes = status.nodes

        val captcha = nodes.firstOrNull { !it.captchaUrl.isNullOrBlank() }
        if (captcha != null) {
            return Note(
                key = "captcha:${captcha.listen}:${captcha.captchaUrl}",
                title = service.getString(R.string.turn_note_captcha_title),
                text = service.getString(R.string.turn_note_captcha_text, captcha.name.ifEmpty { captcha.listen }),
                captchaUrl = captcha.captchaUrl,
            )
        }

        val failed = nodes.firstOrNull { it.state == STATE_FAILED && !it.lastError.isNullOrBlank() }
        if (failed != null) {
            return Note(
                key = "failed:${failed.listen}:${failed.lastError}",
                title = service.getString(R.string.turn_note_failed_title),
                text = service.getString(
                    R.string.turn_note_failed_text,
                    failed.name.ifEmpty { failed.listen },
                    failed.lastError,
                ),
            )
        }

        // Nothing has come up and nothing has given up: say so once it has been long enough.
        val starting = nodes.filter { it.state == STATE_STARTING }
        val running = nodes.any { it.state == STATE_RUNNING && it.streams > 0 }

        if (starting.isEmpty() || running || tryingSince == null) return null

        if (System.currentTimeMillis() - tryingSince < TryingGraceMillis) return null

        return Note(
            key = "trying:${starting.size}",
            title = service.getString(R.string.turn_note_trying_title),
            text = service.getString(R.string.turn_note_trying_text, starting.size),
        )
    }

    private fun buildNotification(note: Note): android.app.Notification {
        val target = if (note.captchaUrl != null) {
            Intent()
                .setClassName(service.packageName, CAPTCHA_ACTIVITY)
                .putExtra(Intents.EXTRA_TURN_CAPTCHA_URL, note.captchaUrl)
        } else {
            Intent().setClassName(service.packageName, TURN_ACTIVITY)
        }.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)

        return NotificationCompat.Builder(service, CHANNEL_ID)
            .setSmallIcon(R.drawable.ic_logo_service)
            .setColor(service.getColorCompat(R.color.color_clash))
            .setContentTitle(note.title)
            .setContentText(note.text)
            .setStyle(NotificationCompat.BigTextStyle().bigText(note.text))
            .setContentIntent(
                PendingIntent.getActivity(
                    service,
                    R.id.nf_turn_status,
                    target,
                    pendingIntentFlags(PendingIntent.FLAG_UPDATE_CURRENT),
                )
            )
            .setAutoCancel(true)
            .setShowWhen(false)
            .build()
    }

    /** One thing worth saying, with the identity that decides whether it needs re-posting. */
    private class Note(
        val key: String,
        val title: CharSequence,
        val text: CharSequence,
        val captchaUrl: String? = null,
    )

    companion object {
        const val CHANNEL_ID = "turn_status_channel"
        private const val NOTIFICATION_ID = 0x574C // "WL"

        private const val PollIntervalMillis = 5_000L
        private const val TryingGraceMillis = 30_000L

        // The app's screens, named as strings: they live in the application module, which this
        // one cannot see, and the notification is posted from here.
        private const val TURN_ACTIVITY = "com.github.kr328.clash.turn.TurnActivity"
        private const val CAPTCHA_ACTIVITY = "com.github.kr328.clash.turn.TurnCaptchaActivity"

        private const val STATE_RUNNING = "running"
        private const val STATE_STARTING = "starting"
        private const val STATE_FAILED = "failed"

        fun createNotificationChannel(service: Service) {
            NotificationManagerCompat.from(service).createNotificationChannel(
                NotificationChannelCompat.Builder(CHANNEL_ID, NotificationManagerCompat.IMPORTANCE_DEFAULT)
                    .setName(service.getString(R.string.turn_status_channel))
                    .setDescription(service.getString(R.string.turn_status_channel_description))
                    .build()
            )
        }
    }
}
