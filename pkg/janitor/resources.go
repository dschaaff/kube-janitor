package janitor

import (
    "fmt"
    "k8s.io/client-go/kubernetes"
    "strings"
)

// ResourceType represents a Kubernetes API resource type
type ResourceType struct {
    Group      string
    Version    string
    Kind       string
    Plural     string
    Namespaced bool
}

// GetResourceTypes returns all available resource types in the cluster
func GetResourceTypes(client kubernetes.Interface) ([]ResourceType, error) {
    resourceTypesMap := make(map[string]ResourceType)

    // Get server resources for core API group
    resources, err := client.Discovery().ServerResourcesForGroupVersion("v1")
    if err != nil {
        return nil, fmt.Errorf("failed to get core API resources: %v", err)
    }

    for _, r := range resources.APIResources {
        if strings.Contains(r.Name, "/") || !stringInSlice("delete", r.Verbs) {
            continue
        }

        key := fmt.Sprintf("v1/%s", r.Name)
        resourceTypesMap[key] = ResourceType{
            Group:      "",
            Version:    "v1",
            Kind:       r.Kind,
            Plural:     r.Name,
            Namespaced: r.Namespaced,
        }
    }

    // Get server API groups
    groups, err := client.Discovery().ServerGroups()
    if err != nil {
        return nil, fmt.Errorf("failed to get API groups: %v", err)
    }

    for _, group := range groups.Groups {
        version := group.PreferredVersion
        resources, err := client.Discovery().ServerResourcesForGroupVersion(version.GroupVersion)
        if err != nil {
            continue
        }

        for _, r := range resources.APIResources {
            if strings.Contains(r.Name, "/") || !stringInSlice("delete", r.Verbs) {
                continue
            }

            key := fmt.Sprintf("%s/%s", version.GroupVersion, r.Name)
            resourceTypesMap[key] = ResourceType{
                Group:      group.Name,
                Version:    version.Version,
                Kind:       r.Kind,
                Plural:     r.Name,
                Namespaced: r.Namespaced,
            }
        }
    }

    // Convert map to slice
    resourceTypes := make([]ResourceType, 0, len(resourceTypesMap))
    for _, rt := range resourceTypesMap {
        resourceTypes = append(resourceTypes, rt)
    }

    return resourceTypes, nil
}

func stringInSlice(str string, slice []string) bool {
    for _, s := range slice {
        if s == str {
            return true
        }
    }
    return false
}
