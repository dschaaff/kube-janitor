package janitor

import (
	"context"
	"flag"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes/fake"
)

func TestConfigFlagParsing(t *testing.T) {
	tests := []struct {
		name                  string
		args                  []string
		expectedIncludeResources []string
		expectedExcludeResources []string
		expectedIncludeNamespaces []string
		expectedExcludeNamespaces []string
	}{
		{
			name: "default flags",
			args: []string{},
			expectedIncludeResources: []string{"all"},
			expectedExcludeResources: []string{"events", "controllerrevisions", "endpoints"},
			expectedIncludeNamespaces: []string{"all"},
			expectedExcludeNamespaces: []string{"kube-system"},
		},
		{
			name: "custom include resources",
			args: []string{"-include-resources", "pods,services"},
			expectedIncludeResources: []string{"pods", "services"},
			expectedExcludeResources: []string{"events", "controllerrevisions", "endpoints"},
			expectedIncludeNamespaces: []string{"all"},
			expectedExcludeNamespaces: []string{"kube-system"},
		},
		{
			name: "custom include namespaces",
			args: []string{"-include-namespaces", "default,test"},
			expectedIncludeResources: []string{"all"},
			expectedExcludeResources: []string{"events", "controllerrevisions", "endpoints"},
			expectedIncludeNamespaces: []string{"default", "test"},
			expectedExcludeNamespaces: []string{"kube-system"},
		},
		{
			name: "custom exclude namespaces",
			args: []string{"-exclude-namespaces", "kube-system,kube-public"},
			expectedIncludeResources: []string{"all"},
			expectedExcludeResources: []string{"events", "controllerrevisions", "endpoints"},
			expectedIncludeNamespaces: []string{"all"},
			expectedExcludeNamespaces: []string{"kube-system", "kube-public"},
		},
		{
			name: "single namespace include",
			args: []string{"-include-namespaces", "test-namespace"},
			expectedIncludeResources: []string{"all"},
			expectedExcludeResources: []string{"events", "controllerrevisions", "endpoints"},
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

func TestNamespaceCleanupWithTTL(t *testing.T) {
	tests := []struct {
		name                  string
		includeNamespaces     []string
		excludeNamespaces     []string
		includeResources      []string
		namespaces            []corev1.Namespace
		expectProcessed       []string
		expectSkipped         []string
	}{
		{
			name:              "specific namespace with TTL",
			includeNamespaces: []string{"test-namespace"},
			excludeNamespaces: []string{"kube-system"},
			includeResources:  []string{"namespaces"},
			namespaces: []corev1.Namespace{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-namespace",
						CreationTimestamp: metav1.Time{
							Time: time.Now().Add(-3 * time.Hour),
						},
						Annotations: map[string]string{
							TTLAnnotation: "2h",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "other-namespace",
						CreationTimestamp: metav1.Time{
							Time: time.Now().Add(-3 * time.Hour),
						},
						Annotations: map[string]string{
							TTLAnnotation: "2h",
						},
					},
				},
			},
			expectProcessed: []string{"test-namespace"},
			expectSkipped:   []string{"other-namespace"},
		},
		{
			name:              "all namespaces with TTL",
			includeNamespaces: []string{"all"},
			excludeNamespaces: []string{"kube-system"},
			includeResources:  []string{"namespaces"},
			namespaces: []corev1.Namespace{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-namespace",
						CreationTimestamp: metav1.Time{
							Time: time.Now().Add(-3 * time.Hour),
						},
						Annotations: map[string]string{
							TTLAnnotation: "2h",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "another-namespace",
						CreationTimestamp: metav1.Time{
							Time: time.Now().Add(-3 * time.Hour),
						},
						Annotations: map[string]string{
							TTLAnnotation: "2h",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "kube-system",
						CreationTimestamp: metav1.Time{
							Time: time.Now().Add(-3 * time.Hour),
						},
						Annotations: map[string]string{
							TTLAnnotation: "2h",
						},
					},
				},
			},
			expectProcessed: []string{"test-namespace", "another-namespace"},
			expectSkipped:   []string{"kube-system"},
		},
		{
			name:              "exclude specific namespace",
			includeNamespaces: []string{"all"},
			excludeNamespaces: []string{"kube-system", "production"},
			includeResources:  []string{"namespaces"},
			namespaces: []corev1.Namespace{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-namespace",
						CreationTimestamp: metav1.Time{
							Time: time.Now().Add(-3 * time.Hour),
						},
						Annotations: map[string]string{
							TTLAnnotation: "2h",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "production",
						CreationTimestamp: metav1.Time{
							Time: time.Now().Add(-3 * time.Hour),
						},
						Annotations: map[string]string{
							TTLAnnotation: "2h",
						},
					},
				},
			},
			expectProcessed: []string{"test-namespace"},
			expectSkipped:   []string{"production"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create fake clientset
			clientset := fake.NewSimpleClientset()
			
			// Create test namespaces
			for _, ns := range tt.namespaces {
				_, err := clientset.CoreV1().Namespaces().Create(context.Background(), &ns, metav1.CreateOptions{})
				if err != nil {
					t.Fatalf("Failed to create namespace %s: %v", ns.Name, err)
				}
			}

			// Create janitor with specific config
			config := &Config{
				IncludeNamespaces: tt.includeNamespaces,
				ExcludeNamespaces: tt.excludeNamespaces,
				IncludeResources:  tt.includeResources,
				DryRun:            true,
				Parallelism:       1,
			}

			j := &Janitor{
				client: clientset,
				config: config,
				cache:  make(map[string]interface{}),
			}

			// Track which namespaces were processed by checking the matchesResourceFilter
			for _, ns := range tt.namespaces {
				// Create an unstructured object to mimic how namespace objects are handled in the real code
				nsUnstructured := &unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "Namespace",
						"metadata": map[string]interface{}{
							"name":              ns.Name,
							"creationTimestamp": ns.CreationTimestamp.Time.Format(time.RFC3339),
							"annotations":       ns.Annotations,
						},
					},
				}
				matches := j.matchesResourceFilter(nsUnstructured)
				
				expectedProcessed := false
				for _, expected := range tt.expectProcessed {
					if ns.Name == expected {
						expectedProcessed = true
						break
					}
				}
				
				if matches != expectedProcessed {
					if expectedProcessed {
						t.Errorf("Expected namespace %s to be processed, but it was not", ns.Name)
					} else {
						t.Errorf("Expected namespace %s to be skipped, but it was processed", ns.Name)
					}
				}
			}
		})
	}
}
func TestNamespaceClusterResourcesBug(t *testing.T) {
	// This test demonstrates the bug where namespaces require --include-cluster-resources
	// to be processed, even though the documentation states they should be handled by default
	
	// Create janitor with IncludeClusterResources = false (default)
	config := &Config{
		IncludeNamespaces:       []string{"all"},
		ExcludeNamespaces:       []string{"kube-system"},
		IncludeResources:        []string{"all"},
		IncludeClusterResources: false, // This is the default
		DryRun:                  true,
		Parallelism:             1,
	}

	// Create a test namespace using the k8s API types (like cleanupNamespaces does)
	testNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-namespace",
			CreationTimestamp: metav1.Time{
				Time: time.Now().Add(-3 * time.Hour),
			},
			Annotations: map[string]string{
				TTLAnnotation: "2h",
			},
		},
	}

	// Create fake client and add the namespace
	clientset := fake.NewSimpleClientset(testNamespace)
	
	j := &Janitor{
		client: clientset,
		config: config,
		cache:  make(map[string]interface{}),
		counterMutex: sync.Mutex{},
	}

	// Test using the real namespace object (as cleanupNamespaces does)
	matches := j.matchesResourceFilter(testNamespace)

	// According to the documentation, namespaces should be processed by default
	// without needing --include-cluster-resources flag
	if !matches {
		t.Errorf("BUG: Namespace should be processed by default without --include-cluster-resources flag, but matchesResourceFilter returned false. This contradicts the documentation.")
	}

	// Now test with include-cluster-resources = true (should definitely work)
	config.IncludeClusterResources = true
	matches = j.matchesResourceFilter(testNamespace)
	if !matches {
		t.Errorf("Namespace should definitely be processed with --include-cluster-resources flag, but matchesResourceFilter returned false")
	}
}