package janitor

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

const (
	defaultExcludeResources  = "events,controllerrevisions,endpoints"
	defaultExcludeNamespaces = "kube-system"
	defaultInterval         = 30
	defaultLogFormat       = "%(asctime)s %(levelname)s: %(message)s"
)

// Config holds all configuration options for the janitor
type Config struct {
	// Command line flags
	DryRun                  bool
	Debug                   bool
	Quiet                   bool
	Once                    bool
	Interval               int
	WaitAfterDelete        int
	DeleteNotification     int
	IncludeResources      []string
	ExcludeResources      []string
	IncludeNamespaces     []string
	ExcludeNamespaces     []string
	RulesFile             string
	DeploymentTimeAnnotation string
	IncludeClusterResources bool
	LogFormat             string
	Parallelism           int

	// Internal string fields for flag parsing
	includeResourcesStr   string
	excludeResourcesStr   string
	includeNamespacesStr  string
	excludeNamespacesStr  string

	// Additional configuration
	Rules               []Rule
	ResourceContextHook ResourceContextHook
	WebhookURL         string
}

// NewConfig creates a new Config with default values
func NewConfig() *Config {
	return &Config{
		Interval:          defaultInterval,
		LogFormat:        defaultLogFormat,
		ExcludeResources: strings.Split(defaultExcludeResources, ","),
		ExcludeNamespaces: strings.Split(defaultExcludeNamespaces, ","),
		IncludeResources: []string{"all"},
		IncludeNamespaces: []string{"all"},
		Parallelism:      DefaultParallelism,
	}
}

// AddFlags adds command line flags to parse configuration
func (c *Config) AddFlags(fs *flag.FlagSet) {
	fs.BoolVar(&c.DryRun, "dry-run", false, "Dry run mode: do not change anything, just print what would be done")
	fs.BoolVar(&c.Debug, "debug", false, "Debug mode: print more information")
	fs.BoolVar(&c.Quiet, "quiet", false, "Quiet mode: Hides cleanup logs but keeps deletion logs")
	fs.BoolVar(&c.Once, "once", false, "Run only once and exit")
	fs.IntVar(&c.Interval, "interval", defaultInterval, "Loop interval in seconds")
	fs.IntVar(&c.WaitAfterDelete, "wait-after-delete", 0, "Wait time after issuing a delete (in seconds)")
	fs.IntVar(&c.DeleteNotification, "delete-notification", 0, "Send an event seconds before to warn of the deletion")
	
	// Use custom variables to handle comma-separated lists
	fs.StringVar(&c.includeResourcesStr, "include-resources", getEnvOrDefault("INCLUDE_RESOURCES", "all"), "Resources to consider for clean up (comma-separated)")
	fs.StringVar(&c.excludeResourcesStr, "exclude-resources", getEnvOrDefault("EXCLUDE_RESOURCES", defaultExcludeResources), "Resources to exclude from clean up (comma-separated)")
	fs.StringVar(&c.includeNamespacesStr, "include-namespaces", getEnvOrDefault("INCLUDE_NAMESPACES", "all"), "Include namespaces for clean up (comma-separated)")
	fs.StringVar(&c.excludeNamespacesStr, "exclude-namespaces", getEnvOrDefault("EXCLUDE_NAMESPACES", defaultExcludeNamespaces), "Exclude namespaces from clean up (comma-separated)")
	
	fs.StringVar(&c.RulesFile, "rules-file", os.Getenv("RULES_FILE"), "Load TTL rules from given file path")
	fs.StringVar(&c.DeploymentTimeAnnotation, "deployment-time-annotation", "", "Annotation that contains a resource's last deployment time")
	fs.BoolVar(&c.IncludeClusterResources, "include-cluster-resources", false, "Include cluster scoped resources")
	fs.StringVar(&c.LogFormat, "log-format", defaultLogFormat, "Set custom log format")
	fs.IntVar(&c.Parallelism, "parallelism", DefaultParallelism, "Number of parallel workers for resource processing (0 = use number of CPUs)")
}

// ParseStringFlags parses the comma-separated string flags into string slices
// This must be called after flag.Parse()
func (c *Config) ParseStringFlags() {
	c.IncludeResources = strings.Split(c.includeResourcesStr, ",")
	c.ExcludeResources = strings.Split(c.excludeResourcesStr, ",")
	c.IncludeNamespaces = strings.Split(c.includeNamespacesStr, ",")
	c.ExcludeNamespaces = strings.Split(c.excludeNamespacesStr, ",")
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	if c.Interval < 1 {
		return fmt.Errorf("interval must be greater than 0")
	}

	if c.DeleteNotification < 0 {
		return fmt.Errorf("delete-notification must be greater than or equal to 0")
	}

	if c.WaitAfterDelete < 0 {
		return fmt.Errorf("wait-after-delete must be greater than or equal to 0")
	}

	if c.Parallelism < 0 {
		return fmt.Errorf("parallelism must be greater than or equal to 0")
	}

	return nil
}

// LoadRules loads rules from the rules file if specified
func (c *Config) LoadRules() error {
	if c.RulesFile == "" {
		return nil
	}

	rules, err := LoadRules(c.RulesFile)
	if err != nil {
		return fmt.Errorf("failed to load rules: %v", err)
	}

	c.Rules = rules
	return nil
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
