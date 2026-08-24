package janitor

import (
	"cmp"
	"slices"
)

// namespacesPlural is the plural a namespace is listed and deleted through.
const namespacesPlural = "namespaces"

// isNamespaces reports whether a group and plural name the built-in Namespace
// type. Matching on the plural alone would also catch a custom resource that
// happens to be called "namespaces" in a group of its own, which has none of a
// namespace's standing.
func isNamespaces(group, plural string) bool {
	return group == "" && plural == namespacesPlural
}

// listing is one list a run makes: every resource of one Resource type in one
// namespace, or across the cluster when Namespace is empty.
type listing struct {
	Type      ResourceType
	Namespace string
}

// selector decides which resources a run considers at all, from the configured
// resource and namespace include and exclude lists. It answers from the
// configuration alone, without reading the cluster.
//
// Because it names each listing once, no resource can be reached twice in a run.
type selector struct {
	cfg *Config
}

func newSelector(cfg *Config) *selector {
	return &selector{cfg: cfg}
}

// listings plans the lists a run makes over the discovered Resource types and
// the namespaces the cluster holds, in a stable order: namespaces first, as a
// run has always taken them, then every other admitted type by group, version
// and plural.
//
// Discovery reports types in map order, so sorting here is what makes a run's
// sequence of cluster reads reproducible.
func (s *selector) listings(types []ResourceType, namespaces []string) []listing {
	admitted := make([]ResourceType, 0, len(types))
	for _, rt := range types {
		if s.admitsType(rt) {
			admitted = append(admitted, rt)
		}
	}

	slices.SortFunc(admitted, func(a, b ResourceType) int {
		return cmp.Or(
			cmp.Compare(typeRank(a), typeRank(b)),
			cmp.Compare(a.Group, b.Group),
			cmp.Compare(a.Version, b.Version),
			cmp.Compare(a.Plural, b.Plural),
		)
	})

	admittedNamespaces := s.admittedNamespaces(namespaces)

	listings := make([]listing, 0, len(admitted)*max(len(admittedNamespaces), 1))
	for _, rt := range admitted {
		if !rt.Namespaced {
			listings = append(listings, listing{Type: rt})
			continue
		}
		for _, namespace := range admittedNamespaces {
			listings = append(listings, listing{Type: rt, Namespace: namespace})
		}
	}

	return listings
}

// admits reports whether a listed Target is one the run considers. The listing
// it came from already settled its Resource type and its namespace, so the only
// question left is a Target whose own name is a namespace: those are listed
// across the cluster and cannot be narrowed by the list call.
func (s *selector) admits(t Target) bool {
	if isNamespaces(t.GVR.Group, t.plural()) {
		return s.admitsNamespace(t.Name)
	}
	return true
}

// admitsType reports whether any resource of the type is considered. Exclusion
// takes precedence over inclusion. A cluster-scoped type needs cluster resources
// to be included, except for namespaces, which a run always considers when they
// are named: see --include-cluster-resources in the README.
func (s *selector) admitsType(rt ResourceType) bool {
	if slices.Contains(s.cfg.ExcludeResources, rt.Plural) {
		return false
	}

	if !slices.Contains(s.cfg.IncludeResources, "all") &&
		!slices.Contains(s.cfg.IncludeResources, rt.Plural) {
		return false
	}

	if !rt.Namespaced && !isNamespaces(rt.Group, rt.Plural) && !s.cfg.IncludeClusterResources {
		return false
	}

	return true
}

// admitsNamespace reports whether resources in the namespace are considered, and
// whether the namespace itself is. Exclusion takes precedence over inclusion.
func (s *selector) admitsNamespace(name string) bool {
	if slices.Contains(s.cfg.ExcludeNamespaces, name) {
		return false
	}

	return slices.Contains(s.cfg.IncludeNamespaces, "all") ||
		slices.Contains(s.cfg.IncludeNamespaces, name)
}

// admittedNamespaces keeps the order the cluster reported, so a run walks
// namespaces the way the API server lists them.
func (s *selector) admittedNamespaces(namespaces []string) []string {
	admitted := make([]string, 0, len(namespaces))
	for _, name := range namespaces {
		if s.admitsNamespace(name) {
			admitted = append(admitted, name)
		}
	}
	return admitted
}

// typeRank orders namespaces ahead of every other Resource type.
func typeRank(rt ResourceType) int {
	if isNamespaces(rt.Group, rt.Plural) {
		return 0
	}
	return 1
}
