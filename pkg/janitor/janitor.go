package janitor

import (
	"context"
	"fmt"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Janitor handles the cleanup of Kubernetes resources
type Janitor struct {
	cluster Cluster
	config  *Config
	log     *Logger
	cache   map[string]interface{}
}

// New creates a Janitor that works through the given Cluster, reporting what it
// does through the given Logger.
//
// The Logger is passed in rather than built here, so that everything one
// process writes goes through one Logger and a test can read back what a run
// said.
func New(config *Config, cluster Cluster, log *Logger) *Janitor {
	return &Janitor{
		cluster: cluster,
		config:  config,
		log:     log,
		cache:   make(map[string]interface{}),
	}
}

// CleanUp performs one cleanup run.
//
// What the run considers is settled up front by the Selector, so the loop below
// only lists and acts. Every resource is reached through exactly one Listing.
func (j *Janitor) CleanUp(ctx context.Context) error {
	j.log.Debugf("Starting cleanup run")

	resourceTypes, err := GetResourceTypes(j.cluster.Typed)
	if err != nil {
		return fmt.Errorf("failed to get resource types: %v", err)
	}
	j.log.Debugf("Found %d resource types", len(resourceTypes))

	namespaces, err := j.namespaceNames(ctx)
	if err != nil {
		return err
	}
	j.log.Debugf("Found %d namespaces", len(namespaces))

	sel := newSelector(j.config)
	listings := sel.listings(resourceTypes, namespaces)
	j.log.Debugf("Considering %d listings", len(listings))

	counter := make(map[string]int)

	for _, l := range listings {
		if ctx.Err() != nil {
			j.log.Debugf("Context cancelled, stopping the run")
			break
		}

		targets, err := j.listTargets(ctx, l)
		if err != nil {
			j.log.Errorf("Error listing %s in namespace %q: %v", l.Type.Plural, l.Namespace, err)
			continue
		}
		j.log.Debugf("Found %d %s in namespace %q", len(targets), l.Type.Plural, l.Namespace)

		j.processTargets(ctx, sel, targets, counter)
	}

	j.logCleanupSummary(counter)
	j.log.Debugf("Cleanup run completed")
	return nil
}

// namespaceNames lists the namespaces the cluster holds. A run reads them once
// to plan with, rather than once per Resource type, because the Selector needs
// all of them before it can plan any listing.
func (j *Janitor) namespaceNames(ctx context.Context) ([]string, error) {
	list, err := j.cluster.Typed.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list namespaces: %v", err)
	}

	names := make([]string, 0, len(list.Items))
	for i := range list.Items {
		names = append(names, list.Items[i].Name)
	}

	return names, nil
}

// listTargets lists every resource the listing names as a Target carrying the
// Resource type it was listed as.
func (j *Janitor) listTargets(ctx context.Context, l listing) ([]Target, error) {
	list, err := j.cluster.Dynamic.Resource(l.Type.gvr()).Namespace(l.Namespace).
		List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	targets := make([]Target, 0, len(list.Items))
	for i := range list.Items {
		targets = append(targets, newTarget(&list.Items[i], l.Type))
	}

	return targets, nil
}

// processTargets processes targets one by one in serial order, skipping those the
// Selector does not admit.
func (j *Janitor) processTargets(ctx context.Context, sel *selector, targets []Target, counter map[string]int) {
	for _, t := range targets {
		if ctx.Err() != nil {
			j.log.Debugf("Context cancelled, stopping resource processing")
			return
		}

		if !sel.admits(t) {
			j.log.Debugf("Resource %s is not considered by this run, skipping", t.describe())
			continue
		}

		if err := j.handleResource(ctx, t, counter); err != nil {
			j.log.Errorf("Error handling %s: %v", t.describe(), err)
		}
	}
}

func (j *Janitor) handleResource(ctx context.Context, t Target, counter map[string]int) error {
	counter["resources-processed"]++

	now := time.Now()

	verdict, err := Decide(t, j.config, now, func() map[string]interface{} {
		return j.resourceContext(ctx, t)
	})
	if err != nil {
		return err
	}

	if verdict.Action == ActionNone {
		j.log.Debugf("%s: nothing to do (%s)", t.describe(), verdict.Source)
	} else {
		j.log.Infof("%s: %s, deadline %s (%s)", t.describe(), verdict.Action,
			verdict.Deadline.Format(time.RFC3339), verdict.Source)
	}

	if err := j.Apply(ctx, t, verdict, now); err != nil {
		return err
	}

	if verdict.Action == ActionDelete {
		counter[t.plural()+"-deleted"]++
	}

	return nil
}

// resourceContext resolves the cluster-derived facts rules can test. A lookup
// failure degrades to an empty context rather than stopping the run, so a rule
// that tests it simply does not match.
func (j *Janitor) resourceContext(ctx context.Context, t Target) map[string]interface{} {
	data, err := j.getResourceContext(ctx, t)
	if err != nil {
		j.log.Warnf("failed to get context for %s: %v", t.describe(), err)
		return map[string]interface{}{}
	}
	return data
}

// logCleanupSummary reports what the run did. The Logger decides which of these
// lines are written, so nothing here checks whether the run is quiet.
func (j *Janitor) logCleanupSummary(counter map[string]int) {
	var stats []string
	for k, v := range counter {
		stats = append(stats, fmt.Sprintf("%s=%d", k, v))
	}

	j.log.Infof("Clean up run completed: %s", strings.Join(stats, ", "))

	for k, v := range counter {
		j.log.Debugf("  %s: %d", k, v)
	}
}
