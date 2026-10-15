package turn

import (
	"io"
	"log"
	"os"
	P "path"
	"strings"
	"sync"
)

// The clients log through the standard logger — the package they come from is upstream's,
// and its log lines are what a person reads when a node will not come up. That logger is
// process-wide, so the whole log is one stream shared by every node; a line's own
// "[STREAM n]" tag is what attributes it to a node (see NodeStatus.StreamBase).

// ringBuffer keeps the last lines of the log in memory, so the status can carry a tail
// without reading the file back.
type ringBuffer struct {
	mu    sync.Mutex
	lines []string
	next  int
	full  bool
}

func newRingBuffer(size int) *ringBuffer {
	return &ringBuffer{lines: make([]string, size)}
}

func (r *ringBuffer) add(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.lines[r.next] = line
	r.next = (r.next + 1) % len(r.lines)

	if r.next == 0 {
		r.full = true
	}
}

// tail returns the last lines in order, oldest first.
func (r *ringBuffer) tail(count int) []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	size := len(r.lines)
	if !r.full {
		size = r.next
	}

	if count > size {
		count = size
	}

	out := make([]string, 0, count)

	for i := size - count; i < size; i++ {
		index := i
		if r.full {
			index = (r.next + i) % len(r.lines)
		}

		out = append(out, r.lines[index])
	}

	return out
}

// logSink is the clients' log on disk: appended line by line and rotated, because the
// interesting part of a TURN client's log is what it said just before it stopped working and
// a plain truncate throws exactly that away.
type logSink struct {
	mu   sync.Mutex
	file *os.File
	path string
	size int64
	ring *ringBuffer
}

// LogFileName is the clients' log next to the profile: appended line by line, rotated once.
const LogFileName = "turn.log"

const (
	logRotateBytes = 256 * 1024
	logRingLines   = 200
)

func openLog(profilePath string) (*logSink, error) {
	path := P.Join(profilePath, LogFileName)

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		return nil, err
	}

	info, err := file.Stat()
	if err != nil {
		_ = file.Close()

		return nil, err
	}

	return &logSink{
		file: file,
		path: path,
		size: info.Size(),
		ring: newRingBuffer(logRingLines),
	}, nil
}

func (l *logSink) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		if line != "" {
			l.ring.add(line)
		}
	}

	if l.file == nil {
		return len(p), nil
	}

	if l.size += int64(len(p)); l.size > logRotateBytes {
		if err := l.rotate(); err != nil {
			return 0, err
		}
	}

	if _, err := l.file.Write(p); err != nil {
		return 0, err
	}

	return len(p), nil
}

func (l *logSink) rotate() error {
	_ = l.file.Close()

	if err := os.Rename(l.path, l.path+".1"); err != nil {
		return err
	}

	file, err := os.OpenFile(l.path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}

	l.file, l.size = file, 0

	return nil
}

func (l *logSink) close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.file == nil {
		return nil
	}

	err := l.file.Close()
	l.file = nil

	return err
}

func (l *logSink) tail(count int) []string {
	return l.ring.tail(count)
}

// logWriter is what the standard logger writes to: stderr (free on Android, useful when the
// core runs on a desktop) and whichever profile's log is current.
type logWriter struct {
	mu   sync.Mutex
	sink *logSink
}

var logFile = &logWriter{}

func (w *logWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	sink := w.sink
	w.mu.Unlock()

	_, _ = os.Stderr.Write(p)

	if sink == nil {
		return len(p), nil
	}

	return sink.Write(p)
}

// swap points the log at the current profile's file, or at nothing.
func (w *logWriter) swap(sink *logSink) *logSink {
	w.mu.Lock()
	defer w.mu.Unlock()

	previous := w.sink
	w.sink = sink

	return previous
}

var installLogOnce sync.Once

// installLog attaches the sink to the standard logger. Once per process: every client and the
// core's own notes about them go through it, and a profile switch only moves where it writes.
func installLog() {
	installLogOnce.Do(func() {
		log.SetOutput(io.MultiWriter(os.Stderr, logFile))
	})
}

// Report writes one line to the clients' log: the core's own view of what it did with them
// (which node it started, why it could not), interleaved with the client's lines. A bug
// report with that log in it is readable end to end.
func Report(format string, args ...any) {
	log.Printf("[turn] "+format, args...)
}

// openLog points the log at this profile and closes the previous one.
func (m *Manager) openLog(profilePath string) {
	installLog()

	sink, err := openLog(profilePath)
	if err != nil {
		Report("Open %s: %s", LogFileName, err.Error())

		return
	}

	previous := logFile.swap(sink)

	m.mu.Lock()
	m.log = sink
	m.mu.Unlock()

	if previous != nil {
		_ = previous.close()
	}
}

// closeLog releases the log file, for the switch to a profile that has none.
func (m *Manager) closeLog() {
	previous := logFile.swap(nil)

	m.mu.Lock()
	m.log = nil
	m.mu.Unlock()

	if previous != nil {
		_ = previous.close()
	}
}
