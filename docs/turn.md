# WL: the panel's VK TURN entries

A subscription from an Arikan panel lists some connections twice. Besides the ordinary entry
there is a second one with the same credentials, named after the first (`… WL`) and pointing at
the user's own device — `127.0.0.1:9000` by default. That entry is a plain proxy as far as the
engine is concerned, and something has to listen on that address for it to carry anything.

That something is the `vk-turn-proxy` client, which drives the traffic through VK call TURN
relays — the way out of a network that only lets calls through. Upstream, the user downloads
that client and types five fields into it by hand (`peer`, `vk-link`, `listen`, `n`, `udp`).
Here the app does it: the client is built into the core and runs in the service process, one
client per node the profile routes through.

## Where the pieces live

| Piece | Where |
| --- | --- |
| The panel's parameters (`peer`, `vk`, `listen`, `mode`, `n`, `udp`) | `turn.json` next to the profile |
| Their parser and the match against the profile's proxies | `core/src/main/golang/native/turn/params` |
| The clients: start, stop, status, log | `core/src/main/golang/native/turn` |
| What the clients are doing, for the app process | `turn-status.json` next to the profile |
| The clients' log (appended, rotated once) | `turn.log`, `turn.log.1` |
| The library the client comes from | our fork of vk-turn-proxy v1.8.3, see NOTICE |
| The screen | Settings → Connection → **WL** |

## The path a subscription takes

1. **Import or update.** The profile fetch asks the panel twice: the subscription itself, and
   the same URL with `format=turn`. The second answer is parsed into `turn.json` — and dropped
   again if it is not a `# vk turn` document, which is what a panel that does not know the
   parameter answers with (`findTurnParams` in `native/config/fetch.go`).
2. **Load.** Loading a profile matches those parameters against the profile that was just
   parsed: a node counts as *used* when some proxy in the profile has a loopback `server` and a
   `port` equal to the node's `listen` port. The names need not agree — the panel names the
   subscription entry after the connection and the TURN section after the node — and the port is
   what the WL mechanism itself uses, so it is what ties the two together.
3. **Run.** One client per used node, started from `native/config/load.go` and stopped from
   `reset()`: a client lives exactly as long as the tunnel it feeds, so switching profiles or
   stopping the VPN takes the VK call down with it.
4. **Watch.** Every state change rewrites `turn-status.json`; the WL screen polls that file,
   because the clients run in the service process and the screen lives in the app's.

## Two things that are easy to get wrong

**The client's own traffic must not enter the tunnel.** It opens sockets for VK's API, for DNS
and for the TURN server, and by default Android routes an app's traffic into its own VPN. Those
sockets are opened through mihomo's socket hook instead — `VpnService.protect` — so they leave
the device directly, which is also what the manual setup does when the helper app is excluded
from the VPN. See `SocketPolicy` in the client fork.

**A captcha needs a human.** VK sometimes answers a call-link request with a captcha. The
client solves the first ones itself; the last resort is a page it serves on
`127.0.0.1:8765` and rewrites so that every request goes through itself — which is also where it
reads the answer from. The WL screen shows that page in a WebView when a node is in the
`captcha` state, and the client carries on by itself once it is answered.

## Reading the log

Every client numbers its streams in its own range of 100 (`StreamIDBase`), so a line's
`[STREAM n]` tag says which node it came from: `turn-status.json` carries each node's
`stream_base`, and both the Go status and the app's screen use it to split one shared log by
node. Lines the core itself writes (starting a node, refusing one) are prefixed `[turn]`.

## When it does not work

| Symptom | Look at |
| --- | --- |
| The WL entry does not connect at all | `turn.json` exists? The profile's WL entry port equals a node's `listen`? |
| State `starting` forever, retries climbing | The node's client cannot reach VK: `turn.log`, `last_error` in `turn-status.json` |
| State `captcha` | Answer it from the WL screen |
| State `failed` with "нет ссылки на звонок VK" | The panel's node has no call link configured (admin side) |
| Nothing in the WL screen | The profile has no `turn.json`: its panel does not serve `?format=turn`, or the profile was imported before the panel started doing so — update the subscription |
