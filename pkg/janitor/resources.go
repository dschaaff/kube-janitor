package janitor

import (
	"fmt"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
)

// resourceType is a kind as the cluster's discovery reports it, carrying the
// plural a Target is listed and deleted through.
type resourceType struct {
	Group      string
	Version    string
	Kind       string
	Plural     string
	Namespaced bool
}

func (rt resourceType) groupVersion() schema.GroupVersion {
	return schema.GroupVersion{Group: rt.Group, Version: rt.Version}
}

// apiVersion renders the group and version the way a Kubernetes object reports
// it: "v1" for the core group, "group/version" for every other.
func (rt resourceType) apiVersion() string {
	return rt.groupVersion().String()
}

// gvr is the resource a target of this type is listed and deleted through.
func (rt resourceType) gvr() schema.GroupVersionResource {
	return rt.groupVersion().WithResource(rt.Plural)
}

// getResourceTypes returns all available resource types in the cluster
func getResourceTypes(client kubernetes.Interface) ([]resourceType, error) {
	resourceTypesMap := make(map[string]resourceType)

	// Get server resources for core API group
	resources, err := client.Discovery().ServerResourcesForGroupVersion("v1")
	if err != nil {
		return nil, fmt.Errorf("failed to get core API resources: %v", err)
	}

	for _, r := range resources.APIResources {
		if strings.Contains(r.Name, "/") || !slices.Contains(r.Verbs, "delete") {
			continue
		}

		key := fmt.Sprintf("v1/%s", r.Name)
		resourceTypesMap[key] = resourceType{
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
			if strings.Contains(r.Name, "/") || !slices.Contains(r.Verbs, "delete") {
				continue
			}

			key := fmt.Sprintf("%s/%s", version.GroupVersion, r.Name)
			resourceTypesMap[key] = resourceType{
				Group:      group.Name,
				Version:    version.Version,
				Kind:       r.Kind,
				Plural:     r.Name,
				Namespaced: r.Namespaced,
			}
		}
	}

	// Remove deprecated APIs when newer alternatives exist
	filterDeprecatedAPIs(resourceTypesMap)

	// Convert map to slice
	resourceTypes := make([]resourceType, 0, len(resourceTypesMap))
	for _, rt := range resourceTypesMap {
		resourceTypes = append(resourceTypes, rt)
	}

	return resourceTypes, nil
}

// filterDeprecatedAPIs removes deprecated API resources when newer alternatives exist
func filterDeprecatedAPIs(resourceTypesMap map[string]resourceType) {
	// Remove v1/endpoints if discovery.k8s.io/v1/endpointslices exists
	if _, hasEndpointSlices := resourceTypesMap["discovery.k8s.io/v1/endpointslices"]; hasEndpointSlices {
		delete(resourceTypesMap, "v1/endpoints")
	}
}
