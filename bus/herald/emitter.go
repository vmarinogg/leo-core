package herald

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// Emitter writes operational telemetry events to JSONL files under
// <momDir>/telemetry/. It is safe for concurrent use.
type Emitter struct {
	dir     string
	enabled bool
	mu      sync.Mutex
}

// New returns an Emitter that writes to <momDir>/telemetry/.
// If enabled is false a no-op emitter is returned.
// If the telemetry directory does not exist it is created with mode 0755.
func New(momDir string, enabled bool) *Emitter {
	dir := filepath.Join(momDir, "logs")
	e := &Emitter{dir: dir, enabled: enabled}
	if !enabled {
		return e
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Printf("[herald] warn: cannot create telemetry dir %s: %v", dir, err)
	}
	return e
}

// Emit writes one event as a single JSON line. Filesystem failures are logged
// at warn level; the call always returns (never crashes the caller).
func (e *Emitter) Emit(event any) error {
	if !e.enabled {
		return nil
	}

	line, err := json.Marshal(event)
	if err != nil {
		log.Printf("[herald] warn: marshal failed: %v", err)
		return nil
	}
	line = append(line, '\n')

	filename := nowFn().UTC().Format("2006-01-02") + ".jsonl"
	path := filepath.Join(e.dir, filename)

	e.mu.Lock()
	defer e.mu.Unlock()

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("[herald] warn: open %s: %v", path, err)
		return nil
	}
	if _, err := f.Write(line); err != nil {
		log.Printf("[herald] warn: write %s: %v", path, err)
	}
	if err := f.Close(); err != nil {
		log.Printf("[herald] warn: close %s: %v", path, err)
	}
	return nil
}

// EmitSessionEvent emits a SessionEvent, stamping the Kind field.
func (e *Emitter) EmitSessionEvent(ev SessionEvent) {
	ev.Kind = "SessionEvent"
	if err := e.Emit(ev); err != nil {
		log.Printf("[herald] warn: EmitSessionEvent: %v", err)
	}
}

// EmitCaptureEvent emits a CaptureEvent, stamping the Kind field.
func (e *Emitter) EmitCaptureEvent(ev CaptureEvent) {
	ev.Kind = "CaptureEvent"
	if err := e.Emit(ev); err != nil {
		log.Printf("[herald] warn: EmitCaptureEvent: %v", err)
	}
}

// EmitMemoryMutation emits a MemoryMutation, stamping the Kind field.
func (e *Emitter) EmitMemoryMutation(ev MemoryMutation) {
	ev.Kind = "MemoryMutation"
	if err := e.Emit(ev); err != nil {
		log.Printf("[herald] warn: EmitMemoryMutation: %v", err)
	}
}

// EmitConsumptionEvent emits a ConsumptionEvent, stamping the Kind field.
func (e *Emitter) EmitConsumptionEvent(ev ConsumptionEvent) {
	ev.Kind = "ConsumptionEvent"
	if err := e.Emit(ev); err != nil {
		log.Printf("[herald] warn: EmitConsumptionEvent: %v", err)
	}
}

// EmitHarnessHealth emits a HarnessHealth event, stamping the Kind field.
func (e *Emitter) EmitHarnessHealth(ev HarnessHealth) {
	ev.Kind = "HarnessHealth"
	if err := e.Emit(ev); err != nil {
		log.Printf("[herald] warn: EmitHarnessHealth: %v", err)
	}
}
