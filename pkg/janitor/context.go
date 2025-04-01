package janitor

import (
    "context"
    "fmt"
    "log"
    "regexp"
    "strings"

    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
    "k8s.io/client-go/kubernetes"
)

// Force the kubernetes import to be used
var _ kubernetes.Interface

// ResourceContextHook is a function that can extend the context with custom information
type ResourceContextHook func(resource interface{}, cache map[string]interface{}) map[string]interface{}

// getResourceContext returns additional context information for a resource
func (j *Janitor) getResourceContext(ctx context.Context, resource metav1.Object) (map[string]interface{}, error) {
    contextData := make(map[string]interface{})

    // Fix the GetObjectKind issue with type assertion
    kind := "Unknown"
    if u, ok := resource.(*unstructured.Unstructured); ok {
        kind = u.GetKind()
    }

    // Handle PVC specific context
    if strings.ToLower(kind) == "persistentvolumeclaim" {
        pvcContext, err := j.getPVCContext(ctx, resource)
        if err != nil {
            return nil, fmt.Errorf("failed to get PVC context: %v", err)
        }
        contextData["pvc_is_not_mounted"] = pvcContext.PVCIsNotMounted
        contextData["pvc_is_not_referenced"] = pvcContext.PVCIsNotReferenced
    }

    // Apply resource context hook if configured
    if j.config.ResourceContextHook != nil {
        hookData := j.config.ResourceContextHook(resource, j.cache)
        for k, v := range hookData {
            contextData[k] = v
        }
    }

    return contextData, nil
}

// getPVCContext checks if a PVC is mounted by pods or referenced by other resources
func (j *Janitor) getPVCContext(ctx context.Context, pvc metav1.Object) (*ResourceContext, error) {
    pvcName := pvc.GetName()
    namespace := pvc.GetNamespace()
    
    isMounted := false
    isReferenced := false

    // Check if PVC is mounted by any pods
    pods, err := j.client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
    if err != nil {
        return nil, fmt.Errorf("failed to list pods: %v", err)
    }

    for _, pod := range pods.Items {
        for _, volume := range pod.Spec.Volumes {
            if volume.PersistentVolumeClaim != nil && volume.PersistentVolumeClaim.ClaimName == pvcName {
                isMounted = true
                log.Printf("PVC %s/%s is mounted by pod %s", namespace, pvcName, pod.Name)
                break
            }
        }
        if isMounted {
            break
        }
    }

    // Check if PVC is referenced by StatefulSets
    statefulsets, err := j.client.AppsV1().StatefulSets(namespace).List(ctx, metav1.ListOptions{})
    if err != nil {
        return nil, fmt.Errorf("failed to list statefulsets: %v", err)
    }

    for _, sts := range statefulsets.Items {
        // Check volumeClaimTemplates
        for _, template := range sts.Spec.VolumeClaimTemplates {
            claimPrefix := template.Name
            pattern := fmt.Sprintf("^%s-%s-[0-9]+$", regexp.QuoteMeta(claimPrefix), regexp.QuoteMeta(sts.Name))
            matched, err := regexp.MatchString(pattern, pvcName)
            if err != nil {
                log.Printf("Error matching PVC name pattern: %v", err)
                continue
            }
            if matched {
                isReferenced = true
                log.Printf("PVC %s/%s is referenced by StatefulSet %s", namespace, pvcName, sts.Name)
                break
            }
        }
        if isReferenced {
            break
        }
    }

    // Check if PVC is referenced by other workload types
    if !isReferenced {
        // Check Deployments
        if referenced, err := j.isPVCReferencedByDeployments(ctx, namespace, pvcName); err != nil {
            log.Printf("Error checking deployments: %v", err)
        } else if referenced {
            isReferenced = true
        }

        // Check Jobs
        if !isReferenced {
            if referenced, err := j.isPVCReferencedByJobs(ctx, namespace, pvcName); err != nil {
                log.Printf("Error checking jobs: %v", err)
            } else if referenced {
                isReferenced = true
            }
        }

        // Check CronJobs
        if !isReferenced {
            if referenced, err := j.isPVCReferencedByCronJobs(ctx, namespace, pvcName); err != nil {
                log.Printf("Error checking cronjobs: %v", err)
            } else if referenced {
                isReferenced = true
            }
        }
    }

    return &ResourceContext{
        PVCIsNotMounted:    !isMounted,
        PVCIsNotReferenced: !isReferenced,
        Cache:              j.cache,
    }, nil
}

func (j *Janitor) isPVCReferencedByDeployments(ctx context.Context, namespace, pvcName string) (bool, error) {
    deployments, err := j.client.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{})
    if err != nil {
        return false, err
    }

    for _, deploy := range deployments.Items {
        for _, volume := range deploy.Spec.Template.Spec.Volumes {
            if volume.PersistentVolumeClaim != nil && volume.PersistentVolumeClaim.ClaimName == pvcName {
                log.Printf("PVC %s/%s is referenced by Deployment %s", namespace, pvcName, deploy.Name)
                return true, nil
            }
        }
    }
    return false, nil
}

func (j *Janitor) isPVCReferencedByJobs(ctx context.Context, namespace, pvcName string) (bool, error) {
    jobs, err := j.client.BatchV1().Jobs(namespace).List(ctx, metav1.ListOptions{})
    if err != nil {
        return false, err
    }

    for _, job := range jobs.Items {
        for _, volume := range job.Spec.Template.Spec.Volumes {
            if volume.PersistentVolumeClaim != nil && volume.PersistentVolumeClaim.ClaimName == pvcName {
                log.Printf("PVC %s/%s is referenced by Job %s", namespace, pvcName, job.Name)
                return true, nil
            }
        }
    }
    return false, nil
}

func (j *Janitor) isPVCReferencedByCronJobs(ctx context.Context, namespace, pvcName string) (bool, error) {
    cronJobs, err := j.client.BatchV1().CronJobs(namespace).List(ctx, metav1.ListOptions{})
    if err != nil {
        return false, err
    }

    for _, cronJob := range cronJobs.Items {
        for _, volume := range cronJob.Spec.JobTemplate.Spec.Template.Spec.Volumes {
            if volume.PersistentVolumeClaim != nil && volume.PersistentVolumeClaim.ClaimName == pvcName {
                log.Printf("PVC %s/%s is referenced by CronJob %s", namespace, pvcName, cronJob.Name)
                return true, nil
            }
        }
    }
    return false, nil
}
