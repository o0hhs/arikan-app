package turn

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	P "path"
	"sync"
	"syscall"
	"time"

	vkclient "github.com/cacggghp/vk-turn-proxy/client"
	"github.com/metacubex/mihomo/component/dialer"

	"cfa/native/app"
	"cfa/native/turn/params"
)

// nodeState is what one node's client is doing, as far as the manager can see. It is what the
// app shows: "работает" is StateRunning with at least one stream up.
const (
	StateStarting = "starting"
	StateRunning  = "running"
	StateCaptcha  = "captcha"
	StateFailed   = "failed"
	StateStopped  = "stopped"
	// StateUnused is a node the panel offers that this profile has no entry for: nothing to
	// run, but worth showing next to the others.
	StateUnused = "unused"
)

// NodeStatus is one node in the status file and in TurnStatus().
type NodeStatus struct {
	Name       string   `json:"name"`
	Peer       string   `json:"peer"`
	Listen     string   `json:"listen"`
	Mode       string   `json:"mode"`
	Proxies    []string `json:"proxies,omitempty"`
	Unusable   string   `json:"unusable,omitempty"`
	State      string   `json:"state"`
	Streams    int      `json:"streams"`
	StreamsMax int      `json:"streams_max"`
	Reconnects int      `json:"reconnects"`
	// StreamBase is the first stream ID this node's client logs with: a log line's
	// "[STREAM n]" tag falls inside [StreamBase, StreamBase+StreamsMax), which is how the app
	// splits one shared log by node.
	StreamBase int    `json:"stream_base"`
	CaptchaURL string `json:"captcha_url,omitempty"`
	LastError  string `json:"last_error,omitempty"`
	Since      int64  `json:"since"`
}

// Status is the whole picture: every node the panel offers, what its client is doing, and
// the tail of the log. Written next to the profile while the tunnel runs; the app reads it.
type Status struct {
	UpdatedAt int64        `json:"updated_at"`
	Profile   string       `json:"profile,omitempty"`
	Nodes     []NodeStatus `json:"nodes"`
	Log       []string     `json:"log,omitempty"`
}

// runner is one node's client: what it was started with and what it has told us since.
type runner struct {
	node params.Node
	base int

	cancel context.CancelFunc
	done   chan struct{}

	mu         sync.Mutex
	state      string
	streams    int
	reconnects int
	captcha    string
	lastError  string
	since      int64
}

// StatusFileName is the status of the clients, next to the profile they were started for: the
// app reads it there, in the process that shows the UI (the clients run in the service's).
const StatusFileName = "turn-status.json"

// Manager owns the clients of the active profile. One per process: the profile belongs to the
// process, and so does the tunnel the clients feed.
type Manager struct {
	mu      sync.Mutex
	profile string
	nodes   *params.Params
	runners []*runner
	log     *logSink
}

// manager is the process-wide manager: the core's entry points (the profile load and the
// bridge's turnStart/turnStop) all drive this one.
var manager = &Manager{}

// ManagerFor returns the process-wide manager the core's entry points drive.
func ManagerFor() *Manager {
	return manager
}

// setup installs the process-wide hooks the client needs from its host. Idempotent: the hooks
// are the same for every profile, and a profile switch must not tear them down.
var setup = sync.OnceFunc(func() {
	// The client's own traffic — VK's API and call link, DNS, the TURN server — has to leave
	// the device outside our tunnel. mihomo's socket hook is exactly that: it calls
	// VpnService.protect through the JNI bridge, and refuses the dial while the user has
	// "block connections without VPN" on. Without it the client's traffic would be routed
	// into the tunnel it is feeding.
	//
	// The hook is read per socket instead of captured once: it is installed by the core's own
	// delegate.Init, and a build that never got that far must leave the sockets alone rather
	// than fail every dial.
	vkclient.SetSocketPolicy(vkclient.SocketPolicy{Control: func(network, address string, c syscall.RawConn) error {
		if hook := dialer.DefaultSocketHook; hook != nil {
			return hook(network, address, c)
		}

		return nil
	}})

	// Manual captcha: the client serves its page on 127.0.0.1:8765 and used to shell out to
	// a browser, which an Android app process cannot do. The app shows the URL in a WebView.
	vkclient.SetBrowserOpener(func(streamID int, url string) {
		manager.captchaOpened(streamID, url)
	})

	vkclient.SetCaptchaResolvedHook(func(streamID int) {
		manager.captchaSolved(streamID)
	})
})

// Start (re)starts the clients of a freshly loaded profile: the nodes its WL entries point at,
// and no others — a client costs a VK call join, so a node nobody routes through stays down.
//
// It is called with the profile the engine just loaded, so a profile switch stops the old
// clients before the new ones start.
func (m *Manager) Start(profilePath string, proxies []map[string]any) {
	setup()

	m.Stop()

	stored, err := params.LoadParams(profilePath)
	if err != nil {
		Report("Load %s: %s", params.ParamsFileName, err.Error())
	}

	if stored != nil {
		// The panel may have renumbered ports or dropped a node since the last import; the
		// match against the profile that was just loaded is what decides what runs.
		params.MatchProxies(stored, proxies)

		if err := params.SaveParams(profilePath, stored); err != nil {
			Report("Save %s: %s", params.ParamsFileName, err.Error())
		}
	}

	m.mu.Lock()
	m.profile = profilePath
	m.nodes = stored
	log := m.log
	m.mu.Unlock()

	if stored == nil {
		// Nothing to run and nothing to report: a profile without WL entries must not grow
		// files it has no use for.
		if log != nil {
			m.closeLog()
		}

		return
	}

	m.openLog(profilePath)

	for index, node := range stored.UsedNodes() {
		run := m.startRunner(node, nodeBase(index), node.Streams)

		m.mu.Lock()
		m.runners = append(m.runners, run)
		m.mu.Unlock()

		Report("Node %q: client started on %s (peer %s, %s, n=%d, udp=%v)",
			node.Name, node.Listen, node.Peer, node.Mode, node.Streams, node.UDP)
	}

	for _, node := range stored.Nodes {
		if node.Unusable != "" {
			Report("Node %q: cannot be started: %s", node.Name, node.Unusable)
		}
	}

	m.writeStatus()
}

// StartFromParams starts clients for parameters given as JSON, without a profile. The
// automatic path (Start) is what the app runs; this is what the core exports, and what makes
// the client reachable from tests and tools.
func (m *Manager) StartFromParams(paramsJSON string) error {
	var parsed params.Params
	if err := json.Unmarshal([]byte(paramsJSON), &parsed); err != nil {
		return fmt.Errorf("parse turn params: %w", err)
	}

	setup()

	m.Stop()

	m.mu.Lock()
	m.nodes = &parsed
	m.mu.Unlock()

	for index, node := range parsed.Nodes {
		if node.Unusable != "" {
			continue
		}

		run := m.startRunner(node, nodeBase(index), node.Streams)

		m.mu.Lock()
		m.runners = append(m.runners, run)
		m.mu.Unlock()
	}

	m.writeStatus()

	return nil
}

// Stop stops every client and leaves the status at "stopped", keeping what was known about
// each node so the app can show what just went down.
func (m *Manager) Stop() {
	m.mu.Lock()
	runners := m.runners
	m.runners = nil
	m.mu.Unlock()

	for _, run := range runners {
		run.cancel()
	}

	for _, run := range runners {
		select {
		case <-run.done:
		case <-time.After(clientStopTimeout):
			Report("Node %q: client did not stop in time", run.node.Name)
		}

		run.mu.Lock()
		run.state = StateStopped
		run.streams = 0
		run.captcha = ""
		run.mu.Unlock()
	}

	if len(runners) > 0 {
		m.writeStatus()
	}
}

// clientStopTimeout bounds how long a teardown waits for one client. The client's own
// goroutines give up as soon as their context is cancelled; the ones waiting on a captcha
// human can take a moment longer.
const clientStopTimeout = 5 * time.Second

// startRunner starts one node's client.
func (m *Manager) startRunner(node params.Node, base, streams int) *runner {
	ctx, cancel := context.WithCancel(context.Background())

	run := &runner{
		node:   node,
		base:   base,
		cancel: cancel,
		done:   make(chan struct{}),
		state:  StateStarting,
		since:  time.Now().Unix(),
	}

	cfg := vkclient.Config{
		Peer:       node.Peer,
		Listen:     node.Listen,
		VkLink:     node.Vk,
		NumStreams: streams,
		UDP:        node.UDP,
		VLESS:      node.Mode == "vless",
		Debug:      app.DebugBuild(),
		// One client per StreamsPerInstance stream IDs: the client finds its own state from
		// a stream ID, and a log line's tag then names its node.
		StreamIDBase: base,
		OnEvent:      run.onEvent,
	}

	if last := lastOf(node.Proxies); last != "" {
		Report("Node %q: serving %s", node.Name, last)
	}

	go func() {
		defer close(run.done)

		if err := vkclient.Run(ctx, cfg); err != nil {
			run.fail(err.Error())
		}
	}()

	return run
}

// nodeBase is the stream ID range of the node at index: StreamsPerInstance slots each,
// starting at 1 so that stream ID 0 stays unassigned.
func nodeBase(index int) int {
	return (index + 1) * vkclient.StreamsPerInstance
}

// runnerFor finds the client a stream ID belongs to. The hooks that reach here only get an
// ID, which is exactly why the client numbers its streams in per-node ranges.
func (m *Manager) runnerFor(streamID int) *runner {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, run := range m.runners {
		if streamID >= run.base && streamID < run.base+vkclient.StreamsPerInstance {
			return run
		}
	}

	return nil
}

func (m *Manager) captchaOpened(streamID int, url string) {
	run := m.runnerFor(streamID)
	if run == nil {
		return
	}

	run.mu.Lock()
	run.state = StateCaptcha
	run.captcha = url
	run.mu.Unlock()

	Report("Node %q: VK asks for a captcha, answer it at %s", run.node.Name, url)

	m.writeStatus()
}

func (m *Manager) captchaSolved(streamID int) {
	run := m.runnerFor(streamID)
	if run == nil {
		return
	}

	run.mu.Lock()
	if run.state == StateCaptcha {
		run.state = StateRunning
	}
	run.captcha = ""
	run.mu.Unlock()

	Report("Node %q: captcha answered", run.node.Name)

	m.writeStatus()
}

// onEvent is where a client's state changes land, called from the client's own goroutines.
func (r *runner) onEvent(event vkclient.Event) {
	r.mu.Lock()

	switch event.Kind {
	case vkclient.EventListening:
		r.state = StateRunning
	case vkclient.EventEstablished:
		r.streams++
		if r.state == StateStarting {
			r.state = StateRunning
		}
	case vkclient.EventClosed:
		if r.streams > 0 {
			r.streams--
		}
	case vkclient.EventRetry:
		r.reconnects++
		r.lastError = event.Detail
	case vkclient.EventCaptcha:
		r.state = StateCaptcha
		r.captcha = event.Detail
	case vkclient.EventFatal:
		r.state = StateFailed
		r.lastError = event.Detail
		r.captcha = ""
	}

	streams, state := r.streams, r.state

	r.mu.Unlock()

	if event.Kind == vkclient.EventEstablished {
		Report("Node %q: stream %d is up (%d connected)", r.node.Name, event.StreamID, streams)
	}

	// Retries are frequent when VK is refusing; the status file is written on the states the
	// user sees instead of on every attempt.
	if event.Kind != vkclient.EventRetry {
		manager.writeStatus()
	}

	if event.Kind == vkclient.EventClosed && streams == 0 && state != StateFailed {
		Report("Node %q: no streams left", r.node.Name)
	}
}

func (r *runner) fail(reason string) {
	r.mu.Lock()
	r.state = StateFailed
	r.lastError = reason
	r.mu.Unlock()

	Report("Node %q: client stopped: %s", r.node.Name, reason)

	manager.writeStatus()
}

// StatusJSON is the status the bridge hands out: what every node's client is doing plus the
// log tail — the same picture the app reads from the status file next to the profile.
func (m *Manager) StatusJSON() string {
	bytes, err := json.Marshal(m.status())
	if err != nil {
		return "{}"
	}

	return string(bytes)
}

func (m *Manager) status() Status {
	m.mu.Lock()
	profile, stored, runners, log := m.profile, m.nodes, append([]*runner(nil), m.runners...), m.log
	m.mu.Unlock()

	status := Status{UpdatedAt: time.Now().Unix(), Profile: profile}

	byListen := map[string]*runner{}
	for _, run := range runners {
		byListen[run.node.Listen] = run
	}

	if stored != nil {
		for _, node := range stored.Nodes {
			status.Nodes = append(status.Nodes, nodeStatus(node, byListen[node.Listen]))
		}
	}

	if log != nil {
		status.Log = log.ring.tail(statusLogLines)
	}

	return status
}

// nodeStatus is what the status file says about one node: what the panel asked for, and what
// its client is doing — or why nothing is running.
func nodeStatus(n params.Node, run *runner) NodeStatus {
	node := NodeStatus{
		Name:       n.Name,
		Peer:       n.Peer,
		Listen:     n.Listen,
		Mode:       n.Mode,
		Proxies:    n.Proxies,
		Unusable:   n.Unusable,
		StreamsMax: n.Streams,
		State:      StateUnused,
		Since:      time.Now().Unix(),
	}

	switch {
	case n.Unusable != "":
		node.State = StateFailed
		node.LastError = n.Unusable
	case run == nil:
		// The panel offers this node, the profile does not use it.
		return node
	}

	if run == nil {
		return node
	}

	run.mu.Lock()
	node.State = run.state
	node.Streams = run.streams
	node.Reconnects = run.reconnects
	node.StreamBase = run.base
	node.CaptchaURL = run.captcha
	node.LastError = run.lastError
	node.Since = run.since
	run.mu.Unlock()

	return node
}

// statusLogLines is how much of the log the status carries: enough for a screen, not enough
// to make the status file big.
const statusLogLines = 40

func (m *Manager) writeStatus() {
	m.mu.Lock()
	profile, stored := m.profile, m.nodes
	m.mu.Unlock()

	if profile == "" || stored == nil {
		return
	}

	bytes, err := json.MarshalIndent(m.status(), "", "  ")
	if err != nil {
		return
	}

	// Best-effort: the status is what the app shows, it must never fail a load.
	_ = os.WriteFile(P.Join(profile, StatusFileName), bytes, 0600)
}

func lastOf(names []string) string {
	if len(names) == 0 {
		return ""
	}

	return names[len(names)-1]
}
