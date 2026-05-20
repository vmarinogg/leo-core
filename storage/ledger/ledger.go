// Package ledger implements MOM's Layer 1 immutable canonical event
// log (ADR 0021). The Ledger is an append-only directory of segment
// files at $HOME/.mom/ledger/. Crier (ADR 0022) reads from it and
// projects events into the Vault via Librarian.
//
// Storage shape:
//
//	$HOME/.mom/ledger/
//	├── 00000000000000000000.seg     ← length-prefixed JSON records
//	├── 00000000000000000001.seg
//	└── current.txt                   ← name of the segment currently
//	                                    receiving appends
//
// Each segment starts with an 8-byte magic + version header. Each
// record is a 4-byte big-endian length prefix followed by the JSON
// envelope. Append + fsync happen-before the offset is returned.
// Partial writes (process killed mid-flush) are detected on next
// open by length-prefix-vs-tail mismatch and truncated to the last
// good record.
//
// API surface is small and intentional:
//
//	Open(dir) (*Ledger, error)   open or create the ledger at dir
//	Append(event) (offset, err)  append + fsync, return durable offset
//	Read(offset) (Record, err)   read a single record by offset
//	Iterate(from) Iter           sequential iterator from offset
//	Close() error                close the active segment
//
// There is no Update, Delete, or Truncate. Segment-level retention is
// a v0.60 concern (Gardener). The driver opens files with O_APPEND
// only — verified by archtest in storage/ledger/ledger_arch_test.go.
package ledger

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/momhq/mom/bus/herald"
)

// SegmentRotationBytes is the maximum size of a single segment file
// before rotation. Default 64 MiB.
const SegmentRotationBytes int64 = 64 * 1024 * 1024

// Magic identifies the file format. The two bytes after Magic are the
// format version (uint16 BE). Future format changes bump the version.
var Magic = [6]byte{'M', 'O', 'M', 'L', 'E', 'D'}

// FormatVersion is the current segment format. Older readers refuse
// to load segments with a different version.
const FormatVersion uint16 = 1

// segmentHeaderSize is Magic(6) + Version(2) = 8 bytes.
const segmentHeaderSize = 8

// recordPrefixSize is the length-prefix on every record (uint32 BE).
const recordPrefixSize = 4

// Record is one canonical event durably stored in the Ledger.
//
// Offset is monotonically increasing across the lifetime of the
// Ledger. Consumers (Crier) checkpoint by Offset.
//
// Event is the canonical herald.Event from the Editor (ADR 0020).
// AppendedAt is the wall-clock time the Ledger wrote the record.
type Record struct {
	ID         string         `json:"id"`
	Offset     uint64         `json:"offset"`
	AppendedAt time.Time      `json:"appended_at"`
	Event      herald.Event   `json:"event"`
}

// Ledger is a handle on the on-disk segment directory. Open returns
// a *Ledger; callers Append, Read, or Iterate through it. Safe for
// concurrent Append from one process (other writers will see a stale
// view); cross-process concurrent writes are not supported.
type Ledger struct {
	dir      string
	mu       sync.Mutex
	active   *segment      // currently open for appending
	segments []*segmentInfo // all segments ordered by start offset
	nextID   uint64         // next offset to assign
}

type segment struct {
	path   string
	file   *os.File
	size   int64  // current size including header
	start  uint64 // offset of the FIRST record in this segment
}

type segmentInfo struct {
	path  string
	start uint64
	end   uint64 // last offset in this segment (inclusive); 0 if empty
}

// Open opens (or creates) the Ledger rooted at dir. Idempotent —
// repeated calls return distinct *Ledger handles but operate on the
// same underlying segment directory.
func Open(dir string) (*Ledger, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("ledger: mkdir %s: %w", dir, err)
	}
	l := &Ledger{dir: dir}
	if err := l.loadSegments(); err != nil {
		return nil, err
	}
	if err := l.openOrCreateActive(); err != nil {
		return nil, err
	}
	return l, nil
}

// Close flushes and closes the active segment. Safe to call multiple
// times.
func (l *Ledger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.active == nil || l.active.file == nil {
		return nil
	}
	err := l.active.file.Close()
	l.active.file = nil
	return err
}

// Append writes one canonical event to the Ledger. Append blocks
// until the record is fsync'd; the returned offset is durable.
//
// Returns (offset, nil) on success. Append never partial-publishes:
// a failure leaves the on-disk segment consistent (last good record
// or empty), and the next Append assigns the same offset.
func (l *Ledger) Append(e herald.Event) (uint64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.active == nil || l.active.file == nil {
		return 0, errors.New("ledger: closed")
	}
	offset := l.nextID
	rec := Record{
		ID:         newRecordID(offset),
		Offset:     offset,
		AppendedAt: time.Now().UTC(),
		Event:      e,
	}
	body, err := json.Marshal(rec)
	if err != nil {
		return 0, fmt.Errorf("ledger: marshal: %w", err)
	}
	if len(body) > int(SegmentRotationBytes/2) {
		return 0, fmt.Errorf("ledger: record too large (%d bytes); refusing append", len(body))
	}

	// Rotate before append if the new record would exceed the segment
	// rotation threshold. The check is conservative: we rotate when
	// the segment is already past the limit, OR when this single
	// record would push it past.
	needed := int64(recordPrefixSize + len(body))
	if l.active.size+needed > SegmentRotationBytes && l.active.size > segmentHeaderSize {
		if err := l.rotateLocked(); err != nil {
			return 0, err
		}
	}

	// Write length-prefix + body. Single O_APPEND write keeps the
	// op atomic at the OS level for small writes; we fsync to make
	// the durability promise.
	buf := make([]byte, recordPrefixSize+len(body))
	binary.BigEndian.PutUint32(buf[:recordPrefixSize], uint32(len(body)))
	copy(buf[recordPrefixSize:], body)
	n, err := l.active.file.Write(buf)
	if err != nil {
		// Partial write: best-effort truncate back to the previous
		// known-good size. If truncate fails, return both errors.
		if trunc := l.active.file.Truncate(l.active.size); trunc != nil {
			return 0, fmt.Errorf("ledger: write %s: %w (truncate also failed: %v)", l.active.path, err, trunc)
		}
		return 0, fmt.Errorf("ledger: write %s: %w", l.active.path, err)
	}
	if int64(n) != needed {
		// Short write — restore.
		_ = l.active.file.Truncate(l.active.size)
		return 0, fmt.Errorf("ledger: short write %d/%d to %s", n, needed, l.active.path)
	}
	if err := l.active.file.Sync(); err != nil {
		return 0, fmt.Errorf("ledger: fsync %s: %w", l.active.path, err)
	}
	l.active.size += needed
	l.nextID = offset + 1
	// Track end offset on the active segment.
	if len(l.segments) > 0 {
		l.segments[len(l.segments)-1].end = offset
	}
	return offset, nil
}

// Read returns the record at offset, if present. Returns os.ErrNotExist
// when the offset is beyond the head of the ledger.
func (l *Ledger) Read(offset uint64) (Record, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	seg := l.findSegmentForOffset(offset)
	if seg == nil {
		return Record{}, fmt.Errorf("ledger: offset %d: %w", offset, os.ErrNotExist)
	}
	return readRecordAtOffset(seg.path, offset)
}

// Iterate returns an iterator that yields records starting at from,
// in ledger order. The iterator reads segments in sequence; closed
// segments are read-once, the active segment is read to its tail.
//
// The caller is responsible for calling Iter.Close when done.
type Iter struct {
	l         *Ledger
	segments  []*segmentInfo
	current   *os.File
	pendingOf uint64 // next offset the iterator expects
	err       error
}

// Iterate begins streaming from offset 'from'.
func (l *Ledger) Iterate(from uint64) *Iter {
	l.mu.Lock()
	defer l.mu.Unlock()
	segs := make([]*segmentInfo, 0, len(l.segments))
	for _, s := range l.segments {
		if s.end >= from || s.start >= from {
			segs = append(segs, s)
		}
	}
	return &Iter{l: l, segments: segs, pendingOf: from}
}

// Next reads the next record, returning (rec, true) on success or
// (zero, false) at end of stream. Inspect Err after Next returns false
// to distinguish clean EOF from an error.
func (it *Iter) Next() (Record, bool) {
	for {
		if it.current == nil {
			if len(it.segments) == 0 {
				return Record{}, false
			}
			head := it.segments[0]
			it.segments = it.segments[1:]
			f, err := os.Open(head.path)
			if err != nil {
				it.err = err
				return Record{}, false
			}
			if _, err := f.Seek(segmentHeaderSize, io.SeekStart); err != nil {
				it.err = err
				f.Close()
				return Record{}, false
			}
			it.current = f
		}
		rec, err := readNextRecord(it.current)
		if err == io.EOF {
			it.current.Close()
			it.current = nil
			continue
		}
		if err != nil {
			it.err = err
			it.current.Close()
			it.current = nil
			return Record{}, false
		}
		if rec.Offset < it.pendingOf {
			continue
		}
		return rec, true
	}
}

// Err returns any iteration error after Next has reported false.
func (it *Iter) Err() error { return it.err }

// Close releases resources held by the iterator.
func (it *Iter) Close() error {
	if it.current != nil {
		err := it.current.Close()
		it.current = nil
		return err
	}
	return nil
}

// --- internals ---

func (l *Ledger) loadSegments() error {
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		return fmt.Errorf("ledger: read dir %s: %w", l.dir, err)
	}
	var segs []*segmentInfo
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".seg") {
			continue
		}
		stem := strings.TrimSuffix(name, ".seg")
		startOffset, err := strconv.ParseUint(stem, 10, 64)
		if err != nil {
			return fmt.Errorf("ledger: bad segment filename %q: %w", name, err)
		}
		segs = append(segs, &segmentInfo{
			path:  filepath.Join(l.dir, name),
			start: startOffset,
		})
	}
	sort.Slice(segs, func(i, j int) bool { return segs[i].start < segs[j].start })

	// Compute end offsets + nextID by walking each segment once. Only
	// the most recent segment's tail may be torn; readNextRecord
	// already handles partial writes by returning io.EOF and the
	// open path truncates the active segment to last good record.
	var maxOffset int64 = -1
	for _, s := range segs {
		end, err := scanSegmentTail(s.path)
		if err != nil {
			return fmt.Errorf("ledger: scan %s: %w", s.path, err)
		}
		s.end = end
		if int64(end) > maxOffset {
			maxOffset = int64(end)
		}
	}
	l.segments = segs
	if maxOffset < 0 {
		l.nextID = 0
	} else {
		l.nextID = uint64(maxOffset) + 1
	}
	return nil
}

func (l *Ledger) openOrCreateActive() error {
	if len(l.segments) == 0 {
		// Create the first segment at offset 0.
		path := filepath.Join(l.dir, segmentName(0))
		f, err := openSegmentForAppend(path, true)
		if err != nil {
			return err
		}
		info, err := f.Stat()
		if err != nil {
			f.Close()
			return err
		}
		l.active = &segment{path: path, file: f, size: info.Size(), start: 0}
		l.segments = append(l.segments, &segmentInfo{path: path, start: 0, end: 0})
		return nil
	}
	// Reuse the latest segment as active. Truncate any torn tail.
	last := l.segments[len(l.segments)-1]
	if err := truncateTornTail(last.path); err != nil {
		return err
	}
	f, err := openSegmentForAppend(last.path, false)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	l.active = &segment{path: last.path, file: f, size: info.Size(), start: last.start}
	return nil
}

func (l *Ledger) rotateLocked() error {
	if err := l.active.file.Close(); err != nil {
		return fmt.Errorf("ledger: close active: %w", err)
	}
	l.active.file = nil
	newPath := filepath.Join(l.dir, segmentName(l.nextID))
	f, err := openSegmentForAppend(newPath, true)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	l.active = &segment{path: newPath, file: f, size: info.Size(), start: l.nextID}
	l.segments = append(l.segments, &segmentInfo{path: newPath, start: l.nextID, end: 0})
	return nil
}

func (l *Ledger) findSegmentForOffset(offset uint64) *segmentInfo {
	for i := len(l.segments) - 1; i >= 0; i-- {
		if l.segments[i].start <= offset {
			return l.segments[i]
		}
	}
	return nil
}

// openSegmentForAppend opens path with O_APPEND only (no O_TRUNC, no
// O_WRONLY). If create is true, the file is created (with header) when
// it does not exist; otherwise an existing file is required.
//
// This is the ONLY place in the package that opens a segment for
// writing. An archtest verifies no other call site uses O_TRUNC, etc.
func openSegmentForAppend(path string, create bool) (*os.File, error) {
	flag := os.O_APPEND | os.O_RDWR
	if create {
		flag |= os.O_CREATE
	}
	f, err := os.OpenFile(path, flag, 0o600)
	if err != nil {
		return nil, fmt.Errorf("ledger: open %s: %w", path, err)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("ledger: stat %s: %w", path, err)
	}
	if info.Size() == 0 {
		// Brand-new segment — write the header.
		if err := writeHeader(f); err != nil {
			f.Close()
			return nil, err
		}
		if err := f.Sync(); err != nil {
			f.Close()
			return nil, fmt.Errorf("ledger: fsync header %s: %w", path, err)
		}
	} else if info.Size() < segmentHeaderSize {
		f.Close()
		return nil, fmt.Errorf("ledger: segment %s too small (%d bytes)", path, info.Size())
	} else {
		if err := validateHeader(path); err != nil {
			f.Close()
			return nil, err
		}
	}
	return f, nil
}

func writeHeader(f *os.File) error {
	hdr := make([]byte, segmentHeaderSize)
	copy(hdr, Magic[:])
	binary.BigEndian.PutUint16(hdr[6:8], FormatVersion)
	if _, err := f.Write(hdr); err != nil {
		return fmt.Errorf("ledger: write header: %w", err)
	}
	return nil
}

func validateHeader(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("ledger: open for header read %s: %w", path, err)
	}
	defer f.Close()
	hdr := make([]byte, segmentHeaderSize)
	if _, err := io.ReadFull(f, hdr); err != nil {
		return fmt.Errorf("ledger: read header %s: %w", path, err)
	}
	for i := range Magic {
		if hdr[i] != Magic[i] {
			return fmt.Errorf("ledger: bad magic in %s", path)
		}
	}
	ver := binary.BigEndian.Uint16(hdr[6:8])
	if ver != FormatVersion {
		return fmt.Errorf("ledger: unsupported segment version %d in %s (this binary speaks v%d)", ver, path, FormatVersion)
	}
	return nil
}

// truncateTornTail scans path forward from the header. The first
// length prefix that points past EOF, or whose body fails to read,
// is treated as a torn write — the file is truncated to the byte
// offset BEFORE that bad prefix.
func truncateTornTail(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("ledger: open for tail scan %s: %w", path, err)
	}
	defer f.Close()
	if _, err := f.Seek(segmentHeaderSize, io.SeekStart); err != nil {
		return err
	}
	lastGood := int64(segmentHeaderSize)
	for {
		prefix := make([]byte, recordPrefixSize)
		n, err := io.ReadFull(f, prefix)
		if err == io.EOF || (err == io.ErrUnexpectedEOF && n == 0) {
			break
		}
		if err != nil {
			// Partial prefix: truncate.
			break
		}
		recLen := binary.BigEndian.Uint32(prefix)
		body := make([]byte, recLen)
		if _, err := io.ReadFull(f, body); err != nil {
			// Partial body: truncate to lastGood.
			break
		}
		var rec Record
		if err := json.Unmarshal(body, &rec); err != nil {
			break
		}
		lastGood += int64(recordPrefixSize) + int64(recLen)
	}
	// Truncate to lastGood. Safe to no-op if file already that size.
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() != lastGood {
		if err := f.Truncate(lastGood); err != nil {
			return fmt.Errorf("ledger: truncate %s to %d: %w", path, lastGood, err)
		}
	}
	return nil
}

// scanSegmentTail walks every record in the segment file and returns
// the offset of the LAST good record (or 0 if the segment has no
// records yet).
func scanSegmentTail(path string) (uint64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	if _, err := f.Seek(segmentHeaderSize, io.SeekStart); err != nil {
		return 0, err
	}
	var lastOffset uint64
	for {
		rec, err := readNextRecord(f)
		if err == io.EOF {
			return lastOffset, nil
		}
		if err != nil {
			// Treat read errors as end-of-good-data; the active-segment
			// open path will handle torn-tail truncation.
			return lastOffset, nil
		}
		lastOffset = rec.Offset
	}
}

// readNextRecord reads one length-prefixed JSON record from f at its
// current position. Returns io.EOF at clean end of segment.
func readNextRecord(f *os.File) (Record, error) {
	prefix := make([]byte, recordPrefixSize)
	if _, err := io.ReadFull(f, prefix); err != nil {
		if err == io.ErrUnexpectedEOF {
			return Record{}, io.EOF
		}
		return Record{}, err
	}
	recLen := binary.BigEndian.Uint32(prefix)
	body := make([]byte, recLen)
	if _, err := io.ReadFull(f, body); err != nil {
		return Record{}, err
	}
	var rec Record
	if err := json.Unmarshal(body, &rec); err != nil {
		return Record{}, fmt.Errorf("ledger: parse record: %w", err)
	}
	return rec, nil
}

func readRecordAtOffset(path string, offset uint64) (Record, error) {
	f, err := os.Open(path)
	if err != nil {
		return Record{}, err
	}
	defer f.Close()
	if _, err := f.Seek(segmentHeaderSize, io.SeekStart); err != nil {
		return Record{}, err
	}
	for {
		rec, err := readNextRecord(f)
		if err == io.EOF {
			return Record{}, fmt.Errorf("ledger: offset %d: %w", offset, os.ErrNotExist)
		}
		if err != nil {
			return Record{}, err
		}
		if rec.Offset == offset {
			return rec, nil
		}
		if rec.Offset > offset {
			return Record{}, fmt.Errorf("ledger: offset %d not found (passed %d): %w", offset, rec.Offset, os.ErrNotExist)
		}
	}
}

func segmentName(startOffset uint64) string {
	return fmt.Sprintf("%020d.seg", startOffset)
}

func newRecordID(offset uint64) string {
	return fmt.Sprintf("led-%016x", offset)
}
