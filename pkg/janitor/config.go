package janitor

import (
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/dschaaff/kube-janitor/pkg/janitor/hooks"
)

// programName is what the janitor calls itself in its usage text and its log
// lines.
const programName = "kube-janitor"

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

	// Settings the environment names on its own, having no flag beside them.
	// WebhookURL is where a Notification is delivered, and ContextName names
	// the cluster a Notification says it came from.
	WebhookURL  string
	ContextName string

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
//
// Every failure comes back as an error and nothing is written anywhere, so the
// caller reports the trouble once, in the place it chooses. Whatever the
// environment holds, arguments that only asked for usage get usage.
func LoadConfig(args []string, env func(string) string) (*Config, error) {
	c, fs := newFlagSet(env)
	fs.SetOutput(io.Discard)

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	if err := c.validate(); err != nil {
		return nil, err
	}

	if err := c.resolveHook(env("RESOURCE_CONTEXT_HOOK")); err != nil {
		return nil, err
	}

	if err := c.loadRules(); err != nil {
		return nil, err
	}

	return c, nil
}

// Usage writes the options a run accepts to w, each with the default it would
// take from this environment. Asking for usage is not a failure, so a caller
// puts it where its output goes rather than where its errors do.
func Usage(w io.Writer, env func(string) string) {
	_, fs := newFlagSet(env)
	fs.SetOutput(w)

	fs.Usage()
}

// newFlagSet builds the flags a run is parsed from, over the defaults this
// environment gives them. Loading a Configuration and printing the options both
// go through it, so what usage says a run would do cannot drift from what it
// does.
func newFlagSet(env func(string) string) (*Config, *flag.FlagSet) {
	c := newConfig()
	c.applyEnv(env)

	fs := flag.NewFlagSet(programName, flag.ContinueOnError)
	c.addFlags(fs)

	return c, fs
}

// applyEnv folds the environment into the defaults before the flags are
// registered, so every flag's default is the value the run would use had the
// flag been left out — which is also what usage prints. A variable that is
// unset or empty leaves the default alone, which is how a container image
// unsets one it inherited.
//
// Some settings have no flag beside them and are only ever named here; they
// are read in the same place as the rest so that the whole of a Configuration
// is settled before the first Listing, and nothing reaches for the environment
// again once a run has started.
//
// Nothing here can fail: it only decides what a setting starts as. The settings
// the environment names that could be wrong are resolved after the arguments
// are parsed, so a run that only asked for usage still gets it.
func (c *Config) applyEnv(env func(string) string) {
	list := func(key string, target *[]string) {
		if value := env(key); value != "" {
			*target = splitList(value)
		}
	}

	text := func(key string, target *string) {
		if value := env(key); value != "" {
			*target = value
		}
	}

	list("INCLUDE_RESOURCES", &c.IncludeResources)
	list("EXCLUDE_RESOURCES", &c.ExcludeResources)
	list("INCLUDE_NAMESPACES", &c.IncludeNamespaces)
	list("EXCLUDE_NAMESPACES", &c.ExcludeNamespaces)

	text("RULES_FILE", &c.RulesFile)
	text("WEBHOOK_URL", &c.WebhookURL)
	text("CONTEXT_NAME", &c.ContextName)
}

// resolveHook looks up the resource context hook the environment named, so that
// a run is handed the hook it asked for and a name that names nothing is
// refused before the first Listing rather than at the first Target.
func (c *Config) resolveHook(name string) error {
	if name == "" {
		return nil
	}

	hook, err := hooks.GetHook(name)
	if err != nil {
		return fmt.Errorf("failed to get resource context hook: %w", err)
	}

	c.ResourceContextHook = ResourceContextHook(hook)
	return nil
}

// addFlags registers each flag against the field it sets, taking its default
// from the value that field already holds.
func (c *Config) addFlags(fs *flag.FlagSet) {
	fs.BoolVar(&c.DryRun, "dry-run", c.DryRun, "Dry run mode: do not change anything, just print what would be done")
	fs.BoolVar(&c.Debug, "debug", c.Debug, "Debug mode: also log DEBUG lines")
	fs.BoolVar(&c.Quiet, "quiet", c.Quiet, "Quiet mode: log only WARNING and ERROR lines")
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

	if _, err := parseLogFormat(c.LogFormat); err != nil {
		return err
	}

	return nil
}

// notificationPrefix names the cluster a Notification came from, when the
// Configuration gives it a name to go by. A Notification is worded once, when
// the Verdict is reached, so the event, the delivery and the dry-run preview
// cannot disagree about which cluster is talking.
func (c *Config) notificationPrefix() string {
	if c.ContextName == "" {
		return ""
	}

	return "[" + c.ContextName + "] "
}

// lowestLogLevel works out the least a run reports. Asking for a diagnosis
// beats asking for quiet: a run given both is being diagnosed, and a diagnosis
// that left out the ordinary course of the run would be a poor one.
func (c *Config) lowestLogLevel() logLevel {
	switch {
	case c.Debug:
		return levelDebug
	case c.Quiet:
		return levelWarning
	default:
		return levelInfo
	}
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
		return fmt.Errorf("failed to load rules: %w", err)
	}

	c.Rules = rules
	return nil
}
