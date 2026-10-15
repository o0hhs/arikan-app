// Package params is the panel's TURN side channel as a document: what `?format=turn` serves,
// and which of a profile's proxies it belongs to.
//
// It is separate from the package that runs the clients (cfa/native/turn) on purpose: this is
// pure text and JSON, so it is testable on any machine, while running a client needs the
// Android core around it.
//
// The panel hands out a profile whose " WL" proxies point at 127.0.0.1:9000 — an address
// something on the device has to be listening on. That something used to be the user with a
// downloaded binary and five fields to fill in; here it is a client running in the core's own
// process, started with the profile and stopped with it.
//
// Two files live next to the profile (see [ParamsFileName] and [StatusFileName]): the
// parameters the panel served for `?format=turn`, and what the clients are doing. The first
// is written when the subscription is fetched, the second while the tunnel runs; the app
// reads both.
//
// The panel hands out a profile whose " WL" proxies point at 127.0.0.1:9000 — an address
// something on the device has to be listening on. That something used to be the user with a
// downloaded binary and five fields to fill in; here it is a client running in the core's own
// process, started with the profile and stopped with it.
//
// Two files live next to the profile (see [ParamsFileName] and [StatusFileName]): the
// parameters the panel served for `?format=turn`, and what the clients are doing. The first
// is written when the subscription is fetched, the second while the tunnel runs; the app
// reads both.
package params

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	P "path"
	"strconv"
	"strings"
	"time"
)

// ParamsFileName holds the panel's TURN parameters for the nodes of one profile, parsed out
// of the `?format=turn` document. It sits next to config.yaml, like fetch-headers.json.
const ParamsFileName = "turn.json"

// paramsVersion is bumped when the file's shape changes; readers ignore files they do not know.
const paramsVersion = 1

// Node is one `## name` block of the panel's document: everything the client for that node
// needs, plus what we learned by matching it against the profile.
type Node struct {
	Name string `json:"name"`
	// Peer is where our node's server half listens, host:port.
	Peer string `json:"peer"`
	// Vk is the call to join. A node without one cannot be started.
	Vk string `json:"vk"`
	// Listen is the local address the client must take, e.g. 127.0.0.1:9000.
	Listen string `json:"listen"`
	// Mode is the panel's word for the framing: "vless" (whole connections) or "udp".
	Mode string `json:"mode"`
	// Streams is how many TURN sessions to keep open ("n"); 0 means the client's default.
	Streams int `json:"n"`
	// UDP is whether to reach the TURN server over UDP ("udp: on").
	UDP bool `json:"udp"`
	// Proxies are the profile's proxies that point at this node's Listen address, filled in
	// when the profile is loaded. Empty means no entry in this profile uses this node.
	Proxies []string `json:"proxies,omitempty"`
	// Unusable says why this node cannot be run, empty when it can.
	Unusable string `json:"unusable,omitempty"`
}

// Params is the parsed document plus where it came from.
type Params struct {
	Version   int    `json:"version"`
	FetchedAt int64  `json:"fetched_at"`
	Nodes     []Node `json:"nodes"`
}

// ParseParams reads the panel's `# vk turn` document. A server that does not know the
// `format` parameter answers with the subscription itself, so anything that is not this
// document is refused rather than parsed into nonsense — the caller then drops the file.
func ParseParams(text string) (*Params, error) {
	lines := strings.Split(text, "\n")

	header := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		if !strings.EqualFold(trimmed, "# vk turn") {
			return nil, fmt.Errorf("not a vk turn document: first line is %q", trimmed)
		}

		header = true

		break
	}

	if !header {
		return nil, fmt.Errorf("empty vk turn document")
	}

	params := &Params{Version: paramsVersion, FetchedAt: time.Now().Unix()}

	var current *Node

	flush := func() {
		if current == nil {
			return
		}

		current.Unusable = unusableReason(*current)
		params.Nodes = append(params.Nodes, *current)
		current = nil
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, "##") {
			continue
		}

		if name, ok := strings.CutPrefix(trimmed, "##"); ok {
			flush()

			current = &Node{Name: strings.TrimSpace(name)}

			continue
		}

		if current == nil {
			// A key before the first section: the document is not what it claims to be.
			return nil, fmt.Errorf("vk turn document has %q outside a section", trimmed)
		}

		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			return nil, fmt.Errorf("vk turn document has a line without a key: %q", trimmed)
		}

		key, value = strings.TrimSpace(key), strings.TrimSpace(value)

		switch strings.ToLower(key) {
		case "peer":
			current.Peer = value
		case "vk":
			current.Vk = value
		case "listen":
			current.Listen = value
		case "mode":
			current.Mode = value
		case "n":
			streams, err := strconv.Atoi(value)
			if err != nil {
				return nil, fmt.Errorf("vk turn node %q has n: %s", current.Name, value)
			}
			current.Streams = streams
		case "udp":
			current.UDP = strings.EqualFold(value, "on") || strings.EqualFold(value, "true")
		}
	}

	flush()

	if len(params.Nodes) == 0 {
		return nil, fmt.Errorf("vk turn document has no sections")
	}

	return params, nil
}

// unusableReason says why a node cannot be started, "" when it can. The panel omits lines it
// has nothing for (a node whose admin set no call link, an `udp:` that is simply off), so a
// missing value is a reason, not a parse error.
func unusableReason(node Node) string {
	switch {
	case node.Peer == "":
		return "нет адреса сервера (peer)"
	case node.Listen == "":
		return "нет локального адреса (listen)"
	case node.Vk == "":
		return "нет ссылки на звонок VK"
	case !strings.Contains(node.Peer, ":"):
		return "адрес сервера без порта"
	case !strings.Contains(node.Listen, ":"):
		return "локальный адрес без порта"
	}

	return ""
}

// SaveParams writes the parsed document next to the profile.
func SaveParams(profilePath string, params *Params) error {
	bytes, err := json.MarshalIndent(params, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(P.Join(profilePath, ParamsFileName), bytes, 0600)
}

// LoadParams reads the file written by [SaveParams]. A missing file is not an error: most
// profiles have no WL entries, and a profile imported from a server that does not know
// `format=turn` never gets one.
func LoadParams(profilePath string) (*Params, error) {
	bytes, err := os.ReadFile(P.Join(profilePath, ParamsFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}

		return nil, err
	}

	var params Params
	if err := json.Unmarshal(bytes, &params); err != nil {
		return nil, err
	}

	return &params, nil
}

// DropParams removes the file, so a profile whose source stopped answering with TURN
// parameters does not keep running yesterday's ones.
func DropParams(profilePath string) {
	_ = os.Remove(P.Join(profilePath, ParamsFileName))
}

// MatchProxies fills in which of the profile's proxies point at each node, and is why a node
// nobody uses is not started: a client costs a VK call join, and the panel gives every node
// its own local port for exactly this reason.
//
// The link is the port. The panel names the subscription entry after the connection
// ("<имя подключения> WL", or whatever the admin called it) and the TURN section after the
// node, so the names need not agree; the address does — the entry's server/port is the
// client's own listen address, which is what makes that entry work at all.
func MatchProxies(params *Params, proxies []map[string]any) {
	for i := range params.Nodes {
		params.Nodes[i].Proxies = nil

		port, err := listenPort(params.Nodes[i].Listen)
		if err != nil {
			continue
		}

		for _, proxy := range proxies {
			server, _ := proxy["server"].(string)
			if !isLoopback(server) {
				continue
			}

			if proxyPort, ok := proxyPort(proxy["port"]); !ok || proxyPort != port {
				continue
			}

			name, _ := proxy["name"].(string)
			if name == "" {
				name = server + ":" + strconv.Itoa(port)
			}

			params.Nodes[i].Proxies = append(params.Nodes[i].Proxies, name)
		}
	}
}

// UsedNodes returns the nodes this profile actually routes through.
func (p *Params) UsedNodes() []Node {
	if p == nil {
		return nil
	}

	var used []Node

	for _, node := range p.Nodes {
		if len(node.Proxies) > 0 && node.Unusable == "" {
			used = append(used, node)
		}
	}

	return used
}

func listenPort(listen string) (int, error) {
	_, port, err := net.SplitHostPort(listen)
	if err != nil {
		return 0, err
	}

	return strconv.Atoi(port)
}

// isLoopback is the panel's side of the contract: the WL entry's server is the client's own
// machine ("127.0.0.1" by default, and the admin may have written ::1 or localhost).
func isLoopback(server string) bool {
	switch strings.ToLower(strings.TrimSpace(server)) {
	case "127.0.0.1", "::1", "localhost":
		return true
	}

	return false
}

// proxyPort reads a proxy's port, which the profile may carry as a number or as a string.
func proxyPort(value any) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	case string:
		port, err := strconv.Atoi(v)
		if err != nil {
			return 0, false
		}

		return port, true
	}

	return 0, false
}
