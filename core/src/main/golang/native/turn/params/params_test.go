package params

import (
	"strings"
	"testing"
)

// The document the panel serves for `?format=turn` (site/src/content/docs/nodes/turn.mdx).
const panelDocument = `# vk turn

## 🇩🇪 Германия
peer: 203.0.113.9:56000
vk: https://vk.com/call/join/XXXXXXXX
listen: 127.0.0.1:9000
mode: vless
n: 10
udp: on

## 🇳🇱 Нидерланды
peer: 203.0.113.10:56001
vk: https://vk.com/call/join/YYYYYYYY
listen: 127.0.0.1:9001
mode: udp
n: 4
`

func TestParseParamsPanelDocument(t *testing.T) {
	params, err := ParseParams(panelDocument)
	if err != nil {
		t.Fatalf("ParseParams: %v", err)
	}

	if len(params.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(params.Nodes))
	}

	germany := params.Nodes[0]
	if germany.Name != "🇩🇪 Германия" {
		t.Errorf("name: got %q", germany.Name)
	}
	if germany.Peer != "203.0.113.9:56000" {
		t.Errorf("peer: got %q", germany.Peer)
	}
	if germany.Listen != "127.0.0.1:9000" {
		t.Errorf("listen: got %q", germany.Listen)
	}
	if germany.Mode != "vless" {
		t.Errorf("mode: got %q", germany.Mode)
	}
	if germany.Streams != 10 {
		t.Errorf("n: got %d", germany.Streams)
	}
	if !germany.UDP {
		t.Error("udp: expected on")
	}
	if germany.Unusable != "" {
		t.Errorf("unusable: got %q", germany.Unusable)
	}

	// The `udp:` line is only written when it is on, so its absence has to mean off rather
	// than a parse failure.
	netherlands := params.Nodes[1]
	if netherlands.UDP {
		t.Error("udp: expected off when the line is absent")
	}
	if netherlands.Streams != 4 {
		t.Errorf("n: got %d", netherlands.Streams)
	}
}

// A panel that does not know `format=turn` (or a proxy that rewrote the URL) answers with the
// subscription itself. Writing that into turn.json would leave a phantom node behind.
func TestParseParamsRefusesOtherDocuments(t *testing.T) {
	cases := map[string]string{
		"subscription": "proxies:\n  - name: node\n    server: 203.0.113.9\n",
		"html":         "<!DOCTYPE html><html><body>404</body></html>",
		"empty":        "   \n\n",
		"keys only":    "# vk turn\npeer: 203.0.113.9:56000\n",
	}

	for name, document := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseParams(document); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestParseParamsMarksUnusableNodes(t *testing.T) {
	document := `# vk turn

## без ссылки
peer: 203.0.113.9:56000
listen: 127.0.0.1:9000
mode: vless
n: 10

## без peer
vk: https://vk.com/call/join/XXXXXXXX
listen: 127.0.0.1:9001
mode: vless
n: 10
`

	params, err := ParseParams(document)
	if err != nil {
		t.Fatalf("ParseParams: %v", err)
	}

	if params.Nodes[0].Unusable == "" {
		t.Error("node without a call link must be unusable")
	}
	if params.Nodes[1].Unusable == "" {
		t.Error("node without a peer must be unusable")
	}
	if used := params.UsedNodes(); len(used) != 0 {
		t.Errorf("unusable nodes must not be started, got %d", len(used))
	}
}

func TestMatchProxiesByListenPort(t *testing.T) {
	params, err := ParseParams(panelDocument)
	if err != nil {
		t.Fatalf("ParseParams: %v", err)
	}

	proxies := []map[string]any{
		{"name": "🇩🇪 Германия WL", "server": "127.0.0.1", "port": 9000},
		{"name": "🇩🇪 Германия", "server": "203.0.113.9", "port": 443},
		// The admin may name the entry anything; the address is what ties it to the node.
		{"name": "Мой сервер", "server": "::1", "port": "9001"},
		{"name": "чужой порт", "server": "127.0.0.1", "port": 9002},
		{"name": "чужой сервер", "server": "10.0.0.1", "port": 9000},
	}

	MatchProxies(params, proxies)

	if got := params.Nodes[0].Proxies; len(got) != 1 || got[0] != "🇩🇪 Германия WL" {
		t.Errorf("node 1 proxies: got %v", got)
	}

	// The second node's listen port is given as a string in the profile, as YAML allows.
	if got := params.Nodes[1].Proxies; len(got) != 1 || got[0] != "Мой сервер" {
		t.Errorf("node 2 proxies: got %v", got)
	}

	used := params.UsedNodes()
	if len(used) != 2 {
		t.Fatalf("expected both nodes used, got %d", len(used))
	}
}

func TestMatchProxiesDropsUnusedNodes(t *testing.T) {
	params, err := ParseParams(panelDocument)
	if err != nil {
		t.Fatalf("ParseParams: %v", err)
	}

	MatchProxies(params, []map[string]any{
		{"name": "только первая нода", "server": "127.0.0.1", "port": 9000},
	})

	if len(params.Nodes[0].Proxies) != 1 {
		t.Error("first node should be used")
	}
	if len(params.Nodes[1].Proxies) != 0 {
		t.Error("second node should be unused")
	}

	used := params.UsedNodes()
	if len(used) != 1 || used[0].Name != "🇩🇪 Германия" {
		t.Errorf("used nodes: got %v", used)
	}
}

func TestParseParamsRefusesKeyOutsideSection(t *testing.T) {
	_, err := ParseParams("# vk turn\npeer: 203.0.113.9:56000\n")
	if err == nil || !strings.Contains(err.Error(), "outside a section") {
		t.Fatalf("expected an outside-a-section error, got %v", err)
	}
}
