// Package olcrtc runs the OLCRTC client — the second way a panel's WL entry can be carried,
// through a WebRTC call instead of VK's TURN relays (see docs/turn.md).
//
// Where the VK client is told what to do by the panel (the `?format=turn` document), this one
// is told by a link the user pastes: `olcrtc://…`. The link carries the provider, the transport,
// the room, the key — everything the engine needs.
//
// It listens as a SOCKS5 proxy on loopback, not as the raw forwarder the VK client is, so a
// subscription entry reaches it as a socks5 server rather than as a VLESS one; the two are kept
// apart in the app's own words for exactly that reason.
package olcrtc

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// DefaultListen is where the engine's SOCKS5 listener goes unless a caller says otherwise. Not
// 9000: that port belongs to the VK client, and both may be configured at once.
const DefaultListen = "127.0.0.1:1080"

// Link is an `olcrtc://` subscription link, parsed.
//
// The grammar is the one the clients in the wild use:
//
//		olcrtc://PROVIDER?TRANSPORT[<key=value&…>]@ROOM#KEY[%DEVICE][$LABEL]
//
//	  - PROVIDER  — which service carries the call (jitsi, telemost, wbstream, …);
//	  - TRANSPORT — how the tunnel rides it (datachannel, vp8channel, seichannel, videochannel);
//	  - <…>       — transport options; the clients that emit them also put an app-level identity in
//	    there (clientId, the install id the engine knows as DeviceID) and a fallback
//	    host (serverUrl) that only matters when ROOM is a bare room id;
//	  - ROOM      — the room URL, or just its id when the provider's host is known;
//	  - KEY       — the 64-hex shared key of the OLC2 record layer;
//	  - %DEVICE   — the device id, when the link carries one instead of the app;
//	  - $LABEL    — a human name for the link. Never passed to the engine.
type Link struct {
	Provider  string
	Transport string
	Options   map[string]string
	Room      string
	Key       string
	Device    string
	Label     string

	Raw string
}

// ParseLink reads a link. It refuses what the engine could not use — a missing room or a key
// that is not 64 hex — because a link that cannot work should fail where a person can see it,
// not as a tunnel that silently never carries anything.
func ParseLink(raw string) (*Link, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, fmt.Errorf("empty link")
	}

	const scheme = "olcrtc://"
	if !strings.HasPrefix(strings.ToLower(trimmed), scheme) {
		return nil, fmt.Errorf("not an olcrtc link: %q", trimmed)
	}

	payload := trimmed[len(scheme):]

	query := strings.IndexByte(payload, '?')
	if query < 0 {
		return nil, fmt.Errorf("olcrtc link without a transport: %q", trimmed)
	}

	link := &Link{Provider: strings.TrimSpace(payload[:query]), Options: map[string]string{}, Raw: trimmed}

	at := indexAfter(payload, '@', query)
	if at < 0 {
		return nil, fmt.Errorf("olcrtc link without a room: %q", trimmed)
	}

	hash := indexAfter(payload, '#', at)
	if hash < 0 {
		return nil, fmt.Errorf("olcrtc link without a key: %q", trimmed)
	}

	link.Room = strings.TrimSpace(payload[at+1 : hash])
	link.Transport, link.Options = parseTransport(payload[query+1 : at])
	link.Key, link.Device, link.Label = splitTail(payload[hash+1:])

	if link.Provider == "" {
		return nil, fmt.Errorf("olcrtc link without a provider: %q", trimmed)
	}
	if link.Transport == "" {
		return nil, fmt.Errorf("olcrtc link without a transport: %q", trimmed)
	}
	if link.Room == "" {
		return nil, fmt.Errorf("olcrtc link without a room: %q", trimmed)
	}
	if err := checkKey(link.Key); err != nil {
		return nil, err
	}

	// The app-level identity and the fallback host live in the option list.
	if device := link.Options["clientId"]; device != "" && link.Device == "" {
		link.Device = device
	}

	return link, nil
}

// Host is the room's host, for the log and for the status. An empty string when the room is a
// bare id, which some panels and links use.
func (l *Link) Host() string {
	if !strings.Contains(l.Room, "://") {
		// A bare room id: the host is the one the link's options name, if any.
		return l.Options["serverUrl"]
	}

	parsed, err := url.Parse(l.Room)
	if err != nil {
		return ""
	}

	return parsed.Host
}

// CheckListen refuses an address a tunnel should not be given: the engine itself allows a
// non-loopback listener only with SOCKS credentials, and this app has no business exposing one.
func CheckListen(listen string) error {
	host, port, err := net.SplitHostPort(strings.TrimSpace(listen))
	if err != nil {
		return fmt.Errorf("listen address %q: %w", listen, err)
	}

	if port == "" {
		return fmt.Errorf("listen address %q has no port", listen)
	}

	if _, err := strconv.Atoi(port); err != nil {
		return fmt.Errorf("listen port %q is not a number", port)
	}

	switch host {
	case "127.0.0.1", "::1", "localhost":
		return nil
	}

	return fmt.Errorf("listen address %q is not loopback", listen)
}

// indexAfter finds what comes first at or after start: the separator, or -1.
func indexAfter(s string, sep byte, start int) int {
	if start < 0 || start >= len(s) {
		return -1
	}

	if i := strings.IndexByte(s[start:], sep); i >= 0 {
		return start + i
	}

	return -1
}

// parseTransport reads `datachannel<clientId=…&serverUrl=…>`: the transport's name, then the
// options the emitting client chose to add.
func parseTransport(section string) (string, map[string]string) {
	options := map[string]string{}

	name := strings.TrimSpace(section)
	if open := strings.IndexByte(name, '<'); open >= 0 {
		if close := strings.IndexByte(name[open:], '>'); close > 0 {
			for _, pair := range strings.Split(name[open+1:open+close], "&") {
				key, value, ok := strings.Cut(pair, "=")
				if !ok {
					continue
				}

				key, value = strings.TrimSpace(key), strings.TrimSpace(value)
				if key != "" && value != "" {
					options[key] = value
				}
			}

			name = strings.TrimSpace(name[:open] + name[open+close+1:])
		}
	}

	return name, options
}

// splitTail cuts what follows the '#': the key, then an optional device and label.
func splitTail(tail string) (key, device, label string) {
	end := len(tail)

	if i := strings.IndexByte(tail, '$'); i >= 0 {
		end, label = i, strings.TrimSpace(tail[i+1:])
	}

	if i := strings.IndexByte(tail[:end], '%'); i >= 0 {
		device, end = strings.TrimSpace(tail[i+1:end]), i
	}

	return strings.TrimSpace(tail[:end]), device, label
}

// checkKey refuses anything the OLC2 record layer cannot use: the tunnel would come up and
// carry nothing, which is the kind of failure that costs an evening.
func checkKey(key string) error {
	if len(key) != 64 {
		return fmt.Errorf("key must be 64 hex characters, got %d", len(key))
	}

	for _, r := range key {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
		default:
			return fmt.Errorf("key is not hex: %q", key)
		}
	}

	return nil
}
