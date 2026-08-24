package main

import (
	"errors"
	"flag"
	"log"
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
	log.Printf("Kubernetes Janitor %s (built: %s, commit: %s) starting up...",
		version, buildDate, gitCommit)

	config, err := janitor.LoadConfig(os.Args[1:], os.Getenv)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		log.Fatalf("Invalid configuration: %v", err)
	}

	if config.Debug {
		log.SetFlags(log.LstdFlags | log.Lshortfile)
	}

	if config.DryRun {
		log.Println("Running in dry-run mode")
	}

	// Check for KUBECONFIG environment variable
	if os.Getenv("KUBECONFIG") == "" {
		homeDir, err := os.UserHomeDir()
		if err == nil {
			defaultKubeconfig := filepath.Join(homeDir, ".kube", "config")
			if _, err := os.Stat(defaultKubeconfig); err == nil {
				log.Printf("KUBECONFIG not set, using default: %s", defaultKubeconfig)
			} else {
				log.Printf("Warning: KUBECONFIG not set and default config not found at %s", defaultKubeconfig)
			}
		}
	} else {
		log.Printf("Using KUBECONFIG from environment: %s", os.Getenv("KUBECONFIG"))
	}

	cluster, err := janitor.Connect()
	if err != nil {
		log.Fatalf("Failed to connect to cluster: %v", err)
	}

	j := janitor.New(config, cluster)

	// Set up context with cancellation and signal handling
	ctx, gs := shutdown.ShutdownWithContext()

	// Set safe to exit when we're done with cleanup
	defer gs.SetSafeToExit(true)

	if config.Once {
		startTime := time.Now()
		if err := j.CleanUp(ctx); err != nil {
			log.Printf("Error during cleanup: %v", err)
			os.Exit(1)
		}
		log.Printf("Cleanup completed in %v", time.Since(startTime))
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
				log.Printf("Error during cleanup: %v", err)
			} else {
				log.Printf("Cleanup completed in %v", time.Since(startTime))
			}
		}
	}
}
