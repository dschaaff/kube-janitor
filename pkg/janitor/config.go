package janitor

import (
	"flag"
	"fmt"
	"slices"
	"strings"

	"github.com/dschaaff/kube-janitor/pkg/janitor/hooks"
)

const (
	defaultInterval  = 30
	defaultLogFormat = "%(asctime)s %(levelname)s: %(message)s"
)

var (
	defaultExcludeResources  = []string{"events", "controllerrevisions", "endpoints"}
	defaultExcludeNamespaces = []string{"kube-system"}
)

// Config holds all configuration options for the janitor
type Config struct {
	// Command line flags
	DryRun                   bool
	Debug                    bool
	Quiet                    bool
	Once                     bool
	Interval                 int
	WaitAfterDelete          int
	DeleteNotification       int
	IncludeResources         []string
	ExcludeResources         []string
	IncludeNamespaces        []string
	ExcludeNamespaces        []string
	RulesFile                string
	DeploymentTimeAnnotation string
	IncludeClusterResources  bool
	LogFormat                string

	// Additional configuration
	Rules               []Rule
	ResourceContextHook ResourceContextHook
}

// newConfig returns the configuration a run uses when nothing overrides it. It
// is the only place a default is written down: the flags take theirs from the
// Config this returns rather than declaring a second copy.
func newConfig() *Config {
	return &Config{
		Interval:          defaultInterval,
		LogFormat:         defaultLogFormat,
		ExcludeResources:  slices.Clone(defaultExcludeResources),
		ExcludeNamespaces: slices.Clone(defaultExcludeNamespaces),
		IncludeResources:  []string{"all"},
		IncludeNamespaces: []string{"all"},
	}
}

// LoadConfig builds the configuration for one process from its arguments and
// its environment. It is the whole of construction, so no caller can get the
// order wrong and nothing downstream is handed a half-built Config.
//
// env is the environment lookup, taken as an argument so a test needs no
// ambient variables; a program passes os.Getenv. A flag beats the environment
// variable for the same setting, which beats the default.
//
// The Config it returns is parsed, validated, and carries the Rules and the
// resource context hook its settings name. It returns flag.ErrHelp when the
// arguments asked for usage, which a caller reports as success rather than as a
// bad configuration.
func LoadConfig(args []string, env func(string) string) (*Config, error) {
	c := newConfig()
	if err := c.applyEnv(env); err != nil {
		return nil, err
	}

	fs := flag.NewFlagSet("kube-janitor", flag.ContinueOnError)
	c.addFlags(fs)

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	if err := c.validate(); err != nil {
		return nil, err
	}

	if err := c.loadRules(); err != nil {
		return nil, err
	}

	return c, nil
}

// applyEnv folds the environment into the defaults before the flags are
// registered, so every flag's default is the value the run would use had the
// flag been left out — which is also what usage prints. A variable that is
// unset or empty leaves the default alone, which is how a container image
// unsets one it inherited.
func (c *Config) applyEnv(env func(string) string) error {
	list := func(key string, target *[]string) {
		if value := env(key); value != "" {
			*target = splitList(value)
		}
	}

	list("INCLUDE_RESOURCES", &c.IncludeResources)
	list("EXCLUDE_RESOURCES", &c.ExcludeResources)
	list("INCLUDE_NAMESPACES", &c.IncludeNamespaces)
	list("EXCLUDE_NAMESPACES", &c.ExcludeNamespaces)

	if value := env("RULES_FILE"); value != "" {
		c.RulesFile = value
	}

	if name := env("RESOURCE_CONTEXT_HOOK"); name != "" {
		hook, err := hooks.GetHook(name)
		if err != nil {
			return fmt.Errorf("failed to get hook: %v", err)
		}
		c.ResourceContextHook = ResourceContextHook(hook)
	}

	return nil
}

// addFlags registers each flag against the field it sets, taking its default
// from the value that field already holds.
func (c *Config) addFlags(fs *flag.FlagSet) {
	fs.BoolVar(&c.DryRun, "dry-run", c.DryRun, "Dry run mode: do not change anything, just print what would be done")
	fs.BoolVar(&c.Debug, "debug", c.Debug, "Debug mode: print more information")
	fs.BoolVar(&c.Quiet, "quiet", c.Quiet, "Quiet mode: Hides cleanup logs but keeps deletion logs")
	fs.BoolVar(&c.Once, "once", c.Once, "Run only once and exit")
	fs.IntVar(&c.Interval, "interval", c.Interval, "Loop interval in seconds")
	fs.IntVar(&c.WaitAfterDelete, "wait-after-delete", c.WaitAfterDelete, "Wait time after issuing a delete (in seconds)")
	fs.IntVar(&c.DeleteNotification, "delete-notification", c.DeleteNotification, "Send an event seconds before to warn of the deletion")

	fs.Var(stringList{&c.IncludeResources}, "include-resources", "Resources to consider for clean up (comma-separated)")
	fs.Var(stringList{&c.ExcludeResources}, "exclude-resources", "Resources to exclude from clean up (comma-separated)")
	fs.Var(stringList{&c.IncludeNamespaces}, "include-namespaces", "Include namespaces for clean up (comma-separated)")
	fs.Var(stringList{&c.ExcludeNamespaces}, "exclude-namespaces", "Exclude namespaces from clean up (comma-separated)")

	fs.StringVar(&c.RulesFile, "rules-file", c.RulesFile, "Load TTL rules from given file path")
	fs.StringVar(&c.DeploymentTimeAnnotation, "deployment-time-annotation", c.DeploymentTimeAnnotation, "Annotation that contains a resource's last deployment time")
	fs.BoolVar(&c.IncludeClusterResources, "include-cluster-resources", c.IncludeClusterResources, "Include cluster scoped resources")
	fs.StringVar(&c.LogFormat, "log-format", c.LogFormat, "Set custom log format")
}

// splitList reads a comma-separated list. It is the one place that decides what
// separates two names, so a flag and the environment variable beside it cannot
// disagree about what a list is.
func splitList(value string) []string {
	return strings.Split(value, ",")
}

// stringList is a comma-separated flag that splits straight into the field it
// sets. A parsed FlagSet is therefore a finished Config, with no second step to
// forget.
type stringList struct {
	target *[]string
}

// String renders the list the way usage prints a default. The flag package also
// calls this on a zero stringList to work out whether a flag has a default
// worth printing, so it has to tolerate a nil target.
func (l stringList) String() string {
	if l.target == nil {
		return ""
	}
	return strings.Join(*l.target, ",")
}

func (l stringList) Set(value string) error {
	*l.target = splitList(value)
	return nil
}

// validate rejects the settings a run cannot work with.
func (c *Config) validate() error {
	if c.Interval < 1 {
		return fmt.Errorf("interval must be greater than 0")
	}

	if c.DeleteNotification < 0 {
		return fmt.Errorf("delete-notification must be greater than or equal to 0")
	}

	if c.WaitAfterDelete < 0 {
		return fmt.Errorf("wait-after-delete must be greater than or equal to 0")
	}

	return nil
}

// loadRules reads the rules file, if one was named. A file that cannot be read
// or parsed stops the configuration from loading rather than leaving the run
// with no rules and no complaint.
func (c *Config) loadRules() error {
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
