// Package crier projects canonical events from the Ledger (ADR 0021)
// into the Vault via Librarian (ADR 0022). Crier is the sole projector:
// archtests forbid it from importing storage/vault directly, and an
// archtest in #369 forbids services from reading the Ledger.
//
// Semantics are at-least-once + idempotent. Crier reads the Ledger
// from checkpoint+1, applies each event via librarian.ApplyLedgerEvent
// (which writes the projection and advances the checkpoint in a
// single transaction), and continues.
//
// The MVP exposes a one-shot Replay that drains the Ledger from
// checkpoint to head. A daemon-loop variant (Run-with-poll) is a
// follow-up — Replay is sufficient for #368 (replay tests) and for
// the v0.50 wire-up.
package crier

import (
	"errors"
	"fmt"
	"time"

	"github.com/momhq/mom/storage/ledger"
	"github.com/momhq/mom/storage/librarian"
)

// Projector is the Librarian-shaped surface Crier needs. Implemented
// by storage/librarian.Librarian (production); a test double in
// crier_test.go covers idempotency and error paths.
type Projector interface {
	GetCrierCheckpoint() (librarian.CrierCheckpoint, error)
	ApplyLedgerEvent(offset int64, eventType, sessionID string, createdAt time.Time, payload map[string]any) (applied bool, err error)
}

// LedgerReader is the Ledger-shaped surface Crier needs. Implemented
// by storage/ledger.Ledger.
type LedgerReader interface {
	Iterate(from uint64) *ledger.Iter
}

// Crier reads from a LedgerReader and projects into a Projector.
// Construct via New; call Replay to drain the Ledger up to its head.
type Crier struct {
	led LedgerReader
	pro Projector
}

// New constructs a Crier wired to led and pro.
func New(led LedgerReader, pro Projector) *Crier {
	return &Crier{led: led, pro: pro}
}

// Stats summarizes a Replay run.
type Stats struct {
	// Applied is the number of events that produced a NEW projection
	// (the row did not already exist).
	Applied int
	// Skipped is the number of events that were re-applied no-op-ly:
	// offset <= checkpoint, or the op_events UNIQUE index caught a
	// duplicate.
	Skipped int
	// LastOffset is the offset of the most recently processed event,
	// or the prior checkpoint when no events were available.
	LastOffset int64
}

// Replay drains the Ledger from checkpoint+1 to the current head and
// returns a Stats summary. Errors during a single projection abort
// the Replay (returning a partial Stats); the next Replay call
// resumes from the last successful checkpoint.
//
// Replay holds no exclusive lock on the Ledger or the Librarian —
// concurrent producers may continue appending while Crier reads.
// Iter.Next is safe against in-flight appends because each segment
// read uses its own file handle and stops at the first incomplete
// record.
func (c *Crier) Replay() (Stats, error) {
	if c.led == nil || c.pro == nil {
		return Stats{}, errors.New("crier: not wired (led=nil or pro=nil)")
	}
	cp, err := c.pro.GetCrierCheckpoint()
	if err != nil {
		return Stats{}, fmt.Errorf("crier: get checkpoint: %w", err)
	}
	stats := Stats{LastOffset: cp.Offset}
	startFrom := uint64(0)
	if cp.Offset >= 0 {
		startFrom = uint64(cp.Offset + 1)
	}
	it := c.led.Iterate(startFrom)
	defer it.Close()
	for {
		rec, ok := it.Next()
		if !ok {
			break
		}
		applied, err := c.pro.ApplyLedgerEvent(
			int64(rec.Offset),
			string(rec.Event.Type),
			rec.Event.SessionID,
			rec.AppendedAt,
			rec.Event.Payload,
		)
		if err != nil {
			return stats, fmt.Errorf("crier: apply offset %d: %w", rec.Offset, err)
		}
		if applied {
			stats.Applied++
		} else {
			stats.Skipped++
		}
		stats.LastOffset = int64(rec.Offset)
	}
	if err := it.Err(); err != nil {
		return stats, fmt.Errorf("crier: iterate: %w", err)
	}
	return stats, nil
}
