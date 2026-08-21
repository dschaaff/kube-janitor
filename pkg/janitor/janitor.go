package janitor

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
)

// namespaceResourceType is the Resource type namespaces are listed and deleted
// through. Namespaces come from the typed client, which carries no discovery
// record of its own, and these values are fixed by the Kubernetes API.
var namespaceResourceType = ResourceType{
	Version:    "v1",
	Kind:       "Namespace",
	Plural:     "namespaces",
	Namespaced: false,
}

// Janitor handles the cleanup of Kubernetes resources
type Janitor struct {
	cluster Cluster
	config  *Config
	cache   map[string]interface{}
}

// New creates a Janitor that works through the given Cluster.
func New(config *Config, cluster Cluster) *Janitor {
	return &Janitor{
		cluster: cluster,
		config:  config,
		cache:   make(map[string]interface{}),
	}
}

// debugLog logs a message if debug mode is enabled
func (j *Janitor) debugLog(format string, args ...interface{}) {
	if j.config.Debug {
		log.Printf("DEBUG: "+format, args...)
	}
}

// infoLog logs a message at the info level (always visible unless quiet mode is enabled)
func (j *Janitor) infoLog(format string, args ...interface{}) {
	if !j.config.Quiet {
		log.Printf("INFO: "+format, args...)
	}
}

// CleanUp performs one cleanup run
func (j *Janitor) CleanUp(ctx context.Context) error {
	j.debugLog("Starting cleanup run")

	resourceTypes, err := GetResourceTypes(j.cluster.Typed)
	if err != nil {
		return fmt.Errorf("failed to get resource types: %v", err)
	}

	j.debugLog("Found %d resource types", len(resourceTypes))

	counter := make(map[string]int)

	// First handle namespaces if included
	j.debugLog("Processing namespaces")
	if err := j.cleanupNamespaces(ctx, counter); err != nil {
		return fmt.Errorf("failed to cleanup namespaces: %v", err)
	}

	// Then handle other resources
	for _, resourceType := range resourceTypes {
		j.debugLog("Processing resource type: %s", resourceType.Kind)
		if err := j.cleanupResourceType(ctx, resourceType, counter); err != nil {
			log.Printf("Error cleaning up resource type %s: %v", resourceType.Kind, err)
			continue
		}
	}

	j.logCleanupSummary(counter)
	j.debugLog("Cleanup run completed")
	return nil
}

// cleanupResourceType handles cleanup for a specific resource type
func (j *Janitor) cleanupResourceType(ctx context.Context, resourceType ResourceType, counter map[string]int) error {
	// Skip if resource type is excluded
	if !j.shouldProcessResourceType(resourceType) {
		j.debugLog("Skipping excluded resource type: %s", resourceType.Kind)
		return nil
	}

	j.debugLog("Getting namespaces for resource type: %s", resourceType.Kind)
	// Get all namespaces
	namespaces, err := j.cluster.Typed.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list namespaces: %v", err)
	}

	// Process namespaced resources
	if resourceType.Namespaced {
		j.debugLog("Processing namespaced resources for type: %s", resourceType.Kind)

		// Collect all targets from all namespaces first
		var allTargets []Target

		for _, ns := range namespaces.Items {
			// Skip excluded namespaces
			if !j.shouldProcessNamespace(ns.Name) {
				j.debugLog("Skipping excluded namespace: %s", ns.Name)
				continue
			}

			j.debugLog("Listing resources of type %s in namespace %s", resourceType.Kind, ns.Name)
			targets, err := j.listTargets(ctx, resourceType, ns.Name)
			if err != nil {
				log.Printf("Error listing %s in namespace %s: %v", resourceType.Kind, ns.Name, err)
				continue
			}
			j.debugLog("Found %d resources of type %s in namespace %s", len(targets), resourceType.Kind, ns.Name)

			allTargets = append(allTargets, targets...)
		}

		j.processTargets(ctx, allTargets, counter)

	} else if j.config.IncludeClusterResources {
		// Process cluster-scoped resources if enabled
		j.debugLog("Processing cluster-scoped resources for type: %s", resourceType.Kind)
		targets, err := j.listTargets(ctx, resourceType, "")
		if err != nil {
			return fmt.Errorf("failed to list cluster-scoped %s: %v", resourceType.Kind, err)
		}
		j.debugLog("Found %d cluster-scoped resources of type %s", len(targets), resourceType.Kind)

		j.processTargets(ctx, targets, counter)
	}

	return nil
}

// shouldProcessResourceType checks if a resource type should be processed
func (j *Janitor) shouldProcessResourceType(resourceType ResourceType) bool {
	// Skip if resource type is explicitly excluded
	for _, excluded := range j.config.ExcludeResources {
		if excluded == resourceType.Plural {
			j.debugLog("Resource type %s is in exclude list", resourceType.Plural)
			return false
		}
	}

	// Check if resource type is included
	for _, included := range j.config.IncludeResources {
		if included == "all" || included == resourceType.Plural {
			j.debugLog("Resource type %s is included for processing", resourceType.Plural)
			return true
		}
	}

	j.debugLog("Resource type %s is not included for processing", resourceType.Plural)
	return false
}

// shouldProcessNamespace checks if a namespace should be processed
func (j *Janitor) shouldProcessNamespace(namespace string) bool {
	// Skip if namespace is explicitly excluded
	for _, excluded := range j.config.ExcludeNamespaces {
		if excluded == namespace {
			j.debugLog("Namespace %s is in exclude list", namespace)
			return false
		}
	}

	// Check if namespace is included
	for _, included := range j.config.IncludeNamespaces {
		if included == "all" || included == namespace {
			j.debugLog("Namespace %s is included for processing", namespace)
			return true
		}
	}

	j.debugLog("Namespace %s is not included for processing", namespace)
	return false
}

// listTargets lists every resource of the given type as a Target carrying that
// type, in one namespace or across the cluster when namespace is empty.
func (j *Janitor) listTargets(ctx context.Context, resourceType ResourceType, namespace string) ([]Target, error) {
	resource := j.cluster.Dynamic.Resource(resourceType.gvr())

	var lister dynamic.ResourceInterface = resource
	scope := "cluster-scoped"
	if namespace != "" {
		lister = resource.Namespace(namespace)
		scope = "namespace " + namespace
	}

	list, err := lister.List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list %s in %s: %v", resourceType.Kind, scope, err)
	}

	targets := make([]Target, 0, len(list.Items))
	for i := range list.Items {
		t, err := newTarget(&list.Items[i], resourceType)
		if err != nil {
			log.Printf("Error reading %s %s/%s: %v", resourceType.Kind,
				list.Items[i].GetNamespace(), list.Items[i].GetName(), err)
			continue
		}
		targets = append(targets, t)
	}

	return targets, nil
}

func (j *Janitor) handleResource(ctx context.Context, t Target, counter map[string]int) error {
	if !j.matchesResourceFilter(t) {
		j.debugLog("Resource %s does not match filters, skipping", t.describe())
		return nil
	}

	counter["resources-processed"]++

	now := time.Now()

	verdict, err := Decide(t, j.config, now, func() map[string]interface{} {
		return j.resourceContext(ctx, t)
	})
	if err != nil {
		return err
	}

	if verdict.Action == ActionNone {
		j.debugLog("%s: nothing to do (%s)", t.describe(), verdict.Source)
	} else {
		j.infoLog("%s: %s, deadline %s (%s)", t.describe(), verdict.Action,
			verdict.Deadline.Format(time.RFC3339), verdict.Source)
	}

	if err := j.Apply(ctx, t, verdict, now); err != nil {
		return err
	}

	if verdict.Action == ActionDelete {
		counter[t.GVR.Resource+"-deleted"]++
	}

	return nil
}

// resourceContext resolves the cluster-derived facts rules can test. A lookup
// failure degrades to an empty context rather than stopping the run, so a rule
// that tests it simply does not match.
func (j *Janitor) resourceContext(ctx context.Context, t Target) map[string]interface{} {
	data, err := j.getResourceContext(ctx, t)
	if err != nil {
		log.Printf("Warning: failed to get context for %s: %v", t.describe(), err)
		return map[string]interface{}{}
	}
	return data
}

func (j *Janitor) cleanupNamespaces(ctx context.Context, counter map[string]int) error {
	if !stringInSlice("namespaces", j.config.IncludeResources) &&
		!stringInSlice("all", j.config.IncludeResources) {
		j.debugLog("Namespaces not included in resources to process, skipping")
		return nil
	}

	j.debugLog("Listing all namespaces")
	namespaces, err := j.cluster.Typed.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list namespaces: %v", err)
	}
	j.debugLog("Found %d namespaces", len(namespaces.Items))

	// handleResource applies the same filter to every target, so there is no
	// second pass here.
	candidates := make([]Target, 0, len(namespaces.Items))
	for i := range namespaces.Items {
		t, err := newTarget(&namespaces.Items[i], namespaceResourceType)
		if err != nil {
			log.Printf("Error reading namespace %s: %v", namespaces.Items[i].Name, err)
			continue
		}
		candidates = append(candidates, t)
	}

	j.processTargets(ctx, candidates, counter)

	return nil
}

// processTargets processes targets one by one in serial order
func (j *Janitor) processTargets(ctx context.Context, targets []Target, counter map[string]int) {
	if len(targets) == 0 {
		return
	}

	j.debugLog("Processing %d resources serially", len(targets))

	alreadySeen := make(map[string]bool)

	for _, t := range targets {
		// Check for context cancellation before processing each resource
		select {
		case <-ctx.Done():
			j.debugLog("Context cancelled, stopping resource processing")
			return
		default:
		}

		key := t.describe()
		if alreadySeen[key] {
			j.debugLog("Skipping already processed resource: %s", key)
			continue
		}
		alreadySeen[key] = true

		j.debugLog("Processing resource: %s", key)

		if err := j.handleResource(ctx, t, counter); err != nil {
			log.Printf("Error handling %s: %v", key, err)
		}
	}

	j.debugLog("Finished processing resources")
}

func (j *Janitor) logCleanupSummary(counter map[string]int) {
	if j.config.Quiet {
		return
	}

	var stats []string
	for k, v := range counter {
		stats = append(stats, fmt.Sprintf("%s=%d", k, v))
	}

	log.Printf("Clean up run completed: %s", strings.Join(stats, ", "))

	if j.config.Debug {
		j.debugLog("Detailed counter values:")
		for k, v := range counter {
			j.debugLog("  %s: %d", k, v)
		}
	}
}

// matchesResourceFilter checks if a resource matches the configured filters
func (j *Janitor) matchesResourceFilter(t Target) bool {
	kind := t.Kind
	name := t.Name

	// A namespace is its own namespace for filtering purposes.
	namespace := t.Namespace
	if kind == "Namespace" {
		namespace = name
	}

	resourceType := t.GVR.Resource

	// Check if resource type is explicitly excluded
	for _, excluded := range j.config.ExcludeResources {
		if excluded == resourceType {
			return false
		}
	}

	// Check if resource type is included
	resourceIncluded := false
	for _, included := range j.config.IncludeResources {
		if included == "all" || included == resourceType {
			resourceIncluded = true
			break
		}
	}

	if !resourceIncluded {
		return false
	}

	// Handle namespaces specially
	if kind == "Namespace" {
		for _, excluded := range j.config.ExcludeNamespaces {
			if excluded == name {
				return false
			}
		}
		for _, included := range j.config.IncludeNamespaces {
			if included == "all" || included == name {
				return true
			}
		}
		return false
	}

	// Handle cluster-scoped vs namespaced resources
	if namespace == "" {
		return j.config.IncludeClusterResources
	}

	// Check namespace filters
	for _, excluded := range j.config.ExcludeNamespaces {
		if excluded == namespace {
			return false
		}
	}
	for _, included := range j.config.IncludeNamespaces {
		if included == "all" || included == namespace {
			return true
		}
	}

	return false
}
