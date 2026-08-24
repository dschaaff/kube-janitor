package janitor

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// LoadConfig takes its environment as an argument, so these cases state the
// environment they mean and never touch the one the test process runs in.
func envOf(vars map[string]string) func(string) string {
	return func(key string) string { return vars[key] }
}

// noEnv is the environment a process gets when nothing is exported.
func noEnv(string) string { return "" }

func mustLoad(t *testing.T, args []string, env map[string]string) *Config {
	t.Helper()

	cfg, err := LoadConfig(args, envOf(env))
	if err != nil {
		t.Fatalf("LoadConfig(%v) returned %v", args, err)
	}
	return cfg
}

// TestLoadConfigDefaults pins the values a run uses when it is told nothing.
// Every other case states only what it changes, so this is the one place the
// defaults themselves are written down twice — deliberately, so that changing
// one has to be meant.
func TestLoadConfigDefaults(t *testing.T) {
	cfg := mustLoad(t, nil, nil)

	want := &Config{
		Interval:          30,
		LogFormat:         "%(asctime)s %(levelname)s: %(message)s",
		IncludeResources:  []string{"all"},
		ExcludeResources:  []string{"events", "controllerrevisions", "endpoints"},
		IncludeNamespaces: []string{"all"},
		ExcludeNamespaces: []string{"kube-system"},
	}

	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("LoadConfig with nothing given = %+v, want %+v", cfg, want)
	}
}

// The defaults are package-level slices, so a Config must be given its own copy
// rather than a view onto them: one process appending to its exclusions would
// otherwise change what every later Config starts from.
func TestLoadConfigDoesNotShareTheDefaultLists(t *testing.T) {
	first := mustLoad(t, nil, nil)
	first.ExcludeResources[0] = "changed"
	first.ExcludeNamespaces[0] = "changed"

	second := mustLoad(t, nil, nil)

	if second.ExcludeResources[0] != "events" {
		t.Errorf("ExcludeResources = %v, want the untouched default", second.ExcludeResources)
	}
	if second.ExcludeNamespaces[0] != "kube-system" {
		t.Errorf("ExcludeNamespaces = %v, want the untouched default", second.ExcludeNamespaces)
	}
}

// Each case states only the setting it is about: the rest of the Config must
// come back untouched, which is what catches a flag that disturbs a field it
// has no business in.
func TestLoadConfig(t *testing.T) {
	tests := []struct {
		name string
		args []string
		env  map[string]string
		want func(*Config)
	}{
		{
			name: "resources included by flag",
			args: []string{"-include-resources", "pods,services"},
			want: func(c *Config) { c.IncludeResources = []string{"pods", "services"} },
		},
		{
			name: "a single namespace included by flag",
			args: []string{"-include-namespaces", "test-namespace"},
			want: func(c *Config) { c.IncludeNamespaces = []string{"test-namespace"} },
		},
		{
			name: "namespaces excluded by flag replace the default exclusion",
			args: []string{"-exclude-namespaces", "kube-system,kube-public"},
			want: func(c *Config) { c.ExcludeNamespaces = []string{"kube-system", "kube-public"} },
		},
		{
			name: "resources excluded by flag replace the default exclusion",
			args: []string{"-exclude-resources", "events"},
			want: func(c *Config) { c.ExcludeResources = []string{"events"} },
		},
		{
			// The README offers the environment as an alternative to the flags,
			// which is how a deployment configures the janitor without arguments.
			name: "every list set by the environment",
			env: map[string]string{
				"INCLUDE_RESOURCES":  "pods",
				"EXCLUDE_RESOURCES":  "events",
				"INCLUDE_NAMESPACES": "staging",
				"EXCLUDE_NAMESPACES": "kube-system,kube-public",
			},
			want: func(c *Config) {
				c.IncludeResources = []string{"pods"}
				c.ExcludeResources = []string{"events"}
				c.IncludeNamespaces = []string{"staging"}
				c.ExcludeNamespaces = []string{"kube-system", "kube-public"}
			},
		},
		{
			name: "a flag beats the environment variable for the same list",
			args: []string{"-include-resources", "deployments"},
			env:  map[string]string{"INCLUDE_RESOURCES": "pods"},
			want: func(c *Config) { c.IncludeResources = []string{"deployments"} },
		},
		{
			// An empty variable is how a container image unsets one it inherited,
			// and must not narrow the run to a list holding one empty name.
			name: "an empty environment variable leaves the default alone",
			env: map[string]string{
				"INCLUDE_RESOURCES": "", "EXCLUDE_NAMESPACES": "", "RULES_FILE": "",
				"WEBHOOK_URL": "", "CONTEXT_NAME": "",
			},
		},
		{
			// Neither has a flag beside it: the environment is the only thing that
			// names them, and it names them before the run starts.
			name: "the webhook and context name the environment named",
			env:  map[string]string{"WEBHOOK_URL": "https://hooks.test/x", "CONTEXT_NAME": "prod-eu"},
			want: func(c *Config) { c.WebhookURL, c.ContextName = "https://hooks.test/x", "prod-eu" },
		},
		{
			name: "every switch turned on",
			args: []string{"-dry-run", "-debug", "-quiet", "-once", "-include-cluster-resources"},
			want: func(c *Config) {
				c.DryRun, c.Debug, c.Quiet, c.Once, c.IncludeClusterResources = true, true, true, true, true
			},
		},
		{
			name: "the timings given",
			args: []string{"-interval", "60", "-wait-after-delete", "5", "-delete-notification", "120"},
			want: func(c *Config) { c.Interval, c.WaitAfterDelete, c.DeleteNotification = 60, 5, 120 },
		},
		{
			name: "the annotation and log format given",
			args: []string{"-deployment-time-annotation", "deployed-at", "-log-format", "json"},
			want: func(c *Config) { c.DeploymentTimeAnnotation, c.LogFormat = "deployed-at", "json" },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := newConfig()
			if tt.want != nil {
				tt.want(want)
			}

			if got := mustLoad(t, tt.args, tt.env); !reflect.DeepEqual(got, want) {
				t.Errorf("LoadConfig(%v, %v) = %+v, want %+v", tt.args, tt.env, got, want)
			}
		})
	}
}

// A Config that loads is a Config a run can work with, so the settings that
// would make a run misbehave are refused here rather than later.
func TestLoadConfigRefusesUnusableSettings(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			// The interval drives a ticker, which panics on a period of zero.
			name: "an interval of zero",
			args: []string{"-interval", "0"},
			want: "interval must be greater than 0",
		},
		{
			name: "a negative interval",
			args: []string{"-interval", "-1"},
			want: "interval must be greater than 0",
		},
		{
			name: "a negative notification window",
			args: []string{"-delete-notification", "-1"},
			want: "delete-notification must be greater than or equal to 0",
		},
		{
			name: "a negative wait after delete",
			args: []string{"-wait-after-delete", "-1"},
			want: "wait-after-delete must be greater than or equal to 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := LoadConfig(tt.args, noEnv)

			if err == nil {
				t.Fatalf("LoadConfig(%v) returned a Config, want an error", tt.args)
			}
			if cfg != nil {
				t.Errorf("LoadConfig(%v) returned a Config alongside its error", tt.args)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("LoadConfig(%v) = %v, want it to mention %q", tt.args, err, tt.want)
			}
		})
	}
}

// The rules file is read while the configuration loads, so a run is handed
// either its Rules or an error, and never an empty rule set it cannot explain.
func TestLoadConfigReadsTheRulesFile(t *testing.T) {
	path := writeRulesFile(t, `rules:
- id: stale-pods
  resources: ["pods"]
  jmespath: "metadata.labels.test == 'true'"
  ttl: "7d"
`)

	for _, tt := range []struct {
		name string
		args []string
		env  map[string]string
	}{
		{name: "named by flag", args: []string{"-rules-file", path}},
		{name: "named by the environment", env: map[string]string{"RULES_FILE": path}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := mustLoad(t, tt.args, tt.env)

			if cfg.RulesFile != path {
				t.Errorf("RulesFile = %q, want %q", cfg.RulesFile, path)
			}
			if len(cfg.Rules) != 1 || cfg.Rules[0].ID != "stale-pods" {
				t.Fatalf("Rules = %+v, want the one rule the file holds", cfg.Rules)
			}
		})
	}
}

func TestLoadConfigRefusesABadRulesFile(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "a file that is not there",
			path: filepath.Join(t.TempDir(), "absent.yaml"),
			want: "failed to read rules file",
		},
		{
			name: "a file that is not the rules format",
			path: writeRulesFile(t, "this: [is not: valid yaml"),
			want: "failed to parse rules file",
		},
		{
			name: "a rule the janitor cannot compile",
			path: writeRulesFile(t, "rules:\n- id: broken\n  resources: [\"pods\"]\n  jmespath: \"???\"\n  ttl: \"7d\"\n"),
			want: "invalid rule",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadConfig([]string{"-rules-file", tt.path}, noEnv)

			if err == nil {
				t.Fatalf("LoadConfig loaded %s, want an error", tt.path)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("LoadConfig(%s) = %v, want it to mention %q", tt.path, err, tt.want)
			}
		})
	}
}

// The resource context hook is resolved while the configuration loads, for the
// same reason the rules file is: a run is handed the hook it was told to use,
// and a name that names nothing is refused here.
func TestLoadConfigResolvesTheResourceContextHook(t *testing.T) {
	t.Run("a hook the janitor knows", func(t *testing.T) {
		cfg := mustLoad(t, nil, map[string]string{"RESOURCE_CONTEXT_HOOK": "random_dice"})

		if cfg.ResourceContextHook == nil {
			t.Fatal("ResourceContextHook is nil, want the hook the environment named")
		}
		if data := cfg.ResourceContextHook(nil, map[string]interface{}{}); len(data) == 0 {
			t.Errorf("the hook returned %v, want it to contribute context", data)
		}
	})

	t.Run("a hook the janitor does not know", func(t *testing.T) {
		_, err := LoadConfig(nil, envOf(map[string]string{"RESOURCE_CONTEXT_HOOK": "nonesuch"}))

		if err == nil {
			t.Fatal("LoadConfig accepted an unknown hook, want an error")
		}
		if !strings.Contains(err.Error(), "nonesuch") {
			t.Errorf("LoadConfig = %v, want it to name the hook it could not find", err)
		}
	})

	t.Run("no hook named", func(t *testing.T) {
		if cfg := mustLoad(t, nil, nil); cfg.ResourceContextHook != nil {
			t.Error("ResourceContextHook is set, want nil when nothing named one")
		}
	})
}

// Usage is not a bad configuration: a caller tells the two apart so that
// -help exits successfully.
func TestLoadConfigReportsAskingForUsage(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
	}{
		{name: "nothing in the environment"},
		{
			// Asking what the options are is answerable whatever the environment
			// holds, so a variable that would sink a run must not sink -help.
			name: "an environment that would not load",
			env:  map[string]string{"RESOURCE_CONTEXT_HOOK": "nonesuch", "RULES_FILE": "/nowhere.yaml"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := LoadConfig([]string{"-help"}, envOf(tt.env)); !errors.Is(err, flag.ErrHelp) {
				t.Errorf("LoadConfig(-help) = %v, want flag.ErrHelp", err)
			}
		})
	}
}

// Usage prints the default each flag would actually take, so what a run is
// told matches what it would do rather than what the source says.
func TestUsageShowsTheDefaultsThisEnvironmentGives(t *testing.T) {
	var out strings.Builder
	Usage(&out, envOf(map[string]string{"EXCLUDE_NAMESPACES": "kube-system,kube-public"}))

	printed := out.String()

	for _, want := range []string{"-exclude-namespaces", "kube-system,kube-public", "-log-format", "-interval"} {
		if !strings.Contains(printed, want) {
			t.Errorf("usage does not mention %q:\n%s", want, printed)
		}
	}
}

func writeRulesFile(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "rules.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write the rules file: %v", err)
	}
	return path
}
