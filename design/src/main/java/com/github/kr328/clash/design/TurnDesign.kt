package com.github.kr328.clash.design

import android.content.Context
import android.view.View
import androidx.core.view.isVisible
import com.google.android.material.color.MaterialColors
import com.github.kr328.clash.core.model.TurnStatus
import com.github.kr328.clash.design.databinding.DesignTurnBinding
import com.github.kr328.clash.design.databinding.ItemTurnNodeBinding
import com.github.kr328.clash.design.util.*
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import androidx.appcompat.R as AppCompatR
import com.google.android.material.R as MaterialR

/**
 * The WL screen: the VK TURN clients of the active profile — one card per node the panel
 * offers, and the log the clients write.
 *
 * A panel subscription carries its connection twice: once normally, and once as a proxy that
 * points at the device itself (`127.0.0.1:9000`). The clients the core runs there are what
 * makes that second entry work, and this is where they can be watched.
 */
class TurnDesign(context: Context) : Design<TurnDesign.Request>(context) {
    enum class Request {
        Close,

        /** Open the client's own captcha page, which it serves on loopback. */
        OpenCaptcha,
    }

    /**
     * The captcha page waiting to be answered, if any — the URL a client serves while it needs
     * a human (see [patch]). There is one page per process, so one of them at a time.
     */
    var captchaUrl: String? = null
        private set

    private val binding = DesignTurnBinding
        .inflate(context.layoutInflater, context.root, false)

    override val root: View
        get() = binding.root

    init {
        binding.self = this
    }

    /** For the layout's click handlers, like the other settings screens. */
    fun request(request: Request) {
        requests.trySend(request)
    }

    /**
     * Show the status the core wrote, or nothing when the profile has none — a profile whose
     * subscription carries no WL entries has no clients to report.
     */
    suspend fun patch(status: TurnStatus?) = withContext(Dispatchers.Main) {
        binding.nodes.removeAllViews()

        val nodes = status?.nodes.orEmpty()

        binding.summary.text = when {
            status == null -> context.getString(R.string.turn_empty)
            nodes.isEmpty() -> context.getString(R.string.turn_no_nodes)
            else -> context.getString(R.string.turn_summary, nodes.count { it.state == STATE_RUNNING && it.streams > 0 }, nodes.size)
        }

        captchaUrl = nodes.firstOrNull { !it.captchaUrl.isNullOrBlank() }?.captchaUrl

        for (node in nodes) {
            val item = ItemTurnNodeBinding.inflate(context.layoutInflater, binding.nodes, false)

            item.name.text = node.name.ifEmpty { node.listen }
            item.state.text = context.getString(stateLabel(node.state))
            item.state.setTextColor(MaterialColors.getColor(item.state, stateColor(node.state)))
            item.details.text = context.getString(
                R.string.turn_node_details,
                node.listen,
                node.peer,
                node.mode,
                node.streams,
                node.streamsMax,
                node.reconnects,
            )
            item.proxies.isVisible = node.proxies.isNotEmpty()
            item.proxies.text = context.getString(R.string.turn_node_proxies, node.proxies.joinToString(", "))

            val problem = node.unusable ?: node.lastError?.takeIf { node.state == STATE_FAILED }
            item.error.isVisible = !problem.isNullOrBlank()
            item.error.text = problem

            item.captcha.isVisible = !node.captchaUrl.isNullOrBlank()
            item.captcha.setOnClickListener { requests.trySend(Request.OpenCaptcha) }

            val lines = status?.let { node.logLines(it) }.orEmpty()
            item.log.isVisible = lines.isNotEmpty()
            item.log.text = lines.joinToString("\n")

            binding.nodes.addView(item.root)
        }

        // The core's own lines about the clients — which node it started, why it could not —
        // belong to no node's stream range and are shown on their own.
        val core = status?.coreLines().orEmpty()
        binding.coreLog.isVisible = core.isNotEmpty()
        binding.coreLog.text = core.joinToString("\n")
        binding.logTitle.isVisible = core.isNotEmpty()
    }

    private fun stateLabel(state: String): Int = when (state) {
        STATE_RUNNING -> R.string.turn_state_running
        STATE_STARTING -> R.string.turn_state_starting
        STATE_CAPTCHA -> R.string.turn_state_captcha
        STATE_FAILED -> R.string.turn_state_failed
        STATE_STOPPED -> R.string.turn_state_stopped
        else -> R.string.turn_state_unused
    }

    private fun stateColor(state: String): Int = when (state) {
        STATE_RUNNING -> AppCompatR.attr.colorPrimary
        STATE_CAPTCHA -> MaterialR.attr.colorTertiary
        STATE_FAILED -> AppCompatR.attr.colorError
        else -> MaterialR.attr.colorOnSurfaceVariant
    }

    private companion object {
        // The core's own words for these (see native/turn in the Go side).
        const val STATE_STARTING = "starting"
        const val STATE_RUNNING = "running"
        const val STATE_CAPTCHA = "captcha"
        const val STATE_FAILED = "failed"
        const val STATE_STOPPED = "stopped"
    }
}
