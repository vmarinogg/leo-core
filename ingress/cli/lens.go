package cli

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/momhq/mom/storage/canonical"

	"github.com/momhq/mom/services/lens"
	"github.com/momhq/mom/shared/ux"
	"github.com/spf13/cobra"
)

const defaultLensPort = 7474

var lensCmd = &cobra.Command{
	Use:   "lens",
	Short: "Open the session history dashboard in your browser",
	Long: `Launch a local web server and open the MOM sessions dashboard.

The dashboard shows sessions from the central MOM vault.

Press Ctrl+C to stop the server.`,
	RunE: runLens,
}

func init() {
	lensCmd.Flags().Int("port", defaultLensPort, "Port to listen on")
}

func runLens(cmd *cobra.Command, _ []string) error {
	port, _ := cmd.Flags().GetInt("port")
	portExplicit := cmd.Flags().Changed("port")

	lib, closeFn, err := canonical.OpenLibrarian()
	if err != nil {
		return fmt.Errorf("opening central vault: %w", err)
	}
	defer func() { _ = closeFn() }()

	srv, err := lens.New(lib)
	if err != nil {
		return fmt.Errorf("starting lens: %w", err)
	}

	// Default port: try up to 10 fallbacks. Explicit --port: fail loud if taken.
	fallbacks := 10
	if portExplicit {
		fallbacks = 0
	}
	// Loopback-only bind: lens has no authentication, and the dashboard
	// exposes the full session history. A non-loopback bind would put
	// that data on any network the host is connected to.
	ln, err := lens.ListenWithFallback("localhost", port, fallbacks)
	if err != nil {
		return fmt.Errorf("listening on port %d: %w", port, err)
	}
	actualPort := ln.Addr().(*net.TCPAddr).Port

	url := fmt.Sprintf("http://localhost:%d", actualPort)
	p := ux.NewPrinter(cmd.OutOrStdout())
	p.Checkf("mom lens → %s", url)
	p.KeyValue("  vault", "central", 8)
	p.Blank()
	p.Muted("  Ctrl+C to stop")

	_ = openBrowser(url)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	httpSrv := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	go func() {
		<-ctx.Done()
		_ = httpSrv.Close()
	}()

	if err := httpSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return err
	}

	p.Blank()
	p.Muted("lens closed.")
	return nil
}
