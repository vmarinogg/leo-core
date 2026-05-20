package herald

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// nowFn is the clock used for file-name rotation. Replaced in tests.
var nowFn = func() time.Time { return time.Now().UTC() }

// TelemetrySubscriber subscribes to Herald events and writes them as JSONL.
// This preserves the existing telemetry behavior from the former Transponder package.
type TelemetrySubscriber struct {
	dir     string
	enabled bool
	mu      sync.Mutex
}

// NewTelemetrySubscriber returns a TelemetrySubscriber that writes to
// <momDir>/telemetry/. If enabled is false no files are written.
func NewTelemetrySubscriber(momDir string, enabled bool) *TelemetrySubscriber {
	dir := filepath.Join(momDir, "logs")
	ts := &TelemetrySubscriber{dir: dir, enabled: enabled}
	if !enabled {
		return ts
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Printf("[herald] warn: cannot create telemetry dir %s: %v", dir, err)
	}
	return ts
}

// Register subscribes the TelemetrySubscriber to all known event types on bus.
func (ts *TelemetrySubscriber) Register(bus *Bus) {
	allTypes := []EventType{
		SessionStart,
		SessionEnd,
		TurnComplete,
		ToolUse,
		CompactTriggered,
		MemoryCreated,
		MemoryPromoted,
		MemorySearched,
		MemoryDeleted,
		RecordAppended,
		ConfigChanged,
		Error,
	}
	for _, et := range allTypes {
		et := et // capture
		bus.Subscribe(et, func(e Event) {
			ts.write(e)
		})
	}
}

// write serialises event as a JSONL line to the current day's file.
// Filesystem failures are logged at warn level; the method never panics.
func (ts *TelemetrySubscriber) write(event Event) {
	if !ts.enabled {
		return
	}

	record := map[string]any{
		"type":      string(event.Type),
		"timestamp": event.Timestamp.Format(time.RFC3339),
		"payload":   event.Payload,
	}

	line, err := json.Marshal(record)
	if err != nil {
		log.Printf("[herald] warn: marshal failed: %v", err)
		return
	}
	line = append(line, '\n')

	filename := nowFn().UTC().Format("2006-01-02") + ".jsonl"
	path := filepath.Join(ts.dir, filename)

	ts.mu.Lock()
	defer ts.mu.Unlock()

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("[herald] warn: open %s: %v", path, err)
		return
	}
	if _, err := f.Write(line); err != nil {
		log.Printf("[herald] warn: write %s: %v", path, err)
	}
	if err := f.Close(); err != nil {
		log.Printf("[herald] warn: close %s: %v", path, err)
	}
}
