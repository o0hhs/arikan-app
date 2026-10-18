package olcrtc

import "testing"

// A real link, from the panel the user tests against: provider, transport, the app-level
// identity in the option list, the room, the key and a display name.
const userLink = "olcrtc://jitsi?datachannel<clientId=install-a2842e87776dd9ca5e96bb2259cc40cc&serverUrl=meet.riddlerx.org>" +
	"@https://meet.riddlerx.org/olcrtc-98gfgwhr" +
	"#35d17561ab1a324c406b6c3cc725ef88098a446321c9f6107a1132da7b113b6a$PortalVPN | Обход белых списков"

func TestParseLinkReadsARealLink(t *testing.T) {
	link, err := ParseLink(userLink)
	if err != nil {
		t.Fatalf("ParseLink: %v", err)
	}

	if link.Provider != "jitsi" {
		t.Errorf("provider: got %q", link.Provider)
	}
	if link.Transport != "datachannel" {
		t.Errorf("transport: got %q", link.Transport)
	}
	if link.Room != "https://meet.riddlerx.org/olcrtc-98gfgwhr" {
		t.Errorf("room: got %q", link.Room)
	}
	if len(link.Key) != 64 {
		t.Errorf("key: got %d characters", len(link.Key))
	}
	if link.Device != "install-a2842e87776dd9ca5e96bb2259cc40cc" {
		t.Errorf("device: got %q", link.Device)
	}
	if link.Label != "PortalVPN | Обход белых списков" {
		t.Errorf("label: got %q", link.Label)
	}
	if host := link.Host(); host != "meet.riddlerx.org" {
		t.Errorf("host: got %q", host)
	}
	if link.Options["serverUrl"] != "meet.riddlerx.org" {
		t.Errorf("serverUrl option: got %q", link.Options["serverUrl"])
	}
}

// Links without the option list exist too, and the engine does not need them.
func TestParseLinkWithoutOptions(t *testing.T) {
	link, err := ParseLink("olcrtc://telemost?vp8channel@12345678" +
		"#00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	if err != nil {
		t.Fatalf("ParseLink: %v", err)
	}

	if link.Transport != "vp8channel" {
		t.Errorf("transport: got %q", link.Transport)
	}
	if link.Room != "12345678" {
		t.Errorf("room: got %q", link.Room)
	}
	// A bare room id with no serverUrl option has no host to report.
	if host := link.Host(); host != "" {
		t.Errorf("host: got %q", host)
	}
}

// A link that cannot work has to fail here, where a person pasting it can see the reason —
// not later as a tunnel that carries nothing.
func TestParseLinkRefusesWhatCannotWork(t *testing.T) {
	cases := map[string]string{
		"no scheme":     "https://meet.example.org/room#00112233",
		"no transport":  "olcrtc://jitsi@https://meet.example.org/room#0011",
		"no room":       "olcrtc://jitsi?datachannel#" + sixtyFourHex,
		"no key":        "olcrtc://jitsi?datachannel@https://meet.example.org/room",
		"short key":     "olcrtc://jitsi?datachannel@https://meet.example.org/room#00112233",
		"key not hex":   "olcrtc://jitsi?datachannel@https://meet.example.org/room#" + notHex,
		"empty":         "",
		"just the name": "olcrtc://",
	}

	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseLink(raw); err == nil {
				t.Fatalf("expected an error for %q", raw)
			}
		})
	}
}

// The engine allows a listener outside loopback only when it has SOCKS credentials; this app
// never sets any, so it must never be pointed anywhere else.
func TestCheckListenKeepsItLoopback(t *testing.T) {
	for _, ok := range []string{"127.0.0.1:1080", "localhost:9000", "[::1]:1080"} {
		if err := CheckListen(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}

	for _, bad := range []string{"0.0.0.0:1080", "192.168.1.5:1080", "127.0.0.1", "127.0.0.1:port"} {
		if err := CheckListen(bad); err == nil {
			t.Errorf("%s: expected a refusal", bad)
		}
	}
}

const sixtyFourHex = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

const notHex = "zz112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"
