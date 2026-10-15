package com.github.kr328.clash.core.model

import android.os.Parcel
import android.os.Parcelable
import com.github.kr328.clash.core.util.Parcelizer
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

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

        /** The lines of [TurnStatus.log] that belong to this node. */
        fun logLines(status: TurnStatus): List<String> {
            if (streamBase <= 0) return emptyList()

            return status.log.filter { line -> line.belongsTo(streamBase, streamsMax) }
        }

        private fun String.belongsTo(base: Int, count: Int): Boolean {
            val stream = streamTag(this) ?: return false

            return stream >= base && stream < base + count.coerceAtLeast(1)
        }

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

    companion object CREATOR : Parcelable.Creator<TurnStatus> {
        override fun createFromParcel(parcel: Parcel): TurnStatus {
            return Parcelizer.decodeFromParcel(serializer(), parcel)
        }

        override fun newArray(size: Int): Array<TurnStatus?> {
            return arrayOfNulls(size)
        }
    }
}
