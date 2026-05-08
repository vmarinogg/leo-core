// Package drafter consumes the v0.30 Herald event stream and persists
// memories into the Librarian. Two paths share this package:
//
//  1. Watcher path — turn.observed events flow through the soft and
//     hard filters, then accumulate in a per-session in-memory buffer.
//     A flush trigger (turn-count cap, idle timeout, or shutdown)
//     drains the buffer; boundary detection splits it into chunks;
//     each chunk becomes one memory whose tags are auto-extracted
//     from the chunk text. This is the clustering pipeline mom v1
//     used to produce structured memories rather than per-turn
//     fragments.
//
//  2. Record path — memory.record events bypass filters, buffering,
//     and clustering. The user's explicitness wins (ADR 0014).
//
// Privacy contract: redaction runs BEFORE the turn enters the
// buffer. A crash that loses the buffer cannot leak secrets — the
// only loss is the unflushed prose, already redacted. The crash
// window is bounded by idleFlushAfter and flushAtTurnCount; v0.30
// alpha accepts that window per #240.
package drafter

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/jdkato/prose/v2"
	"golang.org/x/text/unicode/norm"

	"github.com/momhq/mom/cli/internal/herald"
	"github.com/momhq/mom/cli/internal/librarian"
)

// Defaults are tuned for v0.30 alpha and exposed as constants so the
// Lens diagnostics layer can show the live configuration.
const (
	defaultBoundaryThreshold = 0.6
	defaultIdleFlushAfter    = 90 * time.Second
	defaultFlushAtTurnCount  = 50
	maxTagsPerMemory         = 15
)

// MemoryRecordEventType re-exports herald.MemoryRecord for source
// compatibility with PR 2 callers that imported it from this package.
var MemoryRecordEventType = herald.MemoryRecord

// Drafter buffers per-session turns and flushes them as clustered
// memories. One Drafter per process.
type Drafter struct {
	lib *librarian.Librarian

	mu      sync.Mutex
	pending map[string]*sessionBuffer

	boundaryThreshold float64
	idleFlushAfter    time.Duration
	flushAtTurnCount  int

	now func() time.Time
}

type sessionBuffer struct {
	turns    []bufferedTurn
	lastSeen time.Time
	// bus is the Herald bus the most recent turn for this session
	// arrived on. flushSession publishes op events on it. Stored
	// per-session because in global watch mode one Drafter is shared
	// across many project buses, and Tick / FlushAll have no per-call
	// bus context.
	bus *herald.Bus
}

// bufferedTurn carries the post-redaction text plus the metadata
// needed at flush time. Tool inputs are scanned for filter_audit
// during ingest and then dropped; they never enter the buffer.
type bufferedTurn struct {
	timestamp time.Time
	role      string
	text      string
	redacted  []string
	harness   string
	provider  string
	model     string
}

// New returns a Drafter bound to the given Librarian, configured with
// the v0.30 defaults.
func New(lib *librarian.Librarian) *Drafter {
	return &Drafter{
		lib:               lib,
		pending:           map[string]*sessionBuffer{},
		boundaryThreshold: defaultBoundaryThreshold,
		idleFlushAfter:    defaultIdleFlushAfter,
		flushAtTurnCount:  defaultFlushAtTurnCount,
		now:               time.Now,
	}
}

// SubscribeAll wires both event types Drafter consumes on the bus.
// Returns a single unsubscribe function detaching both.
func (d *Drafter) SubscribeAll(bus *herald.Bus) func() {
	stopTurn := bus.Subscribe(herald.TurnObserved, func(e herald.Event) {
		if e.SessionID == "" {
			fmt.Fprintf(os.Stderr, "drafter: drop %q event with empty session_id\n", e.Type)
			return
		}
		if err := d.observeTurn(bus, e); err != nil {
			fmt.Fprintf(os.Stderr, "drafter: observe turn (session=%s): %v\n", e.SessionID, err)
		}
	})
	stopRecord := bus.Subscribe(herald.MemoryRecord, func(e herald.Event) {
		if e.SessionID == "" {
			fmt.Fprintf(os.Stderr, "drafter: drop %q event with empty session_id\n", e.Type)
			return
		}
		if err := d.processRecord(bus, e); err != nil {
			fmt.Fprintf(os.Stderr, "drafter: process record (session=%s): %v\n", e.SessionID, err)
		}
	})
	return func() {
		stopTurn()
		stopRecord()
	}
}

// observeTurn applies soft + hard filters, then buffers the redacted
// turn. Triggers a session flush if the turn-count cap fires.
func (d *Drafter) observeTurn(bus *herald.Bus, e herald.Event) error {
	role, _ := e.Payload["role"].(string)
	text, _ := e.Payload["text"].(string)
	model, _ := e.Payload["model"].(string)
	provider, _ := e.Payload["provider"].(string)
	harness, _ := e.Payload["harness"].(string)
	tcs := tcsFromPayload(e.Payload["tool_calls"])

	soft := softTurn{
		Role:                   role,
		Text:                   text,
		ToolCount:              len(tcs),
		CodebaseWriteToolCount: countCategory(tcs, "codebase_write"),
	}
	if isNoise(soft) {
		bus.Publish(herald.Event{
			Type:      herald.OpMemoryDropped,
			SessionID: e.SessionID,
			Payload: map[string]any{
				"reason":  "soft_filter",
				"role":    role,
				"harness": harness,
			},
		})
		return nil
	}

	redactedText, textCats := redactSecrets(text)
	cats := map[string]struct{}{}
	for _, c := range textCats {
		cats[c] = struct{}{}
	}
	for _, tc := range tcs {
		if tc.Input == nil {
			continue
		}
		blob, err := json.Marshal(tc.Input)
		if err != nil {
			continue
		}
		_, more := redactSecrets(string(blob))
		for _, c := range more {
			cats[c] = struct{}{}
		}
	}
	for c := range cats {
		if err := d.lib.IncrementFilterAudit(c); err != nil {
			fmt.Fprintf(os.Stderr, "drafter: filter_audit bump %q: %v\n", c, err)
		}
	}

	bt := bufferedTurn{
		timestamp: extractCreatedAt(e.Payload),
		role:      role,
		text:      redactedText,
		redacted:  categoriesSlice(cats),
		harness:   harness,
		provider:  provider,
		model:     model,
	}
	if bt.timestamp.IsZero() {
		bt.timestamp = d.now()
	}

	flush := false
	d.mu.Lock()
	sb, ok := d.pending[e.SessionID]
	if !ok {
		sb = &sessionBuffer{}
		d.pending[e.SessionID] = sb
	}
	sb.turns = append(sb.turns, bt)
	sb.lastSeen = d.now()
	sb.bus = bus
	// Between this Unlock and flushSession's Lock another turn can
	// arrive on the same session and append; flushSession will then
	// drain it too — correct, just non-obvious.
	if len(sb.turns) >= d.flushAtTurnCount {
		flush = true
	}
	d.mu.Unlock()

	if flush {
		d.flushSession(e.SessionID)
	}
	return nil
}

// FlushAll flushes every pending session. Called on shutdown so the
// in-memory buffer never silently drops on clean exit.
func (d *Drafter) FlushAll() {
	d.mu.Lock()
	sids := make([]string, 0, len(d.pending))
	for sid := range d.pending {
		sids = append(sids, sid)
	}
	d.mu.Unlock()
	for _, sid := range sids {
		d.flushSession(sid)
	}
}

// Tick checks pending buffers and flushes any session whose lastSeen
// is older than idleFlushAfter relative to now. Production wires
// this from a periodic ticker (see cmd.startDrafterTicker); tests
// pass an advanced timestamp directly.
func (d *Drafter) Tick(now time.Time) {
	var toFlush []string
	d.mu.Lock()
	for sid, sb := range d.pending {
		if now.Sub(sb.lastSeen) >= d.idleFlushAfter {
			toFlush = append(toFlush, sid)
		}
	}
	d.mu.Unlock()
	for _, sid := range toFlush {
		d.flushSession(sid)
	}
}

// flushSession drains the per-session buffer and persists one memory
// per chunk emitted by boundary detection. Op events publish on the
// bus stored on the buffer (the bus the most recent turn arrived on).
func (d *Drafter) flushSession(sessionID string) {
	d.mu.Lock()
	sb, ok := d.pending[sessionID]
	if !ok || len(sb.turns) == 0 {
		delete(d.pending, sessionID)
		d.mu.Unlock()
		return
	}
	turns := sb.turns
	bus := sb.bus
	delete(d.pending, sessionID)
	d.mu.Unlock()

	// Snapshot the existing tag corpus ONCE per flush, not per chunk.
	// A long session can produce many chunks; querying AllTagNames N
	// times means N full scans of the tags table for the same data.
	// Failures are non-fatal — an empty vocab makes buildTags fall
	// back to RAKE order, which matches pre-PR-4 behaviour.
	vocab, vocabErr := d.lib.AllTagNames()
	if vocabErr != nil {
		fmt.Fprintf(os.Stderr, "drafter: AllTagNames: %v\n", vocabErr)
	}

	chunks := detectChunks(turns, d.boundaryThreshold)
	for _, ch := range chunks {
		d.persistChunk(bus, sessionID, turns[ch.StartIdx:ch.EndIdx], vocab)
	}
}

func (d *Drafter) persistChunk(bus *herald.Bus, sessionID string, turns []bufferedTurn, vocab []string) {
	if len(turns) == 0 {
		return
	}
	var texts, tagSrc []string
	var allPaths []string
	var allKeywords []RakeCandidate
	redactedAny := false
	for _, t := range turns {
		texts = append(texts, t.text)
		cleaned := sanitizeForTags(t.text)
		tagSrc = append(tagSrc, cleaned)
		allPaths = append(allPaths, extractPaths(t.text)...)
		allKeywords = append(allKeywords, RAKE(cleaned, 5)...)
		if len(t.redacted) > 0 {
			redactedAny = true
		}
	}
	content := strings.Join(texts, "\n")
	tagContent := strings.Join(tagSrc, "\n")

	tags := buildTags(allPaths, tagContent, allKeywords, vocab)

	contentBytes, err := json.Marshal(map[string]any{"text": content})
	if err != nil {
		fmt.Fprintf(os.Stderr, "drafter: marshal chunk content: %v\n", err)
		return
	}

	last := turns[len(turns)-1]
	actor := last.harness
	if actor == "" {
		actor = "watcher"
	}
	id, err := d.lib.InsertMemoryWithTags(librarian.InsertMemory{
		Content:                string(contentBytes),
		SessionID:              sessionID,
		ProvenanceActor:        actor,
		ProvenanceSourceType:   "transcript-extraction",
		ProvenanceTriggerEvent: "watcher",
		CreatedAt:              last.timestamp,
	}, tags)
	if err != nil {
		fmt.Fprintf(os.Stderr, "drafter: persist chunk (session=%s): %v\n", sessionID, err)
		return
	}

	opType := herald.OpMemoryCreated
	if redactedAny {
		opType = herald.OpMemoryRedacted
	}
	bus.Publish(herald.Event{
		Type:      opType,
		SessionID: sessionID,
		Payload: map[string]any{
			"memory_id": id,
			"harness":   last.harness,
			"provider":  last.provider,
			"model":     last.model,
			"turns":     len(turns),
			"tags":      tags,
		},
	})
}

// detectChunks adapts the buffered turns to boundary.Turn shape and
// runs DetectBoundaries.
func detectChunks(turns []bufferedTurn, threshold float64) []Chunk {
	if len(turns) == 0 {
		return nil
	}
	parsed := make([]Turn, len(turns))
	for i, t := range turns {
		rake := RAKE(t.text, 10)
		kw := make([]string, 0, len(rake))
		for _, c := range rake {
			kw = append(kw, c.Phrase)
		}
		parsed[i] = Turn{
			Text:      t.text,
			FilePaths: extractPaths(t.text),
			Keywords:  kw,
		}
	}
	return DetectBoundaries(parsed, threshold)
}

// processRecord persists a memory.record event verbatim — bypasses
// filters, buffering, and clustering. Atomic memory + tags via
// InsertMemoryWithTags. Mirrors PR 2's contract.
func (d *Drafter) processRecord(bus *herald.Bus, e herald.Event) error {
	rawContent, _ := e.Payload["content"].(map[string]any)
	if len(rawContent) == 0 {
		return fmt.Errorf("memory.record event has empty content")
	}
	contentBytes, err := json.Marshal(rawContent)
	if err != nil {
		return fmt.Errorf("marshal content: %w", err)
	}
	summary, _ := e.Payload["summary"].(string)
	actor, _ := e.Payload["provenance_actor"].(string)
	if actor == "" {
		actor = "mcp"
	}
	source, _ := e.Payload["provenance_source_type"].(string)
	if source == "" {
		source = "manual-draft"
	}
	trigger, _ := e.Payload["provenance_trigger_event"].(string)
	if trigger == "" {
		trigger = "record"
	}
	tags := tagsFromPayload(e.Payload["tags"])
	id, err := d.lib.InsertMemoryWithTags(librarian.InsertMemory{
		Content:                string(contentBytes),
		Summary:                summary,
		SessionID:              e.SessionID,
		ProvenanceActor:        actor,
		ProvenanceSourceType:   source,
		ProvenanceTriggerEvent: trigger,
	}, tags)
	if err != nil {
		return fmt.Errorf("insert memory with tags: %w", err)
	}
	bus.Publish(herald.Event{
		Type:      herald.OpMemoryCreated,
		SessionID: e.SessionID,
		Payload: map[string]any{
			"memory_id": id,
			"trigger":   trigger,
			"actor":     actor,
		},
	})
	return nil
}

// buildTags runs the v1 chunk-grain tag pipeline: union of
// path-derived, identifier-derived, and RAKE candidates; split
// compounds; POS-noun filter; cap at maxTagsPerMemory; final
// normalization through librarian.NormalizeTagName for parity with
// mom_record's input pipeline.
//
// vocab is the existing tag corpus (Librarian.AllTagNames()). BM25
// uses it to rank RAKE candidates — phrases already saturated as tags
// score lower, novel phrases score higher. Empty vocab is fine; BM25
// returns input order unchanged in that case.
func buildTags(paths []string, tagContent string, candidates []RakeCandidate, vocab []string) []string {
	tags := map[string]bool{}
	for _, t := range ExtractFileTags(paths) {
		tags[t] = true
	}
	for _, t := range ExtractIdentifiers(tagContent) {
		if len(t) > 2 && len(t) <= 40 {
			tags[t] = true
		}
	}
	bm := newBM25Index(vocab)
	for _, t := range bm.rankCandidates(candidates) {
		if len(t) > 2 && len(t) <= 40 {
			tags[t] = true
		}
	}
	wordSet := map[string]bool{}
	for t := range tags {
		t = strings.ReplaceAll(t, "_", "-")
		for _, p := range strings.Split(t, "-") {
			p = strings.TrimSpace(p)
			if len(p) > 2 && isCleanTag(p) {
				wordSet[p] = true
			}
		}
	}
	var out []string
	posText := strings.Join(mapKeys(wordSet), " ")
	if doc, perr := prose.NewDocument(posText); perr == nil {
		for _, tok := range doc.Tokens() {
			if strings.HasPrefix(tok.Tag, "NN") || tok.Tag == "FW" {
				w := strings.ToLower(tok.Text)
				if len(w) > 2 {
					out = append(out, w)
				}
			}
		}
	} else {
		for w := range wordSet {
			out = append(out, w)
		}
	}
	sort.Strings(out)
	out = dedup(out)
	if len(out) > maxTagsPerMemory {
		out = out[:maxTagsPerMemory]
	}

	final := make([]string, 0, len(out))
	seen := map[string]bool{}
	for _, t := range out {
		n := librarian.NormalizeTagName(t)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		final = append(final, n)
	}
	if len(final) == 0 {
		final = []string{"untagged"}
	}
	return final
}

// extractPaths finds tokens that look like file paths. Used only for
// tag extraction; not persisted.
func extractPaths(text string) []string {
	var paths []string
	for _, word := range strings.Fields(text) {
		if strings.Contains(word, "/") && strings.Contains(word, ".") {
			clean := strings.Trim(word, "\"'`(),;:")
			paths = append(paths, clean)
		}
	}
	return paths
}

// ── tokenisation helpers shared with rake.go / bm25.go ──────────────

// tokenize splits text into lowercase words, collapsing punctuation.
func tokenize(text string) []string {
	text = stripAccents(text)
	return strings.Fields(strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == ' ' {
			return r
		}
		return ' '
	}, text))
}

func stripAccents(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range norm.NFD.String(s) {
		if !unicode.Is(unicode.Mn, r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func splitAtStopwords(words []string) [][]string {
	var result [][]string
	var current []string
	for _, w := range words {
		if isStopword(w) {
			if len(current) > 0 {
				result = append(result, current)
				current = nil
			}
		} else {
			current = append(current, w)
		}
	}
	if len(current) > 0 {
		result = append(result, current)
	}
	var trimmed [][]string
	for _, phrase := range result {
		for len(phrase) > maxPhraseWords {
			trimmed = append(trimmed, phrase[:maxPhraseWords])
			phrase = phrase[maxPhraseWords:]
		}
		if len(phrase) > 0 {
			trimmed = append(trimmed, phrase)
		}
	}
	return trimmed
}

func sortCandidates(candidates []RakeCandidate) {
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Score > candidates[j].Score
	})
}

// isCleanTag rejects markdown fragments, URL pieces, and other
// non-word noise. Permissive on purpose — it is the last line of
// defence before the POS filter.
func isCleanTag(s string) bool {
	for _, r := range s {
		switch r {
		case '`', '*', '#', '[', ']', '(', ')', '<', '>', '{', '}':
			return false
		}
	}
	if strings.Contains(s, "https") || strings.Contains(s, "http") {
		return false
	}
	if strings.HasPrefix(s, ".") {
		return false
	}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			return true
		}
	}
	return false
}

func mapKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func dedup(sorted []string) []string {
	if len(sorted) == 0 {
		return sorted
	}
	out := []string{sorted[0]}
	for _, s := range sorted[1:] {
		if s != out[len(out)-1] {
			out = append(out, s)
		}
	}
	return out
}

// ── payload extraction (carried over from worker.go) ─────────────

type payloadToolCall struct {
	Name     string
	Input    map[string]any
	Category string
}

func tcsFromPayload(v any) []payloadToolCall {
	switch tcs := v.(type) {
	case []map[string]any:
		out := make([]payloadToolCall, 0, len(tcs))
		for _, tc := range tcs {
			out = append(out, payloadToolCallFromMap(tc))
		}
		return out
	case []any:
		out := make([]payloadToolCall, 0, len(tcs))
		for _, item := range tcs {
			if tc, ok := item.(map[string]any); ok {
				out = append(out, payloadToolCallFromMap(tc))
			}
		}
		return out
	}
	return nil
}

func payloadToolCallFromMap(m map[string]any) payloadToolCall {
	name, _ := m["name"].(string)
	cat, _ := m["category"].(string)
	input, _ := m["input"].(map[string]any)
	return payloadToolCall{Name: name, Input: input, Category: cat}
}

func countCategory(tcs []payloadToolCall, cat string) int {
	n := 0
	for _, tc := range tcs {
		if tc.Category == cat {
			n++
		}
	}
	return n
}

func tagsFromPayload(v any) []string {
	switch s := v.(type) {
	case []string:
		return s
	case []any:
		out := make([]string, 0, len(s))
		for _, x := range s {
			if str, ok := x.(string); ok && str != "" {
				out = append(out, str)
			}
		}
		return out
	}
	return nil
}

func extractCreatedAt(payload map[string]any) time.Time {
	if v, ok := payload["created_at"]; ok {
		if t, ok := v.(time.Time); ok {
			return t
		}
	}
	return time.Time{}
}

func categoriesSlice(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for c := range set {
		out = append(out, c)
	}
	return out
}
