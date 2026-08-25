package janitor

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ResourceContextHook is a function that can extend the context with custom information
type ResourceContextHook func(resource interface{}, cache map[string]interface{}) map[string]interface{}

// getResourceContext returns additional context information for a resource
func (j *Janitor) getResourceContext(ctx context.Context, t Target) (map[string]interface{}, error) {
	contextData := make(map[string]interface{})

	// Handle PVC specific context
	if strings.ToLower(t.Kind) == "persistentvolumeclaim" {
		pvcContext, err := j.getPVCContext(ctx, t)
		if err != nil {
			return nil, fmt.Errorf("failed to get PVC context: %v", err)
		}
		contextData["pvc_is_not_mounted"] = pvcContext.PVCIsNotMounted
		contextData["pvc_is_not_referenced"] = pvcContext.PVCIsNotReferenced
	}

	// Apply resource context hook if configured
	if j.config.ResourceContextHook != nil {
		hookData := j.config.ResourceContextHook(t.Raw, j.cache)
		for k, v := range hookData {
			contextData[k] = v
		}
	}

	return contextData, nil
}

// getPVCContext checks if a PVC is mounted by pods or referenced by other resources
func (j *Janitor) getPVCContext(ctx context.Context, t Target) (*ResourceContext, error) {
	pvcName := t.Name
	namespace := t.Namespace

	isMounted := false
	isReferenced := false

	// Check if PVC is mounted by any pods
	pods, err := j.cluster.Typed.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list pods: %v", err)
	}

	for _, pod := range pods.Items {
		for _, volume := range pod.Spec.Volumes {
			if volume.PersistentVolumeClaim != nil && volume.PersistentVolumeClaim.ClaimName == pvcName {
				isMounted = true
				j.log.Debugf("PVC %s/%s is mounted by pod %s", namespace, pvcName, pod.Name)
				break
			}
		}
		if isMounted {
			break
		}
	}

	// Check if PVC is referenced by StatefulSets
	statefulsets, err := j.cluster.Typed.AppsV1().StatefulSets(namespace).List(ctx, metav1.ListOptions{})
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
				j.log.Errorf("Error matching PVC name pattern: %v", err)
				continue
			}
			if matched {
				isReferenced = true
				j.log.Debugf("PVC %s/%s is referenced by StatefulSet %s", namespace, pvcName, sts.Name)
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
			j.log.Errorf("Error checking deployments: %v", err)
		} else if referenced {
			isReferenced = true
		}

		// Check Jobs
		if !isReferenced {
			if referenced, err := j.isPVCReferencedByJobs(ctx, namespace, pvcName); err != nil {
				j.log.Errorf("Error checking jobs: %v", err)
			} else if referenced {
				isReferenced = true
			}
		}

		// Check CronJobs
		if !isReferenced {
			if referenced, err := j.isPVCReferencedByCronJobs(ctx, namespace, pvcName); err != nil {
				j.log.Errorf("Error checking cronjobs: %v", err)
			} else if referenced {
				isReferenced = true
			}
		}
	}

	return &ResourceContext{
		PVCIsNotMounted:    !isMounted,
		PVCIsNotReferenced: !isReferenced,
	}, nil
}

func (j *Janitor) isPVCReferencedByDeployments(ctx context.Context, namespace, pvcName string) (bool, error) {
	deployments, err := j.cluster.Typed.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return false, err
	}

	for _, deploy := range deployments.Items {
		for _, volume := range deploy.Spec.Template.Spec.Volumes {
			if volume.PersistentVolumeClaim != nil && volume.PersistentVolumeClaim.ClaimName == pvcName {
				j.log.Debugf("PVC %s/%s is referenced by Deployment %s", namespace, pvcName, deploy.Name)
				return true, nil
			}
		}
	}
	return false, nil
}

func (j *Janitor) isPVCReferencedByJobs(ctx context.Context, namespace, pvcName string) (bool, error) {
	jobs, err := j.cluster.Typed.BatchV1().Jobs(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return false, err
	}

	for _, job := range jobs.Items {
		for _, volume := range job.Spec.Template.Spec.Volumes {
			if volume.PersistentVolumeClaim != nil && volume.PersistentVolumeClaim.ClaimName == pvcName {
				j.log.Debugf("PVC %s/%s is referenced by Job %s", namespace, pvcName, job.Name)
				return true, nil
			}
		}
	}
	return false, nil
}

func (j *Janitor) isPVCReferencedByCronJobs(ctx context.Context, namespace, pvcName string) (bool, error) {
	cronJobs, err := j.cluster.Typed.BatchV1().CronJobs(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return false, err
	}

	for _, cronJob := range cronJobs.Items {
		for _, volume := range cronJob.Spec.JobTemplate.Spec.Template.Spec.Volumes {
			if volume.PersistentVolumeClaim != nil && volume.PersistentVolumeClaim.ClaimName == pvcName {
				j.log.Debugf("PVC %s/%s is referenced by CronJob %s", namespace, pvcName, cronJob.Name)
				return true, nil
			}
		}
	}
	return false, nil
}
