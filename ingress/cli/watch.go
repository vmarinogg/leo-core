package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/momhq/mom/events/editor"
	"github.com/momhq/mom/storage/canonical"

	"github.com/fsnotify/fsnotify"
	"github.com/momhq/mom/bus/herald"
	"github.com/momhq/mom/ingress/watcher"
	"github.com/momhq/mom/ops/daemon"
	"github.com/momhq/mom/shared/config"
	"github.com/momhq/mom/shared/ux"
	"github.com/momhq/mom/workers/drafter"
	"github.com/momhq/mom/workers/logbook"
	"github.com/spf13/cobra"
)

var (
	watchStatus bool
	watchSweep  bool
	watchGlobal bool
)

var watchCmd = &cobra.Command{
	Use:   "watch",
	Short: "Watch harness transcripts and ingest turns automatically",
	Long: `Starts a filesystem watcher on a harness transcript directory and
ingests new conversation turns into the central vault at $HOME/.mom/mom.db
without MCP calls or hook overhead.

Supported harnesses:
  claude    — ~/.claude/projects/ (default)
  codex     — ~/.codex/sessions/ (or $CODEX_HOME/sessions/)
  pi        — ~/.pi/agent/sessions/

Each session's JSONL transcript is tailed incrementally.
Cursor files in .mom/cache/ track the last ingested byte offset per session,
so restarts are safe and idempotent.

The watcher runs in the foreground. Use Ctrl-C to stop.`,
	Args:          cobra.NoArgs,
	RunE:          runWatch,
	SilenceUsage:  true,
	SilenceErrors: false,
}

func init() {
	watchCmd.Flags().BoolVar(&watchStatus, "status", false,
		"Show watch cursors and ingested sessions, then exit")
	watchCmd.Flags().BoolVar(&watchSweep, "sweep", false,
		"One-shot mode: catch up on unprocessed transcripts and exit")
	watchCmd.Flags().BoolVar(&watchGlobal, "global", false,
		"Run as a single global daemon watching all registered projects")
	_ = watchCmd.Flags().MarkHidden("global")
}

func runWatch(cmd *cobra.Command, _ []string) error {
	// Global mode doesn't need a project-local .mom/ — handle it first.
	if watchGlobal {
		return runWatchGlobal(watchSweep)
	}

	cwd, _ := os.Getwd()
	if envDir := os.Getenv("MOM_PROJECT_DIR"); envDir != "" {
		cwd = envDir
	}
	projectDir, momDir, err := resolveMomContext(cwd)
	if err != nil {
		return err
	}

	if watchStatus {
		return runWatchStatus(momDir)
	}

	p := ux.NewPrinter(os.Stderr)

	// Open the central vault once for this watch process. Worker is
	// shared across the per-project buses below.
	workers := openCentralWorkers()

	// Config-driven multi-harness mode. Manual per-harness overrides were kept
	// out of the v1 public CLI; init/upgrade own harness configuration.
	momCfg, err := config.Load(momDir)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	sources := buildWatcherSources(momCfg, projectDir)
	if len(sources) == 0 {
		return fmt.Errorf("no watcher-capable harnesses enabled in config")
	}

	// Sweep mode: one-shot catch-up and exit.
	if watchSweep {
		bus := newProjectBus(workers)
		w, err := watcher.New(watcher.Config{
			ProjectDir: projectDir,
			MomDir:     momDir,
			Sources:    sources,
			SweepOnly:  true,
			Bus:        bus,
			Editor:     editor.New(bus, nil, nil),
		})
		if err != nil {
			return fmt.Errorf("creating watcher: %w", err)
		}
		sessions, turns := w.Sweep()
		if sessions > 0 {
			p.Checkf("sweep: %s sessions, %s turns",
				p.HighlightValue(fmt.Sprintf("%d", sessions)),
				p.HighlightValue(fmt.Sprintf("%d", turns)))
		} else {
			p.Muted("sweep: nothing new")
		}
		return nil
	}

	// Herald event bus: watcher publishes turn.observed events,
	// Logbook and Drafter (via centralWorkers) subscribe as
	// downstream processors.
	bus := newProjectBus(workers)

	w, err := watcher.New(watcher.Config{
		ProjectDir: projectDir,
		MomDir:     momDir,
		Sources:    sources,
		DebounceMs: 300,
		Bus:        bus,
		Editor:     editor.New(bus, nil, nil),
	})
	if err != nil {
		return fmt.Errorf("creating watcher: %w", err)
	}

	// Print startup info.
	harnessNames := make([]string, len(sources))
	for i, src := range sources {
		harnessNames[i] = src.Harness
	}
	p.Diamond(fmt.Sprintf("watch [%s]", strings.Join(harnessNames, ", ")))
	for rt, dir := range w.TranscriptDirs() {
		p.Chevron(fmt.Sprintf("%s: %s", rt, dir))
	}
	if path, err := canonical.Path(); err == nil {
		p.Chevron(fmt.Sprintf("vault: %s", path))
	}
	p.Muted("press Ctrl-C to stop")
	p.Blank()

	// Start the Drafter idle-flush ticker and ensure FlushAll runs
	// when the watcher exits. Without these, sessions under
	// flushAtTurnCount turns never persist; clean shutdown loses
	// every pending buffer.
	tickCtx, tickCancel := context.WithCancel(context.Background())
	tickDone := startDrafterTicker(tickCtx, workers)
	defer func() {
		tickCancel()
		<-tickDone
		if workers.drafter != nil {
			workers.drafter.FlushAll()
		}
	}()

	if err := w.Run(); err != nil {
		return fmt.Errorf("watcher stopped: %w", err)
	}
	return nil
}

// drafterTickInterval is the cadence at which the global daemon
// invokes Drafter.Tick. Sessions whose lastSeen is older than the
// drafter's idleFlushAfter (default 90s) are flushed at the next
// tick — so the worst-case persistence delay is
// idleFlushAfter + drafterTickInterval. 30s keeps that sub-2-minute.
const drafterTickInterval = 30 * time.Second

// startDrafterTicker spawns a goroutine that calls drafter.Tick on a
// fixed cadence until ctx is done. Returns a channel that closes
// when the goroutine has exited so callers can sequence FlushAll
// after Tick has stopped.
func startDrafterTicker(ctx context.Context, workers centralWorkers) <-chan struct{} {
	done := make(chan struct{})
	if workers.drafter == nil {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		t := time.NewTicker(drafterTickInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-t.C:
				workers.drafter.Tick(now)
			}
		}
	}()
	return done
}

// newProjectBus creates a Herald event bus with the central Drafter
// and Logbook attached. Used by both single-project and global watch
// modes. The shared workers consume turn.observed, memory.record,
// and op.memory.* and persist into the central vault at
// $HOME/.mom/mom.db. The legacy RecordAppended subscribers (v1
// drafter writing .mom/memory/*.json, v1 logbook writing
// session-*.json) retired in #240 PR 3 and PR 4.
func newProjectBus(workers centralWorkers) *herald.Bus {
	bus := herald.NewBus()
	workers.AttachToBus(bus)
	return bus
}

// runWatchGlobal runs the global watch daemon: watches all registered projects.
func runWatchGlobal(sweepOnly bool) error {
	if _, err := daemon.PruneInvalidRegistry(); err != nil {
		return fmt.Errorf("pruning registry: %w", err)
	}
	reg, err := daemon.LoadRegistry()
	if err != nil {
		return fmt.Errorf("loading registry: %w", err)
	}

	// Open the central vault ONCE for the entire global daemon. The
	// same Logbook worker is shared across every per-project bus
	// below — no N-vault-handle leak in multi-project mode.
	workers := openCentralWorkers()

	if sweepOnly {
		p := ux.NewPrinter(os.Stderr)
		totalSessions, totalTurns := 0, 0
		// Sweep all registered projects and exit.
		for projDir, entry := range reg {
			cfg, err := config.Load(entry.MomDir)
			if err != nil {
				p.Warn(fmt.Sprintf("sweep %s: config: %v", projDir, err))
				continue
			}
			sources := buildWatcherSources(cfg, projDir)
			if len(sources) == 0 {
				continue
			}
			bus := newProjectBus(workers)
			w, err := watcher.New(watcher.Config{
				ProjectDir: projDir,
				MomDir:     entry.MomDir,
				Sources:    sources,
				SweepOnly:  true,
				Bus:        bus,
				Editor:     editor.New(bus, nil, nil),
			})
			if err != nil {
				p.Warn(fmt.Sprintf("sweep %s: %v", projDir, err))
				continue
			}
			sessions, turns := w.Sweep()
			totalSessions += sessions
			totalTurns += turns
			if sessions > 0 {
				p.Checkf("sweep %s: %s sessions, %s turns",
					filepath.Base(projDir),
					p.HighlightValue(fmt.Sprintf("%d", sessions)),
					p.HighlightValue(fmt.Sprintf("%d", turns)))
			}
		}
		if totalSessions == 0 {
			p.Muted("sweep: nothing new across all projects")
		}
		return nil
	}

	// Persistent watch mode: one watcher per registered project.
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Drafter idle-flush ticker + shutdown FlushAll. Without these,
	// sessions under flushAtTurnCount turns never persist and clean
	// shutdown loses every pending buffer.
	tickDone := startDrafterTicker(ctx, workers)
	defer func() {
		<-tickDone
		if workers.drafter != nil {
			workers.drafter.FlushAll()
		}
	}()

	type runningWatcher struct {
		cancel context.CancelFunc
	}
	var mu sync.Mutex
	watchers := make(map[string]*runningWatcher)

	startProject := func(projDir string, entry daemon.RegistryEntry) {
		cfg, err := config.Load(entry.MomDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[mom] watch %s: config: %v\n", projDir, err)
			return
		}
		sources := buildWatcherSources(cfg, projDir)
		if len(sources) == 0 {
			return
		}
		bus := newProjectBus(workers)
		w, err := watcher.New(watcher.Config{
			ProjectDir: projDir,
			MomDir:     entry.MomDir,
			Sources:    sources,
			DebounceMs: 300,
			Bus:        bus,
			Editor:     editor.New(bus, nil, nil),
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "[mom] watch %s: %v\n", projDir, err)
			return
		}

		wCtx, wCancel := context.WithCancel(ctx)
		mu.Lock()
		watchers[projDir] = &runningWatcher{cancel: wCancel}
		mu.Unlock()

		go func() {
			if err := w.Run(); err != nil {
				fmt.Fprintf(os.Stderr, "[mom] watch %s stopped: %v\n", projDir, err)
			}
		}()

		go func() {
			<-wCtx.Done()
			w.Stop() //nolint:errcheck
		}()
	}

	// Start watchers for all currently registered projects.
	for projDir, entry := range reg {
		startProject(projDir, entry)
	}

	fmt.Fprintf(os.Stderr, "[mom] global daemon: watching %d projects\n", len(reg))

	// Watch the registry file for changes (add/remove projects).
	regPath, err := daemon.RegistryPath()
	if err != nil {
		return fmt.Errorf("registry path: %w", err)
	}
	regDir := filepath.Dir(regPath)

	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("fsnotify watcher: %w", err)
	}
	defer fw.Close()

	if err := fw.Add(regDir); err != nil {
		return fmt.Errorf("watching registry dir: %w", err)
	}

	for {
		select {
		case <-ctx.Done():
			mu.Lock()
			for _, rw := range watchers {
				rw.cancel()
			}
			mu.Unlock()
			return nil

		case ev, ok := <-fw.Events:
			if !ok {
				return nil
			}
			if filepath.Base(ev.Name) != "watch-registry.json" {
				continue
			}
			if ev.Op&(fsnotify.Write|fsnotify.Create) == 0 {
				continue
			}

			newReg, err := daemon.LoadRegistry()
			if err != nil {
				fmt.Fprintf(os.Stderr, "[mom] reload registry: %v\n", err)
				continue
			}

			mu.Lock()
			// Stop watchers for removed projects.
			for projDir, rw := range watchers {
				if _, exists := newReg[projDir]; !exists {
					rw.cancel()
					delete(watchers, projDir)
					fmt.Fprintf(os.Stderr, "[mom] unregistered: %s\n", projDir)
				}
			}
			// Start watchers for new projects.
			for projDir, entry := range newReg {
				if _, exists := watchers[projDir]; !exists {
					startProject(projDir, entry)
					fmt.Fprintf(os.Stderr, "[mom] registered: %s\n", projDir)
				}
			}
			mu.Unlock()

		case err, ok := <-fw.Errors:
			if !ok {
				return nil
			}
			fmt.Fprintf(os.Stderr, "[mom] fsnotify error: %v\n", err)
		}
	}
}

// runWatchStatus prints watcher cursor files for inspection.
func runWatchStatus(momDir string) error {
	p := ux.NewPrinter(os.Stderr)
	cursorDir := filepath.Join(momDir, "cache")
	entries, err := os.ReadDir(cursorDir)
	if err != nil {
		if os.IsNotExist(err) {
			p.Warn(fmt.Sprintf("no cache dir at %s — watcher has not run yet", cursorDir))
			return nil
		}
		return fmt.Errorf("reading cache dir: %w", err)
	}

	type cursor struct {
		sid    string
		offset string
	}
	var cursors []cursor
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasPrefix(e.Name(), ".watch-cursor-") {
			sid := strings.TrimPrefix(e.Name(), ".watch-cursor-")
			cf := filepath.Join(cursorDir, e.Name())
			data, err := os.ReadFile(cf)
			if err != nil {
				continue
			}
			cursors = append(cursors, cursor{sid: sid, offset: strings.TrimSpace(string(data))})
		}
	}

	if len(cursors) == 0 {
		p.Warn("no watch cursors found — watcher has not run yet")
		return nil
	}

	p.Diamond("watch cursors")
	p.Muted(fmt.Sprintf("%d sessions", len(cursors)))
	p.Blank()
	for _, c := range cursors {
		p.Chevron(fmt.Sprintf("%s: %s bytes", c.sid, c.offset))
	}
	return nil
}

// centralWorkers bundles the two Herald subscribers that need a
// Librarian: Drafter (filter pipeline + memory persistence) and
// Logbook (operational stream). Returned together because they share
// the same Vault — we open the vault once per process and use it for
// both.
type centralWorkers struct {
	drafter *drafter.Drafter
	logbook *logbook.Worker
}

// AttachToBus subscribes both workers to the given bus with the
// correct topic set:
//
//   - Drafter consumes turn.observed and memory.record (write path)
//   - Logbook consumes turn.observed (privacy-projected audit) AND
//     op.memory.created / op.memory.redacted / op.memory.dropped
//     (Drafter's outcome events, persisted as audit rows)
//
// No-op when the workers are nil — openCentralWorkers returns a zero
// value when vault.Open fails. The bus continues to function for
// legacy v1 subscribers in that case.
//
// Encapsulating both subscriptions here is the single place a future
// "what does Logbook record for this bus?" change needs to land.
func (cw centralWorkers) AttachToBus(bus *herald.Bus) {
	if cw.drafter != nil {
		cw.drafter.SubscribeAll(bus)
	}
	if cw.logbook != nil {
		cw.logbook.SubscribeTurnObserved(bus)
		cw.logbook.SubscribeAll(bus,
			herald.OpMemoryCreated,
			herald.OpMemoryRedacted,
			herald.OpMemoryDropped,
		)
	}
}

// openCentralWorkers opens the central vault at $HOME/.mom/mom.db,
// runs migrations, and constructs the workers bound to it. Returns
// zero values + logs to stderr on any failure (HOME resolution,
// MkdirAll, vault.Open) — callers can still use the bus for legacy
// subscribers.
//
// Called once per process, NOT per project. The same workers are
// subscribed to every project's bus by newProjectBus; SQLite WAL +
// the librarian/vault concurrency contract keep this safe across
// goroutines.
//
// The vault stays open for the process's lifetime. The harness owns
// the lifecycle; on shutdown the OS reclaims the handle. A future
// refactor should plumb an explicit Close, but for alpha this is
// acceptable.
func openCentralWorkers() centralWorkers {
	lib, _, err := canonical.OpenLibrarian()
	if err != nil {
		fmt.Fprintf(os.Stderr, "watch: %v — central workers not wired\n", err)
		return centralWorkers{}
	}
	return centralWorkers{
		drafter: drafter.New(lib),
		logbook: logbook.New(lib),
	}
}
