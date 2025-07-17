package main

import (
	"flag"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/dschaaff/kube-janitor/pkg/janitor"
	"github.com/dschaaff/kube-janitor/pkg/janitor/hooks"
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

	config := janitor.NewConfig()
	config.AddFlags(flag.CommandLine)

	flag.Parse() // Parse flags after they've been added to flag.CommandLine

	// Parse the comma-separated string flags after flag.Parse()
	config.ParseStringFlags()

	// Set default parallelism if not specified
	if config.Parallelism == 0 {
		config.Parallelism = runtime.NumCPU()
	}

	if config.Debug {
		log.SetFlags(log.LstdFlags | log.Lshortfile)
	}

	if config.DryRun {
		log.Println("Running in dry-run mode")
	}

	log.Printf("Performance settings: parallelism=%d", config.Parallelism)

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

	if err := config.Validate(); err != nil {
		log.Fatalf("Invalid configuration: %v", err)
	}

	if hookName := os.Getenv("RESOURCE_CONTEXT_HOOK"); hookName != "" {
		hookFunc, err := hooks.GetHook(hookName)
		if err != nil {
			log.Fatalf("Failed to get hook: %v", err)
		}
		// Convert hooks.ResourceContextHook to janitor.ResourceContextHook
		config.ResourceContextHook = func(resource interface{}, cache map[string]interface{}) map[string]interface{} {
			return hookFunc(resource, cache)
		}
	}

	if err := config.LoadRules(); err != nil {
		log.Fatalf("Failed to load rules: %v", err)
	}

	j, err := janitor.New(config)
	if err != nil {
		log.Fatalf("Failed to create janitor: %v", err)
	}

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

// getEnvOrDefault moved to pkg/janitor/config.go
