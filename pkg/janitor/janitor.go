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
    "sync"
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
    counterMutex  sync.Mutex
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
func (j *Janitor) sendDeleteNotification(ctx context.Context, resource metav1.Object, reason string, expiryTime time.Time) error {
    if j.config.DryRun {
        // Use type assertion to get the kind
        kind := "Unknown"
        if u, ok := resource.(*unstructured.Unstructured); ok {
            kind = u.GetKind()
        }

        log.Printf("**DRY-RUN**: Would send delete notification for %s %s/%s",
            kind,
            resource.GetNamespace(),
            resource.GetName())
        j.debugLog("Notification reason: %s, expiry time: %s", reason, expiryTime)
        return nil
    }

    // Check if already notified
    annotations := resource.GetAnnotations()
    if annotations != nil {
        if _, notified := annotations[NotifiedAnnotation]; notified {
            return nil
        }
    }

    // Create notification message
    contextName := os.Getenv("CONTEXT_NAME")
    formattedTime := expiryTime.Format(time.RFC3339)

    // Get kind using type assertion
    kind := "Unknown"
    if u, ok := resource.(*unstructured.Unstructured); ok {
        kind = u.GetKind()
    }

    message := fmt.Sprintf("%s%s %s/%s will be deleted at %s (%s)",
        func() string {
            if contextName != "" {
                return "[" + contextName + "] "
            }
            return ""
        }(),
        kind,
        resource.GetNamespace(),
        resource.GetName(),
        formattedTime,
        reason)

    // Create event
    if err := j.createEvent(ctx, resource, message, "DeleteNotification"); err != nil {
        return err
    }

    // Send webhook notification
    if err := SendWebhookNotification(message); err != nil {
        log.Printf("Failed to send webhook notification: %v", err)
    }

    // Add notification flag
    if annotations == nil {
        annotations = make(map[string]string)
    }
    annotations[NotifiedAnnotation] = "yes"
    resource.SetAnnotations(annotations)

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
		log.Printf("INFO: " +format, args...)
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
        var resourcesMutex sync.Mutex

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

            resourcesMutex.Lock()
            allResources = append(allResources, resources...)
            resourcesMutex.Unlock()
        }

        // Process resources in parallel
        j.processResourcesInParallel(ctx, allResources, counter, alreadySeen)

    } else if j.config.IncludeClusterResources {
        // Process cluster-scoped resources if enabled
        j.debugLog("Processing cluster-scoped resources for type: %s", resourceType.Kind)
        resources, err := j.listClusterResources(ctx, resourceType)
        if err != nil {
            return fmt.Errorf("failed to list cluster-scoped %s: %v", resourceType.Kind, err)
        }
        j.debugLog("Found %d cluster-scoped resources of type %s", len(resources), resourceType.Kind)

        // Process resources in parallel
        j.processResourcesInParallel(ctx, resources, counter, alreadySeen)
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
func (j *Janitor) createEvent(ctx context.Context, resource metav1.Object, message string, reason string) error {
    if j.config.DryRun {
        log.Printf("**DRY-RUN**: Would create event: %s", message)
        return nil
    }

    // Get kind and API version using type assertion
    kind := "Unknown"
    apiVersion := "v1"

    if u, ok := resource.(*unstructured.Unstructured); ok {
        kind = u.GetKind()
        apiVersion = u.GetAPIVersion()
    }

    now := time.Now()
    event := &corev1.Event{
        ObjectMeta: metav1.ObjectMeta{
            GenerateName: "kube-janitor-",
            Namespace:    resource.GetNamespace(),
        },
        InvolvedObject: corev1.ObjectReference{
            APIVersion: apiVersion,
            Kind:       kind,
            Name:       resource.GetName(),
            Namespace:  resource.GetNamespace(),
            UID:        resource.GetUID(),
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

    _, err := j.client.CoreV1().Events(resource.GetNamespace()).Create(ctx, event, metav1.CreateOptions{})
    if err != nil {
        return fmt.Errorf("failed to create event: %v", err)
    }

    return nil
}


// handleExpiry processes a resource's expiry annotation
func (j *Janitor) handleExpiry(ctx context.Context, obj metav1.Object, counter map[string]int) error {
    annotations := obj.GetAnnotations()
    if annotations == nil {
        return nil
    }

    expiry, ok := annotations[ExpiryAnnotation]
    if !ok {
        return nil
    }

    expiryTime, err := time.Parse(time.RFC3339, expiry)
    if err != nil {
        return fmt.Errorf("invalid expiry value: %v", err)
    }

    // Get kind using type assertion
    kind := "Unknown"
    if u, ok := obj.(*unstructured.Unstructured); ok {
        kind = u.GetKind()
    }

    if time.Now().After(expiryTime) {
        message := fmt.Sprintf("%s %s/%s expired on %s and will be deleted (annotation %s is set)",
            kind,
            obj.GetNamespace(),
            obj.GetName(),
            expiry,
            ExpiryAnnotation)

        if err := j.createEvent(ctx, obj, message, "ExpiryTimeReached"); err != nil {
            return fmt.Errorf("failed to create event: %v", err)
        }

        if err := j.deleteResource(ctx, obj); err != nil {
            return fmt.Errorf("failed to delete resource: %v", err)
        }

        j.counterMutex.Lock()
        defer j.counterMutex.Unlock()
        resourceType := fmt.Sprintf("%ss", strings.ToLower(kind))
        counter[resourceType+"-deleted"]++
    } else if j.config.DeleteNotification > 0 {
        notificationTime := expiryTime.Add(-time.Duration(j.config.DeleteNotification) * time.Second)
        if time.Now().After(notificationTime) && !j.wasNotified(obj) {
            if err := j.sendDeleteNotification(ctx, obj, fmt.Sprintf("annotation %s is set", ExpiryAnnotation), expiryTime); err != nil {
                return fmt.Errorf("failed to send delete notification: %v", err)
            }
        }
    }

    return nil
}



// wasNotified checks if a delete notification was already sent


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
func (j *Janitor) handleTTL(ctx context.Context, obj metav1.Object, counter map[string]int) error {
    annotations := obj.GetAnnotations()
    if annotations == nil {
        j.debugLog("Resource %s/%s has no annotations", obj.GetNamespace(), obj.GetName())
        return nil
    }

    // Check for TTL annotation
    ttl, hasTTL := annotations[TTLAnnotation]
    if !hasTTL {
        j.debugLog("Resource %s/%s has no TTL annotation, checking rules", obj.GetNamespace(), obj.GetName())
        // No TTL annotation, check if any rules match
        return j.handleRules(ctx, obj, counter)
    }

    j.infoLog("Resource %s/%s has TTL annotation: %s", obj.GetNamespace(), obj.GetName(), ttl)

    // Parse TTL
    ttlDuration, err := ParseTTL(ttl)
    if err != nil {
        return fmt.Errorf("invalid TTL value: %v", err)
    }

    // TTL of -1 means "forever", so skip
    if ttlDuration < 0 {
        j.debugLog("Resource %s/%s has unlimited TTL, skipping", obj.GetNamespace(), obj.GetName())
        return nil
    }

    // Get deployment time
    var deploymentTime time.Time
    if j.config.DeploymentTimeAnnotation != "" {
        if deployTimeStr, ok := annotations[j.config.DeploymentTimeAnnotation]; ok {
            if t, err := time.Parse(time.RFC3339, deployTimeStr); err == nil {
                deploymentTime = t
                j.debugLog("Using deployment time from annotation: %s", deploymentTime)
            }
        }
    }

    // If no deployment time annotation or couldn't parse it, use creation timestamp
    if deploymentTime.IsZero() {
        deploymentTime = obj.GetCreationTimestamp().Time
        j.debugLog("Using creation timestamp as deployment time: %s", deploymentTime)
    }

    // Calculate expiry time
    expiryTime := deploymentTime.Add(ttlDuration)
    j.infoLog("Resource %s/%s expires at: %s", obj.GetNamespace(), obj.GetName(), expiryTime)

    // Check if resource has expired
    if time.Now().After(expiryTime) {
        j.infoLog("Resource %s/%s has expired, will be deleted", obj.GetNamespace(), obj.GetName())
        // Get kind using type assertion
        kind := "Unknown"
        if u, ok := obj.(*unstructured.Unstructured); ok {
            kind = u.GetKind()
        }

        message := fmt.Sprintf("%s %s/%s expired on %s and will be deleted (TTL %s from %s)",
            kind,
            obj.GetNamespace(),
            obj.GetName(),
            expiryTime.Format(time.RFC3339),
            ttl,
            deploymentTime.Format(time.RFC3339))

        if err := j.createEvent(ctx, obj, message, "TTLExpired"); err != nil {
            return fmt.Errorf("failed to create event: %v", err)
        }

        if err := j.deleteResource(ctx, obj); err != nil {
            return fmt.Errorf("failed to delete resource: %v", err)
        }

        resourceType := fmt.Sprintf("%ss", strings.ToLower(kind))
        counter[resourceType+"-deleted"]++
    } else if j.config.DeleteNotification > 0 {
        // Send notification if configured and not already notified
        notificationTime := expiryTime.Add(-time.Duration(j.config.DeleteNotification) * time.Second)
        j.debugLog("Resource %s/%s notification time: %s", obj.GetNamespace(), obj.GetName(), notificationTime)
        if time.Now().After(notificationTime) && !j.wasNotified(obj) {
            j.infoLog("Sending delete notification for resource %s/%s", obj.GetNamespace(), obj.GetName())
            if err := j.sendDeleteNotification(ctx, obj, fmt.Sprintf("TTL %s from %s", ttl, deploymentTime.Format(time.RFC3339)), expiryTime); err != nil {
                return fmt.Errorf("failed to send delete notification: %v", err)
            }
        }
    }

    return nil
}

// handleRules checks if any rules match the resource and applies TTL accordingly
func (j *Janitor) handleRules(ctx context.Context, obj metav1.Object, counter map[string]int) error {
    if len(j.config.Rules) == 0 {
        j.debugLog("No rules configured, skipping rule evaluation for %s/%s", obj.GetNamespace(), obj.GetName())
        return nil
    }

    // Convert resource to map for JMESPath evaluation
    resourceMap, err := j.objectToMap(obj)
    if err != nil {
        return fmt.Errorf("failed to convert resource to map: %v", err)
    }

    j.debugLog("Evaluating %d rules for resource %s/%s", len(j.config.Rules), obj.GetNamespace(), obj.GetName())

    // Get resource context
    context, err := j.getResourceContext(ctx, obj)
    if err != nil {
        // Get kind using type assertion
        kind := "Unknown"
        if u, ok := obj.(*unstructured.Unstructured); ok {
            kind = u.GetKind()
        }

        log.Printf("Warning: failed to get context for %s %s/%s: %v",
            kind, obj.GetNamespace(), obj.GetName(), err)
        context = make(map[string]interface{})
    }

    // Check each rule
    for _, rule := range j.config.Rules {
        j.debugLog("Checking rule %s for resource %s/%s", rule.ID, obj.GetNamespace(), obj.GetName())
        if rule.Matches(resourceMap, context) {
            j.infoLog("Rule %s matched resource %s/%s", rule.ID, obj.GetNamespace(), obj.GetName())
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

            // Get deployment time
            var deploymentTime time.Time
            if j.config.DeploymentTimeAnnotation != "" {
                annotations := obj.GetAnnotations()
                if annotations != nil {
                    if deployTimeStr, ok := annotations[j.config.DeploymentTimeAnnotation]; ok {
                        if t, err := time.Parse(time.RFC3339, deployTimeStr); err == nil {
                            deploymentTime = t
                            j.debugLog("Using deployment time from annotation: %s", deploymentTime)
                        }
                    }
                }
            }

            // If no deployment time annotation or couldn't parse it, use creation timestamp
            if deploymentTime.IsZero() {
                deploymentTime = obj.GetCreationTimestamp().Time
                j.debugLog("Using creation timestamp as deployment time: %s", deploymentTime)
            }

            // Calculate expiry time
            expiryTime := deploymentTime.Add(ttlDuration)
            j.infoLog("Resource %s/%s expires at: %s based on rule %s",
                obj.GetNamespace(), obj.GetName(), expiryTime, rule.ID)

            // Check if resource has expired
            if time.Now().After(expiryTime) {
                j.infoLog("Resource %s/%s has expired based on rule %s, will be deleted",
                    obj.GetNamespace(), obj.GetName(), rule.ID)
                // Get kind using type assertion
                kind := "Unknown"
                if u, ok := obj.(*unstructured.Unstructured); ok {
                    kind = u.GetKind()
                }

                message := fmt.Sprintf("%s %s/%s expired on %s and will be deleted (rule %s, TTL %s from %s)",
                    kind,
                    obj.GetNamespace(),
                    obj.GetName(),
                    expiryTime.Format(time.RFC3339),
                    rule.ID,
                    rule.TTL,
                    deploymentTime.Format(time.RFC3339))

                if err := j.createEvent(ctx, obj, message, "RuleTTLExpired"); err != nil {
                    return fmt.Errorf("failed to create event: %v", err)
                }

                if err := j.deleteResource(ctx, obj); err != nil {
                    return fmt.Errorf("failed to delete resource: %v", err)
                }

                j.counterMutex.Lock()
                defer j.counterMutex.Unlock()
                resourceType := fmt.Sprintf("%ss", strings.ToLower(kind))
                counter[resourceType+"-deleted"]++
                return nil
            } else if j.config.DeleteNotification > 0 {
                // Send notification if configured and not already notified
                notificationTime := expiryTime.Add(-time.Duration(j.config.DeleteNotification) * time.Second)
                j.debugLog("Rule %s notification time for resource %s/%s: %s",
                    rule.ID, obj.GetNamespace(), obj.GetName(), notificationTime)
                if time.Now().After(notificationTime) && !j.wasNotified(obj) {
                    j.infoLog("Sending delete notification for resource %s/%s based on rule %s",
                        obj.GetNamespace(), obj.GetName(), rule.ID)
                    if err := j.sendDeleteNotification(ctx, obj, fmt.Sprintf("rule %s, TTL %s from %s", rule.ID, rule.TTL, deploymentTime.Format(time.RFC3339)), expiryTime); err != nil {
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

// objectToMap converts a Kubernetes object to a map for JMESPath evaluation
func (j *Janitor) objectToMap(obj metav1.Object) (map[string]interface{}, error) {
    // For unstructured objects, we can just use the Object field
    if u, ok := obj.(*unstructured.Unstructured); ok {
        return u.Object, nil
    }

    // For other objects, we need to convert them to JSON and then unmarshal to a map
    data, err := json.Marshal(obj)
    if err != nil {
        return nil, fmt.Errorf("failed to marshal object: %v", err)
    }

    var result map[string]interface{}
    if err := json.Unmarshal(data, &result); err != nil {
        return nil, fmt.Errorf("failed to unmarshal object: %v", err)
    }

    return result, nil
}

func (j *Janitor) deleteResource(ctx context.Context, obj metav1.Object) error {
    if j.config.DryRun {
        // Get kind using type assertion
        kind := "Unknown"
        if u, ok := obj.(*unstructured.Unstructured); ok {
            kind = u.GetKind()
        }

        log.Printf("**DRY-RUN**: Would delete %s %s/%s",
            kind,
            obj.GetNamespace(),
            obj.GetName())
        j.debugLog("Resource would be deleted with propagation policy: Background")
        return nil
    }

    // Get GVR using type assertion
    var gvr schema.GroupVersionResource

    if u, ok := obj.(*unstructured.Unstructured); ok {
        gvk := u.GroupVersionKind()
        gvr = schema.GroupVersionResource{
            Group:    gvk.Group,
            Version:  gvk.Version,
            Resource: strings.ToLower(gvk.Kind) + "s",
        }
    } else {
        // Default to core/v1 if we can't determine the GVR
        gvr = schema.GroupVersionResource{
            Group:    "",
            Version:  "v1",
            Resource: strings.ToLower("Unknown") + "s",
        }
    }

    deleteOptions := metav1.DeleteOptions{
        PropagationPolicy: &[]metav1.DeletionPropagation{metav1.DeletePropagationBackground}[0],
    }

    if obj.GetNamespace() != "" {
        j.infoLog("Deleting namespaced resource %s/%s", obj.GetNamespace(), obj.GetName())
        err := j.dynamicClient.Resource(gvr).Namespace(obj.GetNamespace()).Delete(ctx, obj.GetName(), deleteOptions)
        if err != nil {
            return fmt.Errorf("failed to delete resource: %v", err)
        }
    } else {
        j.infoLog("Deleting cluster-scoped resource %s", obj.GetName())
        err := j.dynamicClient.Resource(gvr).Delete(ctx, obj.GetName(), deleteOptions)
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

func (j *Janitor) wasNotified(obj metav1.Object) bool {
    annotations := obj.GetAnnotations()
    if annotations == nil {
        return false
    }
    _, notified := annotations[NotifiedAnnotation]
    return notified
}




// logCleanupSummary logs the summary of a cleanup run

func (j *Janitor) handleResource(ctx context.Context, resource metav1.Object, counter map[string]int, alreadySeen map[string]bool) error {
    // Get kind using type assertion
    kind := "Unknown"
    if u, ok := resource.(*unstructured.Unstructured); ok {
        kind = u.GetKind()
    }

    j.debugLog("Processing resource: %s/%s/%s", kind, resource.GetNamespace(), resource.GetName())

    if !j.matchesResourceFilter(resource) {
        j.debugLog("Resource %s/%s/%s does not match filters, skipping",
            kind, resource.GetNamespace(), resource.GetName())
        return nil
    }

    // Increment counter with mutex protection
    j.counterMutex.Lock()
    defer j.counterMutex.Unlock()
    counter["resources-processed"]++

    j.debugLog("Checking TTL for resource: %s/%s/%s",
        kind, resource.GetNamespace(), resource.GetName())

    if err := j.handleTTL(ctx, resource, counter); err != nil {
        return fmt.Errorf("failed to handle TTL: %v", err)
    }

    j.debugLog("Checking expiry for resource: %s/%s/%s",
        kind, resource.GetNamespace(), resource.GetName())
    if err := j.handleExpiry(ctx, resource, counter); err != nil {
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

    // Process namespaces in parallel
    j.processResourcesInParallel(ctx, filteredNamespaces, counter, make(map[string]bool))

    return nil
}

// processResourcesInParallel processes resources in parallel using worker pool
func (j *Janitor) processResourcesInParallel(ctx context.Context, resources []metav1.Object, counter map[string]int, alreadySeen map[string]bool) {
    if len(resources) == 0 {
        return
    }

    // Use a mutex to protect alreadySeen map
    var alreadySeenMutex sync.Mutex

    // Create a wait group to wait for all workers to finish
    var wg sync.WaitGroup

    // Create a channel for resources
    resourceCh := make(chan metav1.Object, len(resources))

    // Determine number of workers
    numWorkers := j.config.Parallelism
    if numWorkers <= 0 {
        numWorkers = 1
    }

    j.debugLog("Processing %d resources with %d workers", len(resources), numWorkers)

    // Start workers
    for i := 0; i < numWorkers; i++ {
        wg.Add(1)
        go func(workerID int) {
            defer wg.Done()
            j.debugLog("Worker %d started", workerID)

            for resource := range resourceCh {
                // Check if already processed
                alreadySeenMutex.Lock()
                kind := "Unknown"
                if u, ok := resource.(*unstructured.Unstructured); ok {
                    kind = u.GetKind()
                }
                key := fmt.Sprintf("%s/%s/%s", kind, resource.GetNamespace(), resource.GetName())
                seen := alreadySeen[key]
                if !seen {
                    alreadySeen[key] = true
                }
                alreadySeenMutex.Unlock()

                if seen {
                    j.debugLog("Worker %d: Skipping already processed resource: %s", workerID, key)
                    continue
                }

                j.debugLog("Worker %d: Processing resource: %s", workerID, key)

                if err := j.handleResource(ctx, resource, counter, alreadySeen); err != nil {
                    log.Printf("Worker %d: Error handling %s %s/%s: %v",
                        workerID, kind, resource.GetNamespace(), resource.GetName(), err)
                }
            }

            j.debugLog("Worker %d finished", workerID)
        }(i)
    }

    // Send resources to channel
    for _, resource := range resources {
        resourceCh <- resource
    }

    // Close channel and wait for workers to finish
    close(resourceCh)
    wg.Wait()
}

func (j *Janitor) logCleanupSummary(counter map[string]int) {
    if j.config.Quiet {
        return
    }

    j.counterMutex.Lock()
    defer j.counterMutex.Unlock()
    
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





