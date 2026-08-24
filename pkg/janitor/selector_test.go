package janitor

import (
	"slices"
	"testing"
)

// The Selector answers from the configuration, the discovered types and the
// namespace names alone, so these cases need no cluster and no fakes.

// refs renders listings as "plural@namespace" so a case can state the whole plan
// on one line, in order.
func refs(listings []listing) []string {
	out := make([]string, 0, len(listings))
	for _, l := range listings {
		out = append(out, l.Type.Plural+"@"+l.Namespace)
	}
	return out
}

func selectorFor(configure func(*Config)) *selector {
	cfg := NewConfig()
	cfg.ExcludeResources = nil
	cfg.ExcludeNamespaces = nil
	if configure != nil {
		configure(cfg)
	}
	return newSelector(cfg)
}

func TestSelectorListings(t *testing.T) {
	tests := []struct {
		name       string
		configure  func(*Config)
		types      []ResourceType
		namespaces []string
		want       []string
	}{
		{
			name:       "a namespaced type is listed once per admitted namespace",
			types:      []ResourceType{podResourceType},
			namespaces: []string{"staging", "prod"},
			want:       []string{"pods@staging", "pods@prod"},
		},
		{
			// The plan is what stops a resource being reached twice, so a type
			// appearing once in discovery must appear once here.
			name:       "namespaces are listed exactly once",
			configure:  func(c *Config) { c.IncludeClusterResources = true },
			types:      []ResourceType{namespaceResourceType, podResourceType},
			namespaces: []string{"staging"},
			want:       []string{"namespaces@", "pods@staging"},
		},
		{
			name:       "namespaces come first whatever order discovery reported",
			types:      []ResourceType{podResourceType, deploymentResourceType, namespaceResourceType},
			namespaces: []string{"staging"},
			want:       []string{"namespaces@", "pods@staging", "deployments@staging"},
		},
		{
			// Discovery reports types in map order, so the plan sorts them: by
			// group first, which puts the core group ahead of the named ones.
			name:       "the order does not depend on the order discovery reported",
			types:      []ResourceType{ingressResourceType, deploymentResourceType, podResourceType},
			namespaces: []string{"staging"},
			want:       []string{"pods@staging", "deployments@staging", "ingresses@staging"},
		},
		{
			name:       "an excluded namespace is not listed",
			configure:  func(c *Config) { c.ExcludeNamespaces = []string{"kube-system"} },
			types:      []ResourceType{podResourceType},
			namespaces: []string{"staging", "kube-system"},
			want:       []string{"pods@staging"},
		},
		{
			name:       "only included namespaces are listed",
			configure:  func(c *Config) { c.IncludeNamespaces = []string{"staging"} },
			types:      []ResourceType{podResourceType},
			namespaces: []string{"staging", "prod"},
			want:       []string{"pods@staging"},
		},
		{
			name:       "exclusion beats inclusion",
			configure:  func(c *Config) { c.IncludeNamespaces = []string{"staging"}; c.ExcludeNamespaces = []string{"staging"} },
			types:      []ResourceType{podResourceType},
			namespaces: []string{"staging"},
			want:       nil,
		},
		{
			name:       "an excluded type is not listed at all",
			configure:  func(c *Config) { c.ExcludeResources = []string{"pods"} },
			types:      []ResourceType{podResourceType, deploymentResourceType},
			namespaces: []string{"staging"},
			want:       []string{"deployments@staging"},
		},
		{
			name:       "only included types are listed",
			configure:  func(c *Config) { c.IncludeResources = []string{"deployments"} },
			types:      []ResourceType{podResourceType, deploymentResourceType},
			namespaces: []string{"staging"},
			want:       []string{"deployments@staging"},
		},
		{
			// The include list names a type by the plural discovery reported, so an
			// irregular one is matched as listed rather than derived from the kind.
			name:       "an irregular plural is matched as listed",
			configure:  func(c *Config) { c.IncludeResources = []string{"ingresses"} },
			types:      []ResourceType{ingressResourceType, podResourceType},
			namespaces: []string{"staging"},
			want:       []string{"ingresses@staging"},
		},
		{
			name:       "a cluster-scoped type is left out unless cluster resources are included",
			types:      []ResourceType{persistentVolumeResourceType, podResourceType},
			namespaces: []string{"staging"},
			want:       []string{"pods@staging"},
		},
		{
			name:       "a cluster-scoped type is listed once cluster resources are included",
			configure:  func(c *Config) { c.IncludeClusterResources = true },
			types:      []ResourceType{persistentVolumeResourceType, podResourceType},
			namespaces: []string{"staging"},
			want:       []string{"persistentvolumes@", "pods@staging"},
		},
		{
			// The README promises namespaces are the one cluster-scoped type a run
			// handles without the flag.
			name:       "namespaces are listed without cluster resources being included",
			types:      []ResourceType{namespaceResourceType},
			namespaces: []string{"staging"},
			want:       []string{"namespaces@"},
		},
		{
			name:       "namespaces are left out when the include list does not name them",
			configure:  func(c *Config) { c.IncludeResources = []string{"pods"} },
			types:      []ResourceType{namespaceResourceType, podResourceType},
			namespaces: []string{"staging"},
			want:       []string{"pods@staging"},
		},
		{
			name:       "excluding namespaces as a type stops them being listed",
			configure:  func(c *Config) { c.ExcludeResources = []string{"namespaces"} },
			types:      []ResourceType{namespaceResourceType, podResourceType},
			namespaces: []string{"staging"},
			want:       []string{"pods@staging"},
		},
		{
			name:       "a cluster holding no admitted namespace still lists a cluster-scoped type",
			configure:  func(c *Config) { c.IncludeNamespaces = []string{"nothing-here"} },
			types:      []ResourceType{namespaceResourceType, podResourceType},
			namespaces: []string{"staging"},
			want:       []string{"namespaces@"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := refs(selectorFor(tt.configure).listings(tt.types, tt.namespaces))

			if !slices.Equal(got, tt.want) {
				t.Errorf("listings() = %v, want %v", got, tt.want)
			}
		})
	}
}

// A Listing settles the Resource type and the namespace of everything it
// returns, so the only Target left to judge is one whose own name is a
// namespace: those are listed across the cluster and cannot be narrowed by the
// list call.
func TestSelectorAdmits(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*Config)
		target    Target
		want      bool
	}{
		{
			name:   "a namespace the run considers",
			target: namespaceTarget("staging"),
			want:   true,
		},
		{
			name:      "an excluded namespace",
			configure: func(c *Config) { c.ExcludeNamespaces = []string{"kube-system"} },
			target:    namespaceTarget("kube-system"),
			want:      false,
		},
		{
			name:      "a namespace outside the include list",
			configure: func(c *Config) { c.IncludeNamespaces = []string{"staging"} },
			target:    namespaceTarget("prod"),
			want:      false,
		},
		{
			// Its listing already decided this, so nothing is re-judged here.
			name:      "a resource from a namespaced listing",
			configure: func(c *Config) { c.IncludeNamespaces = []string{"staging"} },
			target:    newTarget(resourceObject(podResourceType, "staging", "web"), podResourceType),
			want:      true,
		},
		{
			name:      "a cluster-scoped resource that is not a namespace",
			configure: func(c *Config) { c.IncludeClusterResources = true },
			target:    newTarget(resourceObject(persistentVolumeResourceType, "", "pv-1"), persistentVolumeResourceType),
			want:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sel := selectorFor(tt.configure)

			if got := sel.admits(tt.target); got != tt.want {
				t.Errorf("admits(%s) = %v, want %v", tt.target.describe(), got, tt.want)
			}
		})
	}
}

func namespaceTarget(name string) Target {
	return newTarget(resourceObject(namespaceResourceType, "", name), namespaceResourceType)
}
