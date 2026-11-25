package task

import (
	"sync"
	"time"
)

// reportInterval throttles how often a Progression pushes a snapshot to its
// sink. Progress updates frequently (once per file or per buffer), so without a
// throttle the SSE hub would be flooded. The value is small enough to feel like
// "almost real time" but large enough to keep the event stream light.
const reportInterval = 150 * time.Millisecond

// Progression is a thread-safe progress reporter for a single task. The work
// (copy, compress, ...) reports bytes and files through it; the pool wires the
// sink to push snapshots into the store and SSE hub. Calls from multiple goroutines
// are safe.
type Progression struct {
	mu         sync.Mutex
	totalBytes int64
	doneBytes  int64
	filesTotal int
	filesDone  int
	prevBytes  int64
	prevTime   time.Time
	rate       int64
	lastReport time.Time

	// sink receives byte-progress snapshots. It is nil until the pool wires it,
	// so a Progression used outside a Pool never panics.
	sink func(ByteProgress)
	// eventSink receives non-task events (e.g. a streamed search match) the work
	// wants to publish alongside the progress snapshots. It is nil until the
	// pool wires it. It is copied under the lock and invoked without holding it,
	// so an event sink that publishes to the SSE hub never runs while the
	// Progression lock is held.
	eventSink func(name string, data []byte)
}

// SetSink installs the callback that receives progress snapshots.
func (p *Progression) SetSink(sink func(ByteProgress)) {
	p.mu.Lock()
	p.sink = sink
	p.mu.Unlock()
}

// SetEventSink installs the callback that receives non-task events the work
// reports through ReportEvent.
func (p *Progression) SetEventSink(sink func(name string, data []byte)) {
	p.mu.Lock()
	p.eventSink = sink
	p.mu.Unlock()
}

// ReportEvent publishes an arbitrary named event with a JSON (or otherwise
// opaque) payload. Work that streams items as they are produced (search) uses
// it to push one event per item; the pool forwards these to the SSE hub. It is
// a no-op until an event sink is installed.
func (p *Progression) ReportEvent(name string, data []byte) {
	p.mu.Lock()
	sink := p.eventSink
	p.mu.Unlock()
	if sink != nil {
		sink(name, data)
	}
}

// SetTotal records the full size of the work up front so the UI can show a
// 0..100 bar against a known total rather than a spinning indeterminate one.
func (p *Progression) SetTotal(totalBytes int64, filesTotal int) {
	p.mu.Lock()
	p.totalBytes = totalBytes
	p.filesTotal = filesTotal
	// Reset the rate baseline so the first report does not show a spike.
	p.prevBytes = 0
	p.prevTime = time.Time{}
	p.mu.Unlock()
}

// AddBytes records n more bytes completed and reports when enough time has
// elapsed for a meaningful rate sample.
func (p *Progression) AddBytes(n int64) {
	if n <= 0 {
		return
	}
	p.mu.Lock()
	p.doneBytes += n
	p.emitLocked()
	p.mu.Unlock()
}

// AddFile records one completed file. Its bytes were already reported
// incrementally through AddBytes as each chunk was copied, so this only advances
// the file counter and emits so the UI can track "n / m files" live.
func (p *Progression) AddFile() {
	p.mu.Lock()
	p.filesDone++
	p.emitLocked()
	p.mu.Unlock()
}

// SetBytes records an absolute completed-bytes value (used for work that
// measures in bulk chunks rather than per file).
func (p *Progression) SetBytes(done int64) {
	p.mu.Lock()
	p.doneBytes = done
	p.emitLocked()
	p.mu.Unlock()
}

// Snapshot returns a copy of the current byte progress.
func (p *Progression) Snapshot() ByteProgress {
	p.mu.Lock()
	defer p.mu.Unlock()
	return ByteProgress{
		Total:      p.totalBytes,
		Done:       p.doneBytes,
		FilesTotal: p.filesTotal,
		FilesDone:  p.filesDone,
		RatePerSec: p.rate,
	}
}

// emitLocked samples the throughput and, when enough time has elapsed since the
// last report, hands a snapshot to the sink.
func (p *Progression) emitLocked() {
	if p.sink == nil {
		return
	}
	now := time.Now()
	// The first report fires immediately so the UI gets an initial signal, but the
	// throughput sample is skipped until a real interval elapses — otherwise the
	// very first rate divides the bytes by the ~two millennia since the zero time
	// value and reads back as zero.
	first := p.lastReport.IsZero()
	if first || now.Sub(p.lastReport) >= reportInterval {
		if !first {
			if elapsed := now.Sub(p.prevTime).Seconds(); elapsed >= 0.001 {
				p.rate = int64(float64(p.doneBytes-p.prevBytes) / elapsed)
			}
		}
		p.prevBytes = p.doneBytes
		p.prevTime = now
		p.lastReport = now
		p.sink(ByteProgress{
			Total:      p.totalBytes,
			Done:       p.doneBytes,
			FilesTotal: p.filesTotal,
			FilesDone:  p.filesDone,
			RatePerSec: p.rate,
		})
	}
}
