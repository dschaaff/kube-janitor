package janitor

import (
	"flag"
	"testing"
)

func TestConfigFlagParsing(t *testing.T) {
	tests := []struct {
		name                      string
		args                      []string
		expectedIncludeResources  []string
		expectedExcludeResources  []string
		expectedIncludeNamespaces []string
		expectedExcludeNamespaces []string
	}{
		{
			name:                      "default flags",
			args:                      []string{},
			expectedIncludeResources:  []string{"all"},
			expectedExcludeResources:  []string{"events", "controllerrevisions", "endpoints"},
			expectedIncludeNamespaces: []string{"all"},
			expectedExcludeNamespaces: []string{"kube-system"},
		},
		{
			name:                      "custom include resources",
			args:                      []string{"-include-resources", "pods,services"},
			expectedIncludeResources:  []string{"pods", "services"},
			expectedExcludeResources:  []string{"events", "controllerrevisions", "endpoints"},
			expectedIncludeNamespaces: []string{"all"},
			expectedExcludeNamespaces: []string{"kube-system"},
		},
		{
			name:                      "custom include namespaces",
			args:                      []string{"-include-namespaces", "default,test"},
			expectedIncludeResources:  []string{"all"},
			expectedExcludeResources:  []string{"events", "controllerrevisions", "endpoints"},
			expectedIncludeNamespaces: []string{"default", "test"},
			expectedExcludeNamespaces: []string{"kube-system"},
		},
		{
			name:                      "custom exclude namespaces",
			args:                      []string{"-exclude-namespaces", "kube-system,kube-public"},
			expectedIncludeResources:  []string{"all"},
			expectedExcludeResources:  []string{"events", "controllerrevisions", "endpoints"},
			expectedIncludeNamespaces: []string{"all"},
			expectedExcludeNamespaces: []string{"kube-system", "kube-public"},
		},
		{
			name:                      "single namespace include",
			args:                      []string{"-include-namespaces", "test-namespace"},
			expectedIncludeResources:  []string{"all"},
			expectedExcludeResources:  []string{"events", "controllerrevisions", "endpoints"},
			expectedIncludeNamespaces: []string{"test-namespace"},
			expectedExcludeNamespaces: []string{"kube-system"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a new FlagSet for this test
			fs := flag.NewFlagSet("test", flag.ContinueOnError)

			// Create config and add flags
			config := NewConfig()
			config.AddFlags(fs)

			// Parse the test arguments
			if len(tt.args) > 0 {
				err := fs.Parse(tt.args)
				if err != nil {
					t.Fatalf("Failed to parse flags: %v", err)
				}
			}

			// Parse the string flags after flag parsing
			config.ParseStringFlags()

			// Check include resources
			if len(config.IncludeResources) != len(tt.expectedIncludeResources) {
				t.Errorf("Expected %d include resources, got %d", len(tt.expectedIncludeResources), len(config.IncludeResources))
			}
			for i, expected := range tt.expectedIncludeResources {
				if i >= len(config.IncludeResources) || config.IncludeResources[i] != expected {
					t.Errorf("Expected include resource %d to be %s, got %s", i, expected, config.IncludeResources[i])
				}
			}

			// Check exclude resources
			if len(config.ExcludeResources) != len(tt.expectedExcludeResources) {
				t.Errorf("Expected %d exclude resources, got %d", len(tt.expectedExcludeResources), len(config.ExcludeResources))
			}
			for i, expected := range tt.expectedExcludeResources {
				if i >= len(config.ExcludeResources) || config.ExcludeResources[i] != expected {
					t.Errorf("Expected exclude resource %d to be %s, got %s", i, expected, config.ExcludeResources[i])
				}
			}

			// Check include namespaces
			if len(config.IncludeNamespaces) != len(tt.expectedIncludeNamespaces) {
				t.Errorf("Expected %d include namespaces, got %d", len(tt.expectedIncludeNamespaces), len(config.IncludeNamespaces))
			}
			for i, expected := range tt.expectedIncludeNamespaces {
				if i >= len(config.IncludeNamespaces) || config.IncludeNamespaces[i] != expected {
					t.Errorf("Expected include namespace %d to be %s, got %s", i, expected, config.IncludeNamespaces[i])
				}
			}

			// Check exclude namespaces
			if len(config.ExcludeNamespaces) != len(tt.expectedExcludeNamespaces) {
				t.Errorf("Expected %d exclude namespaces, got %d", len(tt.expectedExcludeNamespaces), len(config.ExcludeNamespaces))
			}
			for i, expected := range tt.expectedExcludeNamespaces {
				if i >= len(config.ExcludeNamespaces) || config.ExcludeNamespaces[i] != expected {
					t.Errorf("Expected exclude namespace %d to be %s, got %s", i, expected, config.ExcludeNamespaces[i])
				}
			}
		})
	}
}
