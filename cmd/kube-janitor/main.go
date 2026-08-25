package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dschaaff/kube-janitor/pkg/janitor"
)

var (
	version   = "dev"     // Will be set during build with -ldflags
	buildDate = "unknown" // Will be set during build with -ldflags
	gitCommit = "unknown" // Will be set during build with -ldflags
)

func main() {
	// The configuration decides how a log line is formatted, so nothing is
	// logged in the janitor's own format until it has loaded. A configuration
	// that will not load is reported plainly, once, and is the one message a
	// run can emit in another shape.
	config, err := janitor.LoadConfig(os.Args[1:], os.Getenv)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			janitor.Usage(os.Stdout, os.Getenv)
			return
		}
		fmt.Fprintf(os.Stderr, "kube-janitor: %v\n", err)
		fmt.Fprintln(os.Stderr, "Run kube-janitor -help for the options a run accepts.")
		os.Exit(1)
	}

	logger := janitor.NewLogger(config, os.Stderr)

	logger.Infof("Kubernetes Janitor %s (built: %s, commit: %s) starting up...",
		version, buildDate, gitCommit)

	if config.DryRun {
		logger.Infof("Running in dry-run mode")
	}

	cluster, credentials, err := janitor.Connect()
	if err != nil {
		logger.Errorf("Failed to connect to cluster: %v", err)
		os.Exit(1)
	}

	logger.Infof("Connected using %s", credentials)

	j := janitor.New(config, cluster, logger, janitor.NewNotifier(config))

	// A run stops when the process is asked to stop. The first signal cancels the
	// context every cleanup is given, and CleanUp checks it between listings and
	// between targets, so a cancelled run stops there rather than working through
	// the rest of the cluster.
	//
	// A write already under way is not unwound: a delete cancelled after its event
	// was recorded leaves the event behind, and the next run judges the resource
	// again.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Handling a signal only makes sense once. Giving the signals back to their
	// default disposition as soon as the first arrives means a later one ends the
	// process outright, rather than reaching a channel nothing reads any more.
	//
	// This narrows the window rather than closing it. A signal arriving in the
	// moment between the cancellation and the handlers coming off is still
	// dropped, so a second signal is a good way to end a stuck run but not a
	// guaranteed one.
	go func() {
		<-ctx.Done()
		stop()
	}()

	if config.Once {
		startTime := time.Now()
		err := j.CleanUp(ctx)

		// A run that was asked to stop did not fail. Whether CleanUp reports the
		// cancellation at all depends on where it landed — listing the namespaces
		// returns it, a later listing logs it and carries on — so the context
		// settles this rather than the error, and a signalled run always ends the
		// same way.
		if ctx.Err() != nil {
			logger.Infof("Signal received, winding down")
			return
		}

		if err != nil {
			logger.Errorf("Error during cleanup: %v", err)
			os.Exit(1)
		}

		logger.Infof("Cleanup completed in %v", time.Since(startTime))
		return
	}

	// Run periodic cleanup
	ticker := time.NewTicker(time.Duration(config.Interval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Infof("Signal received, winding down")
			return
		case <-ticker.C:
			// A tick landing in the same moment as the signal leaves both cases
			// ready, and select picks between them at random. Discovery takes no
			// context, so a run started here would reach the cluster before
			// anything could stop it.
			if ctx.Err() != nil {
				continue
			}

			startTime := time.Now()
			if err := j.CleanUp(ctx); err != nil {
				logger.Errorf("Error during cleanup: %v", err)
			} else {
				logger.Infof("Cleanup completed in %v", time.Since(startTime))
			}
		}
	}
}
