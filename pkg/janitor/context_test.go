package janitor

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestGetPVCContext(t *testing.T) {
	tests := []struct {
		name              string
		pvc               *corev1.PersistentVolumeClaim
		pods              []corev1.Pod
		statefulSets      []appsv1.StatefulSet
		deployments       []appsv1.Deployment
		jobs              []batchv1.Job
		cronJobs          []batchv1.CronJob
		wantNotMounted    bool
		wantNotReferenced bool
	}{
		{
			name: "pvc mounted by pod",
			pvc: &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pvc",
					Namespace: "default",
				},
			},
			pods: []corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pod",
						Namespace: "default",
					},
					Spec: corev1.PodSpec{
						Volumes: []corev1.Volume{
							{
								Name: "test-volume",
								VolumeSource: corev1.VolumeSource{
									PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
										ClaimName: "test-pvc",
									},
								},
							},
						},
					},
				},
			},
			wantNotMounted:    false,
			wantNotReferenced: true,
		},
		{
			name: "pvc referenced by statefulset",
			pvc: &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "data-my-sts-0",
					Namespace: "default",
				},
			},
			statefulSets: []appsv1.StatefulSet{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "my-sts",
						Namespace: "default",
					},
					Spec: appsv1.StatefulSetSpec{
						VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
							{
								ObjectMeta: metav1.ObjectMeta{
									Name: "data",
								},
							},
						},
					},
				},
			},
			wantNotMounted:    true,
			wantNotReferenced: false,
		},
		{
			name: "unused pvc",
			pvc: &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "unused-pvc",
					Namespace: "default",
				},
			},
			wantNotMounted:    true,
			wantNotReferenced: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create fake clientset
			clientset := fake.NewSimpleClientset()

			// Create janitor instance with fake client
			j := New(&Config{}, Cluster{Typed: clientset})

			// Create test resources in fake client
			if tt.pvc != nil {
				_, err := clientset.CoreV1().PersistentVolumeClaims(tt.pvc.Namespace).Create(context.Background(), tt.pvc, metav1.CreateOptions{})
				if err != nil {
					t.Fatalf("Failed to create PVC: %v", err)
				}
			}

			for _, pod := range tt.pods {
				_, err := clientset.CoreV1().Pods(pod.Namespace).Create(context.Background(), &pod, metav1.CreateOptions{})
				if err != nil {
					t.Fatalf("Failed to create Pod: %v", err)
				}
			}

			for _, sts := range tt.statefulSets {
				_, err := clientset.AppsV1().StatefulSets(sts.Namespace).Create(context.Background(), &sts, metav1.CreateOptions{})
				if err != nil {
					t.Fatalf("Failed to create StatefulSet: %v", err)
				}
			}

			// Get PVC context
			ctx := context.Background()
			got, err := j.getPVCContext(ctx, mustTarget(t, tt.pvc))
			if err != nil {
				t.Fatalf("getPVCContext() error = %v", err)
			}

			if got.PVCIsNotMounted != tt.wantNotMounted {
				t.Errorf("getPVCContext().PVCIsNotMounted = %v, want %v", got.PVCIsNotMounted, tt.wantNotMounted)
			}

			if got.PVCIsNotReferenced != tt.wantNotReferenced {
				t.Errorf("getPVCContext().PVCIsNotReferenced = %v, want %v", got.PVCIsNotReferenced, tt.wantNotReferenced)
			}
		})
	}
}

func TestGetResourceContext(t *testing.T) {
	tests := []struct {
		name     string
		resource metav1.Object
		hook     ResourceContextHook
		want     map[string]interface{}
		setup    func(*testing.T, *Janitor)
	}{
		{
			name: "resource with hook",
			resource: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
			},
			hook: func(resource interface{}, cache map[string]interface{}) map[string]interface{} {
				return map[string]interface{}{
					"test_value": "test",
				}
			},
			want: map[string]interface{}{
				"test_value": "test",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			j := New(&Config{ResourceContextHook: tt.hook}, Cluster{Typed: fake.NewSimpleClientset()})

			// Run setup if provided
			if tt.setup != nil {
				tt.setup(t, j)
			}

			got, err := j.getResourceContext(context.Background(), mustTarget(t, tt.resource))
			if err != nil {
				t.Fatalf("getResourceContext() error = %v", err)
			}

			// Compare results
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("getResourceContext()[%q] = %v, want %v", k, got[k], v)
				}
			}
		})
	}
}
