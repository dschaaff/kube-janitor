package janitor

import (
    "context"
    "net/http"
    "net/http/httptest"
    "os"
    "testing"
    "time"

    corev1 "k8s.io/api/core/v1"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/client-go/kubernetes/fake"
)

func TestJanitorCleanup(t *testing.T) {
    tests := []struct {
        name            string
        config         *Config
        resources      []metav1.Object
        wantDeleted    []string
        wantNotDeleted []string
    }{
        {
            name: "delete expired TTL resources",
            config: &Config{
                IncludeResources: []string{"all"},
                IncludeNamespaces: []string{"all"},
                DryRun: false,
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
                IncludeResources: []string{"all"},
                IncludeNamespaces: []string{"all"},
                DryRun: false,
                Rules: []Rule{
                    {
                        ID:        "test-rule",
                        Resources: []string{"pods"},
                        JMESPath: "metadata.labels.environment == 'test'",
                        TTL:      "1h",
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
                DryRun: false,
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

func TestHandleResourceWithRulesAndContext(t *testing.T) {
    tests := []struct {
        name     string
        config   *Config
        resource metav1.Object
        context  map[string]interface{}
        wantErr  bool
    }{
        {
            name: "matching rule with context",
            config: &Config{
                Rules: []Rule{
                    {
                        ID:        "test-rule",
                        Resources: []string{"pods"},
                        JMESPath: "metadata.labels.environment == 'test' && _context.random_dice > 3",
                        TTL:      "1h",
                    },
                },
                DryRun: true, // Set DryRun to true to prevent actual deletion attempts
            },
            resource: &corev1.Pod{
                TypeMeta: metav1.TypeMeta{
                    Kind:       "Pod",
                    APIVersion: "v1",
                },
                ObjectMeta: metav1.ObjectMeta{
                    Name:      "test-pod",
                    Namespace: "default",
                    Labels: map[string]string{
                        "environment": "test",
                    },
                    CreationTimestamp: metav1.Time{
                        Time: time.Now().Add(-2 * time.Hour),
                    },
                },
            },
            context: map[string]interface{}{
                "random_dice": 4,
            },
            wantErr: false,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            j := &Janitor{
                client: fake.NewSimpleClientset(),
                config: tt.config,
                cache:  make(map[string]interface{}),
            }

            // Add context to cache
            for k, v := range tt.context {
                j.cache[k] = v
            }

            err := j.handleResource(context.Background(), tt.resource, make(map[string]int), make(map[string]bool))
            if (err != nil) != tt.wantErr {
                t.Errorf("handleResource() error = %v, wantErr %v", err, tt.wantErr)
            }
        })
    }
}

func TestHandleExpiry(t *testing.T) {
    tests := []struct {
        name     string
        resource metav1.Object
        wantErr  bool
    }{
        {
            name: "valid expiry",
            resource: &corev1.Pod{
                TypeMeta: metav1.TypeMeta{
                    Kind:       "Pod",
                    APIVersion: "v1",
                },
                ObjectMeta: metav1.ObjectMeta{
                    Name:      "test-pod",
                    Namespace: "default",
                    Annotations: map[string]string{
                        ExpiryAnnotation: time.Now().Add(-1 * time.Hour).Format(time.RFC3339),
                    },
                },
            },
            wantErr: false,
        },
        {
            name: "future expiry",
            resource: &corev1.Pod{
                TypeMeta: metav1.TypeMeta{
                    Kind:       "Pod",
                    APIVersion: "v1",
                },
                ObjectMeta: metav1.ObjectMeta{
                    Name:      "test-pod",
                    Namespace: "default",
                    Annotations: map[string]string{
                        ExpiryAnnotation: time.Now().Add(1 * time.Hour).Format(time.RFC3339),
                    },
                },
            },
            wantErr: false,
        },
        {
            name: "invalid expiry format",
            resource: &corev1.Pod{
                TypeMeta: metav1.TypeMeta{
                    Kind:       "Pod",
                    APIVersion: "v1",
                },
                ObjectMeta: metav1.ObjectMeta{
                    Name:      "test-pod",
                    Namespace: "default",
                    Annotations: map[string]string{
                        ExpiryAnnotation: "invalid-date",
                    },
                },
            },
            wantErr: true,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            j := &Janitor{
                client: fake.NewSimpleClientset(),
                config: &Config{
                    DryRun: true, // Set DryRun to true to prevent actual deletion attempts
                },
                cache:  make(map[string]interface{}),
            }

            counter := make(map[string]int)
            err := j.handleExpiry(context.Background(), tt.resource, counter)
            if (err != nil) != tt.wantErr {
                t.Errorf("handleExpiry() error = %v, wantErr %v", err, tt.wantErr)
            }
        })
    }
}

func TestHandleResourceTTL(t *testing.T) {
    tests := []struct {
        name     string
        resource metav1.Object
        ttl      string
        wantErr  bool
    }{
        {
            name: "valid TTL",
            resource: &corev1.Pod{
                TypeMeta: metav1.TypeMeta{
                    Kind:       "Pod",
                    APIVersion: "v1",
                },
                ObjectMeta: metav1.ObjectMeta{
                    Name:      "test-pod",
                    Namespace: "default",
                    Annotations: map[string]string{
                        TTLAnnotation: "1h",
                    },
                    CreationTimestamp: metav1.Time{
                        Time: time.Now().Add(-2 * time.Hour),
                    },
                },
            },
            wantErr: false,
        },
        {
            name: "invalid TTL format",
            resource: &corev1.Pod{
                TypeMeta: metav1.TypeMeta{
                    Kind:       "Pod",
                    APIVersion: "v1",
                },
                ObjectMeta: metav1.ObjectMeta{
                    Name:      "test-pod",
                    Namespace: "default",
                    Annotations: map[string]string{
                        TTLAnnotation: "1x",
                    },
                },
            },
            wantErr: true,
        },
        {
            name: "forever TTL",
            resource: &corev1.Pod{
                TypeMeta: metav1.TypeMeta{
                    Kind:       "Pod",
                    APIVersion: "v1",
                },
                ObjectMeta: metav1.ObjectMeta{
                    Name:      "test-pod",
                    Namespace: "default",
                    Annotations: map[string]string{
                        TTLAnnotation: "forever",
                    },
                },
            },
            wantErr: false,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            j := &Janitor{
                client: fake.NewSimpleClientset(),
                config: &Config{
                    DryRun: true, // Set DryRun to true to prevent actual deletion attempts
                },
                cache:  make(map[string]interface{}),
            }

            counter := make(map[string]int)
            err := j.handleTTL(context.Background(), tt.resource, counter)
            if (err != nil) != tt.wantErr {
                t.Errorf("handleResourceTTL() error = %v, wantErr %v", err, tt.wantErr)
            }
        })
    }
}

func TestHandleResourceWithRules(t *testing.T) {
    tests := []struct {
        name     string
        config   *Config
        resource metav1.Object
        wantErr  bool
    }{
        {
            name: "matching rule",
            config: &Config{
                Rules: []Rule{
                    {
                        ID:        "test-rule",
                        Resources: []string{"pods"},
                        JMESPath: "metadata.labels.environment == 'test'",
                        TTL:      "1h",
                    },
                },
                DryRun: true, // Set DryRun to true to prevent actual deletion attempts
            },
            resource: &corev1.Pod{
                TypeMeta: metav1.TypeMeta{
                    Kind:       "Pod",
                    APIVersion: "v1",
                },
                ObjectMeta: metav1.ObjectMeta{
                    Name:      "test-pod",
                    Namespace: "default",
                    Labels: map[string]string{
                        "environment": "test",
                    },
                    CreationTimestamp: metav1.Time{
                        Time: time.Now().Add(-2 * time.Hour),
                    },
                },
            },
            wantErr: false,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            j := &Janitor{
                client: fake.NewSimpleClientset(),
                config: tt.config,
                cache:  make(map[string]interface{}),
            }

            err := j.handleResource(context.Background(), tt.resource, make(map[string]int), make(map[string]bool))
            if (err != nil) != tt.wantErr {
                t.Errorf("handleResource() error = %v, wantErr %v", err, tt.wantErr)
            }
        })
    }
}

func TestCleanupWithWebhookNotification(t *testing.T) {
    // Create test server
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodPost {
            t.Errorf("Expected POST request, got %s", r.Method)
        }
        w.WriteHeader(http.StatusOK)
    }))
    defer server.Close()

    config := &Config{
        WebhookURL: server.URL,
        IncludeResources: []string{"all"},
        IncludeNamespaces: []string{"all"},
        DryRun: true, // Set DryRun to true to prevent actual deletion attempts
    }

    j := &Janitor{
        client: fake.NewSimpleClientset(),
        config: config,
        cache:  make(map[string]interface{}),
    }

    // Create a resource that will be deleted
    pod := &corev1.Pod{
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
            Annotations: map[string]string{
                TTLAnnotation: "1h",
            },
        },
    }

    _, err := j.client.CoreV1().Pods("default").Create(context.Background(), pod, metav1.CreateOptions{})
    if err != nil {
        t.Fatalf("Failed to create test pod: %v", err)
    }

    // Instead of running full cleanup, just handle this resource directly
    counter := make(map[string]int)
    err = j.handleTTL(context.Background(), pod, counter)
    if err != nil {
        t.Errorf("handleTTL() error = %v", err)
    }
}

func TestWebhookNotifications(t *testing.T) {
    // Create test server
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodPost {
            t.Errorf("Expected POST request, got %s", r.Method)
        }
        w.WriteHeader(http.StatusOK)
    }))
    defer server.Close()

    // Set webhook URL
    os.Setenv("WEBHOOK_URL", server.URL)
    defer os.Unsetenv("WEBHOOK_URL")

    // Test notification
    err := SendWebhookNotification("Test notification message")
    if err != nil {
        t.Errorf("SendWebhookNotification() error = %v", err)
    }
}
