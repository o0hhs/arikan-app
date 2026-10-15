package com.github.kr328.clash.core.model

import android.os.Parcel
import android.os.Parcelable
import com.github.kr328.clash.core.util.Parcelizer
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import java.io.File

/**
 * What the VK TURN clients of the active profile are doing — the core's answer to
 * `Clash.queryTurnStatus()`, and the same document the core keeps in `turn-status.json` next
 * to the profile.
 *
 * A panel's " WL" entries are ordinary proxies pointing at 127.0.0.1:9000, and this is the
 * program that listens there. One [Node] per node the panel offers: [Node.state] tells whether
 * its client runs, and [Node.proxies] whether this profile routes through it at all.
 */
@Serializable
class TurnStatus : Parcelable {
    @SerialName("updated_at")
    var updatedAt: Long = 0

    /** The profile these clients were started for; empty when none is loaded. */
    var profile: String? = null

    var nodes: List<Node> = emptyList()

    /** The tail of the clients' shared log, one entry per line. */
    var log: List<String> = emptyList()

    /**
     * One node's client. [state] is one of: `starting`, `running`, `captcha`, `failed`,
     * `stopped`, `unused` — `running` with [streams] above zero is the one that means the
     * node carries traffic.
     */
    @Serializable
    class Node : Parcelable {
        var name: String = ""

        var peer: String = ""

        var listen: String = ""

        var mode: String = ""

        /** This profile's proxies that point at [listen]; empty: nothing routes here. */
        var proxies: List<String> = emptyList()

        /** Why the node cannot run at all (no call link, no peer), empty when it can. */
        var unusable: String? = null

        var state: String = ""

        var streams: Int = 0

        @SerialName("streams_max")
        var streamsMax: Int = 0

        var reconnects: Int = 0

        /**
         * The first stream ID this node's client logs with. A log line's `[STREAM n]` tag
         * falls inside `[streamBase, streamBase + streamsMax)`, which is how one shared log
         * splits by node.
         */
        @SerialName("stream_base")
        var streamBase: Int = 0

        /** Where to answer a captcha, while [state] is `captcha`. */
        @SerialName("captcha_url")
        var captchaUrl: String? = null

        @SerialName("last_error")
        var lastError: String? = null

        var since: Long = 0

        /** Whether a line of the shared log came from this node's client. */
        fun owns(line: String): Boolean {
            if (streamBase <= 0) return false

            val stream = streamTag(line) ?: return false

            return stream >= streamBase && stream < streamBase + streamsMax.coerceAtLeast(1)
        }

        /** The lines of [TurnStatus.log] that belong to this node. */
        fun logLines(status: TurnStatus): List<String> = status.log.filter { owns(it) }

        private fun streamTag(line: String): Int? {
            val start = line.indexOf("[STREAM ")
            if (start < 0) return null

            val end = line.indexOf(']', start)
            if (end < 0) return null

            return line.substring(start + "[STREAM ".length, end).trim().toIntOrNull()
        }

        override fun writeToParcel(parcel: Parcel, flags: Int) =
            Parcelizer.encodeToParcel(serializer(), parcel, this)

        override fun describeContents(): Int = 0

        companion object CREATOR : Parcelable.Creator<Node> {
            override fun createFromParcel(parcel: Parcel): Node =
                Parcelizer.decodeFromParcel(serializer(), parcel)

            override fun newArray(size: Int): Array<Node?> = arrayOfNulls(size)
        }
    }

    override fun writeToParcel(parcel: Parcel, flags: Int) {
        Parcelizer.encodeToParcel(serializer(), parcel, this)
    }

    override fun describeContents(): Int {
        return 0
    }

    /** The lines of [log] no node claims — the core's own notes about the clients. */
    fun coreLines(): List<String> = log.filter { line -> nodes.none { it.owns(line) } }

    companion object CREATOR : Parcelable.Creator<TurnStatus> {
        /** The file the core keeps this document in, next to the profile. */
        const val FILE_NAME = "turn-status.json"

        /** The clients' log, next to the profile: appended while the tunnel runs. */
        const val LOG_FILE_NAME = "turn.log"

        private val json = Json { ignoreUnknownKeys = true }

        /**
         * Read the status the core wrote for a profile, or null when there is none — a profile
         * whose subscription carries no WL entries never grows one, and a stopped tunnel leaves
         * the last one behind for as long as the profile is loaded.
         */
        fun readFrom(profileDir: File): TurnStatus? {
            val file = File(profileDir, FILE_NAME)
            if (!file.isFile) return null

            return runCatching { json.decodeFromString(serializer(), file.readText()) }.getOrNull()
        }

        override fun createFromParcel(parcel: Parcel): TurnStatus {
            return Parcelizer.decodeFromParcel(serializer(), parcel)
        }

        override fun newArray(size: Int): Array<TurnStatus?> {
            return arrayOfNulls(size)
        }
    }
}
