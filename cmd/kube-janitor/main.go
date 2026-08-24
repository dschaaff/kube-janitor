package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dschaaff/kube-janitor/pkg/janitor"
	"github.com/dschaaff/kube-janitor/pkg/janitor/shutdown"
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

	// Check for KUBECONFIG environment variable
	if os.Getenv("KUBECONFIG") == "" {
		homeDir, err := os.UserHomeDir()
		if err == nil {
			defaultKubeconfig := filepath.Join(homeDir, ".kube", "config")
			if _, err := os.Stat(defaultKubeconfig); err == nil {
				logger.Infof("KUBECONFIG not set, using default: %s", defaultKubeconfig)
			} else {
				logger.Warnf("KUBECONFIG not set and default config not found at %s", defaultKubeconfig)
			}
		}
	} else {
		logger.Infof("Using KUBECONFIG from environment: %s", os.Getenv("KUBECONFIG"))
	}

	cluster, err := janitor.Connect()
	if err != nil {
		logger.Errorf("Failed to connect to cluster: %v", err)
		os.Exit(1)
	}

	j := janitor.New(config, cluster, logger, janitor.NewNotifier(config))

	// Set up context with cancellation and signal handling
	ctx, gs := shutdown.ShutdownWithContext()

	// Set safe to exit when we're done with cleanup
	defer gs.SetSafeToExit(true)

	if config.Once {
		startTime := time.Now()
		if err := j.CleanUp(ctx); err != nil {
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
			return
		case <-ticker.C:
			startTime := time.Now()
			if err := j.CleanUp(ctx); err != nil {
				logger.Errorf("Error during cleanup: %v", err)
			} else {
				logger.Infof("Cleanup completed in %v", time.Since(startTime))
			}
		}
	}
}
