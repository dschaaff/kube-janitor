package janitor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Janitor handles the cleanup of Kubernetes resources
type Janitor struct {
	client        kubernetes.Interface
	dynamicClient dynamic.Interface
	config        *Config
	cache         map[string]interface{}
	debug         bool
}

// New creates a new Janitor instance
func New(config *Config) (*Janitor, error) {
	// Create the Kubernetes client
	client, err := getKubeClient()
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes client: %v", err)
	}

	dynamicClient, err := getDynamicClient()
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %v", err)
	}

	return &Janitor{
		client:        client,
		dynamicClient: dynamicClient,
		config:        config,
		cache:         make(map[string]interface{}),
		debug:         config.Debug,
	}, nil
}

// getDynamicClient creates a new dynamic client for the Kubernetes cluster
func getDynamicClient() (dynamic.Interface, error) {
	var config *rest.Config
	var err error

	// Try in-cluster config first
	config, err = rest.InClusterConfig()
	if err != nil {
		// Fall back to kubeconfig
		kubeconfigPath := os.Getenv("KUBECONFIG")
		if kubeconfigPath == "" {
			// If KUBECONFIG is not set, use default location
			homeDir, err := os.UserHomeDir()
			if err == nil {
				kubeconfigPath = filepath.Join(homeDir, ".kube", "config")
			}
		}

		config, err = clientcmd.BuildConfigFromFlags("", kubeconfigPath)
		if err != nil {
			return nil, fmt.Errorf("failed to create config: %v (try setting KUBECONFIG environment variable)", err)
		}
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %v", err)
	}

	return dynamicClient, nil
}

// sendDeleteNotification sends a notification about upcoming resource deletion
func (j *Janitor) sendDeleteNotification(ctx context.Context, t Target, reason string, expiryTime time.Time) error {
	if j.config.DryRun {
		log.Printf("**DRY-RUN**: Would send delete notification for %s", t.describe())
		j.debugLog("Notification reason: %s, expiry time: %s", reason, expiryTime)
		return nil
	}

	if t.wasNotified() {
		return nil
	}

	contextName := os.Getenv("CONTEXT_NAME")
	prefix := ""
	if contextName != "" {
		prefix = "[" + contextName + "] "
	}

	message := fmt.Sprintf("%s%s will be deleted at %s (%s)",
		prefix,
		t.describe(),
		expiryTime.Format(time.RFC3339),
		reason)

	if err := j.createEvent(ctx, t, message, "DeleteNotification"); err != nil {
		return err
	}

	if err := SendWebhookNotification(message); err != nil {
		log.Printf("Failed to send webhook notification: %v", err)
	}

	// Flags the target as notified for the rest of this run only. Nothing writes
	// the annotation back to the cluster, so notifications re-fire on the next
	// run. Fixing that needs the "patch" verb, which deploy/rbac.yaml does not
	// grant.
	if t.Annotations != nil {
		t.Annotations[NotifiedAnnotation] = "yes"
	}

	return nil
}

// debugLog logs a message if debug mode is enabled
func (j *Janitor) debugLog(format string, args ...interface{}) {
	if j.debug {
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

	resourceTypes, err := GetResourceTypes(j.client)
	if err != nil {
		return fmt.Errorf("failed to get resource types: %v", err)
	}

	j.debugLog("Found %d resource types", len(resourceTypes))

	// Create maps for tracking
	counter := make(map[string]int)
	alreadySeen := make(map[string]bool)

	// First handle namespaces if included
	j.debugLog("Processing namespaces")
	if err := j.cleanupNamespaces(ctx, counter); err != nil {
		return fmt.Errorf("failed to cleanup namespaces: %v", err)
	}

	// Then handle other resources
	for _, resourceType := range resourceTypes {
		j.debugLog("Processing resource type: %s", resourceType.Kind)
		if err := j.cleanupResourceType(ctx, resourceType, counter, alreadySeen); err != nil {
			log.Printf("Error cleaning up resource type %s: %v", resourceType.Kind, err)
			continue
		}
	}

	j.logCleanupSummary(counter)
	j.debugLog("Cleanup run completed")
	return nil
}

// cleanupResourceType handles cleanup for a specific resource type
func (j *Janitor) cleanupResourceType(ctx context.Context, resourceType ResourceType, counter map[string]int, alreadySeen map[string]bool) error {
	// Skip if resource type is excluded
	if !j.shouldProcessResourceType(resourceType) {
		j.debugLog("Skipping excluded resource type: %s", resourceType.Kind)
		return nil
	}

	j.debugLog("Getting namespaces for resource type: %s", resourceType.Kind)
	// Get all namespaces
	namespaces, err := j.client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list namespaces: %v", err)
	}

	// Process namespaced resources
	if resourceType.Namespaced {
		j.debugLog("Processing namespaced resources for type: %s", resourceType.Kind)

		// Collect all resources from all namespaces first
		var allResources []metav1.Object

		for _, ns := range namespaces.Items {
			// Skip excluded namespaces
			if !j.shouldProcessNamespace(ns.Name) {
				j.debugLog("Skipping excluded namespace: %s", ns.Name)
				continue
			}

			j.debugLog("Listing resources of type %s in namespace %s", resourceType.Kind, ns.Name)
			resources, err := j.listNamespacedResources(ctx, resourceType, ns.Name)
			if err != nil {
				log.Printf("Error listing %s in namespace %s: %v", resourceType.Kind, ns.Name, err)
				continue
			}
			j.debugLog("Found %d resources of type %s in namespace %s", len(resources), resourceType.Kind, ns.Name)

			allResources = append(allResources, resources...)
		}

		// Process resources serially
		j.processResourcesSerially(ctx, allResources, counter)

	} else if j.config.IncludeClusterResources {
		// Process cluster-scoped resources if enabled
		j.debugLog("Processing cluster-scoped resources for type: %s", resourceType.Kind)
		resources, err := j.listClusterResources(ctx, resourceType)
		if err != nil {
			return fmt.Errorf("failed to list cluster-scoped %s: %v", resourceType.Kind, err)
		}
		j.debugLog("Found %d cluster-scoped resources of type %s", len(resources), resourceType.Kind)

		// Process resources serially
		j.processResourcesSerially(ctx, resources, counter)
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

func (j *Janitor) listNamespacedResources(ctx context.Context, resourceType ResourceType, namespace string) ([]metav1.Object, error) {
	gvr := schema.GroupVersionResource{
		Group:    resourceType.Group,
		Version:  resourceType.Version,
		Resource: resourceType.Plural,
	}

	list, err := j.dynamicClient.Resource(gvr).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list %s in namespace %s: %v", resourceType.Kind, namespace, err)
	}

	var resources []metav1.Object
	for _, item := range list.Items {
		// Convert unstructured.Unstructured to metav1.Object
		obj := item.DeepCopy()
		obj.SetKind(resourceType.Kind)
		obj.SetAPIVersion(fmt.Sprintf("%s/%s", resourceType.Group, resourceType.Version))
		resources = append(resources, obj)
	}

	return resources, nil
}

func (j *Janitor) listClusterResources(ctx context.Context, resourceType ResourceType) ([]metav1.Object, error) {
	gvr := schema.GroupVersionResource{
		Group:    resourceType.Group,
		Version:  resourceType.Version,
		Resource: resourceType.Plural,
	}

	list, err := j.dynamicClient.Resource(gvr).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list cluster-scoped %s: %v", resourceType.Kind, err)
	}

	var resources []metav1.Object
	for _, item := range list.Items {
		// Convert unstructured.Unstructured to metav1.Object
		obj := item.DeepCopy()
		obj.SetKind(resourceType.Kind)
		obj.SetAPIVersion(fmt.Sprintf("%s/%s", resourceType.Group, resourceType.Version))
		resources = append(resources, obj)
	}

	return resources, nil
}

// getKubeClient creates a new Kubernetes client
func getKubeClient() (kubernetes.Interface, error) {
	var config *rest.Config
	var err error

	// Try in-cluster config first
	config, err = rest.InClusterConfig()
	if err != nil {
		// Fall back to kubeconfig
		kubeconfigPath := os.Getenv("KUBECONFIG")
		if kubeconfigPath == "" {
			// If KUBECONFIG is not set, use default location
			homeDir, err := os.UserHomeDir()
			if err == nil {
				kubeconfigPath = filepath.Join(homeDir, ".kube", "config")
			}
		}

		config, err = clientcmd.BuildConfigFromFlags("", kubeconfigPath)
		if err != nil {
			return nil, fmt.Errorf("failed to create config: %v (try setting KUBECONFIG environment variable)", err)
		}
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %v", err)
	}

	return clientset, nil
}

// createEvent creates a Kubernetes event for the given resource
func (j *Janitor) createEvent(ctx context.Context, t Target, message string, reason string) error {
	if j.config.DryRun {
		log.Printf("**DRY-RUN**: Would create event: %s", message)
		return nil
	}

	// Determine event namespace - use fallback for cluster-scoped resources
	eventNamespace := t.Namespace
	if eventNamespace == "" {
		eventNamespace = "default"
	}

	now := time.Now()
	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "kube-janitor-",
			Namespace:    eventNamespace,
		},
		InvolvedObject: corev1.ObjectReference{
			APIVersion: t.APIVersion,
			Kind:       t.Kind,
			Name:       t.Name,
			Namespace:  t.Namespace,
			UID:        t.UID,
		},
		Reason:         reason,
		Message:        message,
		FirstTimestamp: metav1.NewTime(now),
		LastTimestamp:  metav1.NewTime(now),
		Count:          1,
		Type:           "Normal",
		Source: corev1.EventSource{
			Component: "kube-janitor",
		},
	}

	_, err := j.client.CoreV1().Events(eventNamespace).Create(ctx, event, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create event: %v", err)
	}

	return nil
}

// handleExpiry processes a resource's expiry annotation
func (j *Janitor) handleExpiry(ctx context.Context, t Target, counter map[string]int) error {
	expiry, ok := t.Annotations[ExpiryAnnotation]
	if !ok {
		return nil
	}

	expiryTime, err := ParseExpiry(expiry)
	if err != nil {
		return fmt.Errorf("invalid expiry value: %v", err)
	}

	if time.Now().After(expiryTime) {
		message := fmt.Sprintf("%s expired on %s and will be deleted (annotation %s is set)",
			t.describe(),
			expiry,
			ExpiryAnnotation)

		if err := j.createEvent(ctx, t, message, "ExpiryTimeReached"); err != nil {
			return fmt.Errorf("failed to create event: %v", err)
		}

		if err := j.deleteResource(ctx, t); err != nil {
			return fmt.Errorf("failed to delete resource: %v", err)
		}

		counter[t.GVR.Resource+"-deleted"]++
	} else if j.config.DeleteNotification > 0 {
		notificationTime := expiryTime.Add(-time.Duration(j.config.DeleteNotification) * time.Second)
		if time.Now().After(notificationTime) && !t.wasNotified() {
			if err := j.sendDeleteNotification(ctx, t, fmt.Sprintf("annotation %s is set", ExpiryAnnotation), expiryTime); err != nil {
				return fmt.Errorf("failed to send delete notification: %v", err)
			}
		}
	}

	return nil
}

// SendWebhookNotification sends a notification to a webhook
func SendWebhookNotification(message string) error {
	webhookURL := os.Getenv("WEBHOOK_URL")
	if webhookURL == "" {
		return nil
	}

	payload := WebhookMessage{
		Message: message,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal webhook payload: %v", err)
	}

	resp, err := http.Post(webhookURL, "application/json", bytes.NewBuffer(data))
	if err != nil {
		return fmt.Errorf("failed to send webhook: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned non-success status: %s", resp.Status)
	}

	return nil
}

// handleTTL processes a resource's TTL annotation or matching rules
func (j *Janitor) handleTTL(ctx context.Context, t Target, counter map[string]int) error {
	// Preserved as-is: a resource with no annotations at all never reaches rule
	// evaluation. That is a bug, fixed when the three decisions collapse.
	if t.Annotations == nil {
		j.debugLog("Resource %s/%s has no annotations", t.Namespace, t.Name)
		return nil
	}

	// Check for TTL annotation
	ttl, hasTTL := t.Annotations[TTLAnnotation]
	if !hasTTL {
		j.debugLog("Resource %s/%s has no TTL annotation, checking rules", t.Namespace, t.Name)
		// No TTL annotation, check if any rules match
		return j.handleRules(ctx, t, counter)
	}

	j.infoLog("Resource %s/%s has TTL annotation: %s", t.Namespace, t.Name, ttl)

	// Parse TTL
	ttlDuration, err := ParseTTL(ttl)
	if err != nil {
		return fmt.Errorf("invalid TTL value: %v", err)
	}

	// TTL of -1 means "forever", so skip
	if ttlDuration < 0 {
		j.debugLog("Resource %s/%s has unlimited TTL, skipping", t.Namespace, t.Name)
		return nil
	}

	deploymentTime := j.deploymentTime(t)

	// Calculate expiry time
	expiryTime := deploymentTime.Add(ttlDuration)
	j.infoLog("Resource %s/%s expires at: %s", t.Namespace, t.Name, expiryTime)

	// Check if resource has expired
	if time.Now().After(expiryTime) {
		j.infoLog("Resource %s/%s has expired, will be deleted", t.Namespace, t.Name)

		message := fmt.Sprintf("%s expired on %s and will be deleted (TTL %s from %s)",
			t.describe(),
			expiryTime.Format(time.RFC3339),
			ttl,
			deploymentTime.Format(time.RFC3339))

		if err := j.createEvent(ctx, t, message, "TTLExpired"); err != nil {
			return fmt.Errorf("failed to create event: %v", err)
		}

		if err := j.deleteResource(ctx, t); err != nil {
			return fmt.Errorf("failed to delete resource: %v", err)
		}

		counter[t.GVR.Resource+"-deleted"]++
	} else if j.config.DeleteNotification > 0 {
		// Send notification if configured and not already notified
		notificationTime := expiryTime.Add(-time.Duration(j.config.DeleteNotification) * time.Second)
		j.debugLog("Resource %s/%s notification time: %s", t.Namespace, t.Name, notificationTime)
		if time.Now().After(notificationTime) && !t.wasNotified() {
			j.infoLog("Sending delete notification for resource %s/%s", t.Namespace, t.Name)
			if err := j.sendDeleteNotification(ctx, t, fmt.Sprintf("TTL %s from %s", ttl, deploymentTime.Format(time.RFC3339)), expiryTime); err != nil {
				return fmt.Errorf("failed to send delete notification: %v", err)
			}
		}
	}

	return nil
}

// deploymentTime returns the moment a target's lifetime counts from: the
// configured deployment time annotation when present and parseable, and the
// creation timestamp otherwise.
func (j *Janitor) deploymentTime(t Target) time.Time {
	if j.config.DeploymentTimeAnnotation != "" {
		if raw, ok := t.Annotations[j.config.DeploymentTimeAnnotation]; ok {
			if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
				j.debugLog("Using deployment time from annotation: %s", parsed)
				return parsed
			}
		}
	}

	j.debugLog("Using creation timestamp as deployment time: %s", t.CreatedAt)
	return t.CreatedAt
}

// handleRules checks if any rules match the resource and applies TTL accordingly
func (j *Janitor) handleRules(ctx context.Context, t Target, counter map[string]int) error {
	if len(j.config.Rules) == 0 {
		j.debugLog("No rules configured, skipping rule evaluation for %s/%s", t.Namespace, t.Name)
		return nil
	}

	j.debugLog("Evaluating %d rules for resource %s/%s", len(j.config.Rules), t.Namespace, t.Name)

	// Get resource context
	resourceContext, err := j.getResourceContext(ctx, t)
	if err != nil {
		log.Printf("Warning: failed to get context for %s: %v", t.describe(), err)
		resourceContext = make(map[string]interface{})
	}

	// Check each rule
	for _, rule := range j.config.Rules {
		j.debugLog("Checking rule %s for resource %s/%s", rule.ID, t.Namespace, t.Name)
		if rule.Matches(t.Raw, resourceContext) {
			j.infoLog("Rule %s matched resource %s/%s", rule.ID, t.Namespace, t.Name)
			// Parse TTL
			ttlDuration, err := ParseTTL(rule.TTL)
			if err != nil {
				return fmt.Errorf("invalid TTL in rule %s: %v", rule.ID, err)
			}

			// TTL of -1 means "forever", so skip
			if ttlDuration < 0 {
				j.debugLog("Rule %s has unlimited TTL, skipping", rule.ID)
				continue
			}

			deploymentTime := j.deploymentTime(t)

			// Calculate expiry time
			expiryTime := deploymentTime.Add(ttlDuration)
			j.infoLog("Resource %s/%s expires at: %s based on rule %s",
				t.Namespace, t.Name, expiryTime, rule.ID)

			// Check if resource has expired
			if time.Now().After(expiryTime) {
				j.infoLog("Resource %s/%s has expired based on rule %s, will be deleted",
					t.Namespace, t.Name, rule.ID)

				message := fmt.Sprintf("%s expired on %s and will be deleted (rule %s, TTL %s from %s)",
					t.describe(),
					expiryTime.Format(time.RFC3339),
					rule.ID,
					rule.TTL,
					deploymentTime.Format(time.RFC3339))

				if err := j.createEvent(ctx, t, message, "RuleTTLExpired"); err != nil {
					return fmt.Errorf("failed to create event: %v", err)
				}

				if err := j.deleteResource(ctx, t); err != nil {
					return fmt.Errorf("failed to delete resource: %v", err)
				}

				counter[t.GVR.Resource+"-deleted"]++
				return nil
			} else if j.config.DeleteNotification > 0 {
				// Send notification if configured and not already notified
				notificationTime := expiryTime.Add(-time.Duration(j.config.DeleteNotification) * time.Second)
				j.debugLog("Rule %s notification time for resource %s/%s: %s",
					rule.ID, t.Namespace, t.Name, notificationTime)
				if time.Now().After(notificationTime) && !t.wasNotified() {
					j.infoLog("Sending delete notification for resource %s/%s based on rule %s",
						t.Namespace, t.Name, rule.ID)
					if err := j.sendDeleteNotification(ctx, t, fmt.Sprintf("rule %s, TTL %s from %s", rule.ID, rule.TTL, deploymentTime.Format(time.RFC3339)), expiryTime); err != nil {
						return fmt.Errorf("failed to send delete notification: %v", err)
					}
				}
			}

			// Only apply the first matching rule
			break
		}
	}

	return nil
}

func (j *Janitor) deleteResource(ctx context.Context, t Target) error {
	if j.config.DryRun {
		log.Printf("**DRY-RUN**: Would delete %s", t.describe())
		j.debugLog("Resource would be deleted with propagation policy: Background")
		return nil
	}

	deleteOptions := metav1.DeleteOptions{
		PropagationPolicy: &[]metav1.DeletionPropagation{metav1.DeletePropagationBackground}[0],
	}

	if t.Namespace != "" {
		j.infoLog("Deleting namespaced resource %s/%s", t.Namespace, t.Name)
		err := j.dynamicClient.Resource(t.GVR).Namespace(t.Namespace).Delete(ctx, t.Name, deleteOptions)
		if err != nil {
			return fmt.Errorf("failed to delete resource: %v", err)
		}
	} else {
		j.infoLog("Deleting cluster-scoped resource %s", t.Name)
		err := j.dynamicClient.Resource(t.GVR).Delete(ctx, t.Name, deleteOptions)
		if err != nil {
			return fmt.Errorf("failed to delete resource: %v", err)
		}
	}

	if j.config.WaitAfterDelete > 0 {
		j.infoLog("Waiting %d seconds after delete", j.config.WaitAfterDelete)
		time.Sleep(time.Duration(j.config.WaitAfterDelete) * time.Second)
	}

	return nil
}

// logCleanupSummary logs the summary of a cleanup run

func (j *Janitor) handleResource(ctx context.Context, resource metav1.Object, counter map[string]int, alreadySeen map[string]bool) error {
	t, err := newTarget(resource)
	if err != nil {
		return fmt.Errorf("failed to read resource: %v", err)
	}

	j.debugLog("Processing resource: %s", t.describe())

	if !j.matchesResourceFilter(resource) {
		j.debugLog("Resource %s does not match filters, skipping", t.describe())
		return nil
	}

	// Increment counter
	counter["resources-processed"]++

	j.debugLog("Checking TTL for resource: %s", t.describe())

	if err := j.handleTTL(ctx, t, counter); err != nil {
		return fmt.Errorf("failed to handle TTL: %v", err)
	}

	j.debugLog("Checking expiry for resource: %s", t.describe())
	if err := j.handleExpiry(ctx, t, counter); err != nil {
		return fmt.Errorf("failed to handle expiry: %v", err)
	}

	return nil
}

func (j *Janitor) cleanupNamespaces(ctx context.Context, counter map[string]int) error {
	if !stringInSlice("namespaces", j.config.IncludeResources) &&
		!stringInSlice("all", j.config.IncludeResources) {
		j.debugLog("Namespaces not included in resources to process, skipping")
		return nil
	}

	j.debugLog("Listing all namespaces")
	namespaces, err := j.client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list namespaces: %v", err)
	}
	j.debugLog("Found %d namespaces", len(namespaces.Items))

	// Filter namespaces that match our criteria
	var filteredNamespaces []metav1.Object
	for i := range namespaces.Items {
		ns := &namespaces.Items[i]
		if j.matchesResourceFilter(ns) {
			filteredNamespaces = append(filteredNamespaces, ns)
		} else {
			j.debugLog("Namespace %s does not match filters, skipping", ns.Name)
		}
	}

	// Process namespaces serially
	j.processResourcesSerially(ctx, filteredNamespaces, counter)

	return nil
}

// processResourcesSerially processes resources one by one in serial order
func (j *Janitor) processResourcesSerially(ctx context.Context, resources []metav1.Object, counter map[string]int) {
	if len(resources) == 0 {
		return
	}

	j.debugLog("Processing %d resources serially", len(resources))

	alreadySeen := make(map[string]bool)

	for _, resource := range resources {
		// Check for context cancellation before processing each resource
		select {
		case <-ctx.Done():
			j.debugLog("Context cancelled, stopping resource processing")
			return
		default:
		}

		// Check if already processed
		kind := "Unknown"
		if u, ok := resource.(*unstructured.Unstructured); ok {
			kind = u.GetKind()
		} else if _, ok := resource.(*corev1.Namespace); ok {
			kind = "Namespace"
		}
		key := fmt.Sprintf("%s/%s/%s", kind, resource.GetNamespace(), resource.GetName())

		if alreadySeen[key] {
			j.debugLog("Skipping already processed resource: %s", key)
			continue
		}
		alreadySeen[key] = true

		j.debugLog("Processing resource: %s", key)

		if err := j.handleResource(ctx, resource, counter, alreadySeen); err != nil {
			log.Printf("Error handling %s %s/%s: %v",
				kind, resource.GetNamespace(), resource.GetName(), err)
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

	if j.debug {
		j.debugLog("Detailed counter values:")
		for k, v := range counter {
			j.debugLog("  %s: %d", k, v)
		}
	}
}

// matchesResourceFilter checks if a resource matches the configured filters
func (j *Janitor) matchesResourceFilter(obj metav1.Object) bool {
	// Get kind using type assertion
	kind := "Unknown"
	if u, ok := obj.(*unstructured.Unstructured); ok {
		kind = u.GetKind()
	} else if ns, ok := obj.(*corev1.Namespace); ok {
		kind = "Namespace"
		_ = ns // avoid unused variable warning
	}

	namespace := obj.GetNamespace()
	name := obj.GetName()

	if kind == "Namespace" {
		namespace = name
	}

	resourceType := strings.ToLower(kind) + "s"

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
