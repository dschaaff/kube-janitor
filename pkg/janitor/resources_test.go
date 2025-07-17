package janitor

import (
	"testing"
)

func TestGetResourceTypes(t *testing.T) {
	// Skip this test as the fake client doesn't properly support the discovery API
	t.Skip("Skipping as fake client doesn't properly support discovery API")
}

func TestFilterDeprecatedAPIs(t *testing.T) {
	tests := []struct {
		name           string
		resourceTypes  map[string]ResourceType
		expectedKeys   []string
		unexpectedKeys []string
	}{
		{
			name: "removes endpoints when endpointslices exist",
			resourceTypes: map[string]ResourceType{
				"v1/endpoints": {
					Group:      "",
					Version:    "v1",
					Kind:       "Endpoints",
					Plural:     "endpoints",
					Namespaced: true,
				},
				"discovery.k8s.io/v1/endpointslices": {
					Group:      "discovery.k8s.io",
					Version:    "v1",
					Kind:       "EndpointSlice",
					Plural:     "endpointslices",
					Namespaced: true,
				},
				"v1/pods": {
					Group:      "",
					Version:    "v1",
					Kind:       "Pod",
					Plural:     "pods",
					Namespaced: true,
				},
			},
			expectedKeys:   []string{"discovery.k8s.io/v1/endpointslices", "v1/pods"},
			unexpectedKeys: []string{"v1/endpoints"},
		},
		{
			name: "keeps endpoints when endpointslices do not exist",
			resourceTypes: map[string]ResourceType{
				"v1/endpoints": {
					Group:      "",
					Version:    "v1",
					Kind:       "Endpoints",
					Plural:     "endpoints",
					Namespaced: true,
				},
				"v1/pods": {
					Group:      "",
					Version:    "v1",
					Kind:       "Pod",
					Plural:     "pods",
					Namespaced: true,
				},
			},
			expectedKeys:   []string{"v1/endpoints", "v1/pods"},
			unexpectedKeys: []string{},
		},
		{
			name: "no changes when neither endpoints nor endpointslices exist",
			resourceTypes: map[string]ResourceType{
				"v1/pods": {
					Group:      "",
					Version:    "v1",
					Kind:       "Pod",
					Plural:     "pods",
					Namespaced: true,
				},
				"v1/services": {
					Group:      "",
					Version:    "v1",
					Kind:       "Service",
					Plural:     "services",
					Namespaced: true,
				},
			},
			expectedKeys:   []string{"v1/pods", "v1/services"},
			unexpectedKeys: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a copy of the map to avoid modifying the test data
			resourceTypesMap := make(map[string]ResourceType)
			for k, v := range tt.resourceTypes {
				resourceTypesMap[k] = v
			}

			filterDeprecatedAPIs(resourceTypesMap)

			// Check that expected keys are present
			for _, key := range tt.expectedKeys {
				if _, exists := resourceTypesMap[key]; !exists {
					t.Errorf("Expected key %s to exist in resourceTypesMap", key)
				}
			}

			// Check that unexpected keys are not present
			for _, key := range tt.unexpectedKeys {
				if _, exists := resourceTypesMap[key]; exists {
					t.Errorf("Expected key %s to not exist in resourceTypesMap", key)
				}
			}
		})
	}
}
