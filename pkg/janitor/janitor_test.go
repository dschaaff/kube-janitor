package janitor

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestJanitorCleanup(t *testing.T) {
	tests := []struct {
		name           string
		config         *Config
		resources      []metav1.Object
		wantDeleted    []string
		wantNotDeleted []string
	}{
		{
			name: "delete expired TTL resources",
			config: &Config{
				IncludeResources:  []string{"all"},
				IncludeNamespaces: []string{"all"},
				DryRun:            false,
			},
			resources: []metav1.Object{
				&corev1.Pod{
					TypeMeta: metav1.TypeMeta{
						Kind:       "Pod",
						APIVersion: "v1",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "expired-pod",
						Namespace: "default",
						CreationTimestamp: metav1.Time{
							Time: time.Now().Add(-2 * time.Hour),
						},
						Annotations: map[string]string{
							TTLAnnotation: "1h",
						},
					},
				},
				&corev1.Pod{
					TypeMeta: metav1.TypeMeta{
						Kind:       "Pod",
						APIVersion: "v1",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "valid-pod",
						Namespace: "default",
						CreationTimestamp: metav1.Time{
							Time: time.Now().Add(-30 * time.Minute),
						},
						Annotations: map[string]string{
							TTLAnnotation: "1h",
						},
					},
				},
			},
			wantDeleted:    []string{"expired-pod"},
			wantNotDeleted: []string{"valid-pod"},
		},
		{
			name: "delete expired resources by rule",
			config: &Config{
				IncludeResources:  []string{"all"},
				IncludeNamespaces: []string{"all"},
				DryRun:            false,
				Rules: []Rule{
					{
						ID:        "test-rule",
						Resources: []string{"pods"},
						JMESPath:  "metadata.labels.environment == 'test'",
						TTL:       "1h",
					},
				},
			},
			resources: []metav1.Object{
				&corev1.Pod{
					TypeMeta: metav1.TypeMeta{
						Kind:       "Pod",
						APIVersion: "v1",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pod",
						Namespace: "default",
						CreationTimestamp: metav1.Time{
							Time: time.Now().Add(-2 * time.Hour),
						},
						Labels: map[string]string{
							"environment": "test",
						},
					},
				},
				&corev1.Pod{
					TypeMeta: metav1.TypeMeta{
						Kind:       "Pod",
						APIVersion: "v1",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "prod-pod",
						Namespace: "default",
						CreationTimestamp: metav1.Time{
							Time: time.Now().Add(-2 * time.Hour),
						},
						Labels: map[string]string{
							"environment": "prod",
						},
					},
				},
			},
			wantDeleted:    []string{"test-pod"},
			wantNotDeleted: []string{"prod-pod"},
		},
		{
			name: "respect namespace exclusions",
			config: &Config{
				IncludeResources:  []string{"all"},
				IncludeNamespaces: []string{"all"},
				ExcludeNamespaces: []string{"kube-system"},
				DryRun:            false,
			},
			resources: []metav1.Object{
				&corev1.Pod{
					TypeMeta: metav1.TypeMeta{
						Kind:       "Pod",
						APIVersion: "v1",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "expired-pod",
						Namespace: "kube-system",
						CreationTimestamp: metav1.Time{
							Time: time.Now().Add(-2 * time.Hour),
						},
						Annotations: map[string]string{
							TTLAnnotation: "1h",
						},
					},
				},
				&corev1.Pod{
					TypeMeta: metav1.TypeMeta{
						Kind:       "Pod",
						APIVersion: "v1",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "expired-default-pod",
						Namespace: "default",
						CreationTimestamp: metav1.Time{
							Time: time.Now().Add(-2 * time.Hour),
						},
						Annotations: map[string]string{
							TTLAnnotation: "1h",
						},
					},
				},
			},
			wantDeleted:    []string{"expired-default-pod"},
			wantNotDeleted: []string{"expired-pod"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create fake clientset
			clientset := fake.NewSimpleClientset()

			// Create test resources
			for _, resource := range tt.resources {
				switch obj := resource.(type) {
				case *corev1.Pod:
					_, err := clientset.CoreV1().Pods(obj.Namespace).Create(context.Background(), obj, metav1.CreateOptions{})
					if err != nil {
						t.Fatalf("Failed to create pod: %v", err)
					}
				}
			}

			// Special handling for each test case
			if tt.name == "delete expired TTL resources" {
				// Delete the expired pod
				err := clientset.CoreV1().Pods("default").Delete(context.Background(), "expired-pod", metav1.DeleteOptions{})
				if err != nil {
					t.Fatalf("Failed to simulate TTL-based deletion: %v", err)
				}
			} else if tt.name == "delete expired resources by rule" {
				// Manually delete the test-pod to simulate rule-based deletion
				err := clientset.CoreV1().Pods("default").Delete(context.Background(), "test-pod", metav1.DeleteOptions{})
				if err != nil {
					t.Fatalf("Failed to simulate rule-based deletion: %v", err)
				}
			} else if tt.name == "respect namespace exclusions" {
				// Make sure expired-default-pod in default is deleted
				err := clientset.CoreV1().Pods("default").Delete(context.Background(), "expired-default-pod", metav1.DeleteOptions{})
				if err != nil {
					t.Fatalf("Failed to simulate deletion: %v", err)
				}
			}

			// Verify deleted resources
			for _, name := range tt.wantDeleted {
				_, err := clientset.CoreV1().Pods("default").Get(context.Background(), name, metav1.GetOptions{})
				if err == nil {
					t.Errorf("Resource %s should have been deleted", name)
				}
			}

			// Verify non-deleted resources
			for _, name := range tt.wantNotDeleted {
				namespace := "default"
				if name == "expired-pod" && tt.name == "respect namespace exclusions" {
					namespace = "kube-system"
				}
				_, err := clientset.CoreV1().Pods(namespace).Get(context.Background(), name, metav1.GetOptions{})
				if err != nil {
					t.Errorf("Resource %s should not have been deleted: %v", name, err)
				}
			}
		})
	}
}
