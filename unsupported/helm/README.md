# Kube-Janitor
Kubernetes Janitor cleans up (deletes) Kubernetes resources on (1) a configured TTL (time to live) or (2) a configured expiry date (absolute timestamp).

This chart helps you deploy kube-janitor as CronJob on your Kubernetes cluster. You can find the original repo here: https://codeberg.org/dschaaff/kube-janitor

## Configuration

Kubernetes Janitor can be executed as a ` Deployment` or a `CronJob` depending on the `kind` parameter.
Please checkout Kubernete's docs for more info on CronJob's [config options](https://kubernetes.io/docs/concepts/workloads/controllers/cron-jobs/).
You can check out more configuration examples on kube-janitor's own [README](https://codeberg.org/dschaaff/kube-janitor#configuration) for more of the package's options.

Some of the default values are not specified in the `values.yaml` and are instead inherited the default from upstream.

| Parameter              | Description                                                    | Type      | Default                     |
| ---------------------- | -------------------------------------------------------------- | --------- | --------------------------- |
| `kind    `             | Execution kind: `Deployment` or `CronJob`                      | `string`  | `Deployment`                |
| `image.repository`     | Image repository                                               | `string`  | `dschaaff/kube-janitor`      |
| `image.tag`            | Override image tag                                             | `string`  | Chart `appVersion`          |
| `image.pullPolicy`     | Image pull policy                                              | `string`  | `IfNotPresent`              |
| `image.pullSecrets`    | Image pull secrets                                             | `list`    | `[]`                        |
| `kubejanitor.dryRun`   | Run in dry-run mode only. The job will print out what would be done, but does not make changes | `boolean` | false |
| `kubejanitor.debug`    | Run in debug-mode                                              | `boolean` | false                       |
| `kubejanitor.includeResources`  | List of k8s resource types to include, ex. `deployment,svc,ingress` | `list` | `[]`             |
| `kubejanitor.excludeResources`  | List of k8s resource types to exclude, ex. `deployment,svc,ingress` | `list` | `['events','controllerrevisions'] (kube-janitor default) |
| `kubejanitor.includeNamespaces` | List of namespaces to include                         | `list`    | `[]`                        |
| `kubejanitor.excludeNamespaces` | List of namespaces to exclude                         | `list`    | `['kube-system']` (kube-janitor default) |
| `kubejanitor.interval` | Interval in seconds between executions (only used with `Deployment` kind) | `integer` | `30` (kube-janitor default) |
| `kubejanitor.additionalArgs`    | Additional command line arguments                     | `list`    | `[]`                        |
| `kubejanitor.rules`    | Rules configuration to set TTL for arbitrary objects           | `list`    | `[]`                        |
| `cron.schedule`        | `CronJobSpec` for set the schedule of the CronJob resource     | `string`  | `*/5 * * * *`               |
| `cron.successfulJobsHistoryLimit` | `CronJobSpec` for number of successful jobs to keep | `integer` | `3` (k8s default)           |
| `cron.failedJobsHistoryLimit`     | `CronJobSpec` for number of failed jobs to keep     | `integer` | `3`                         |
| `cron.startingDeadlineSeconds`    | `CronJobSpec` for number of length of time window (in seconds) within which to check for jobs failed to run | `integer` | Unset |
| `cron.restartPolicy`   | `PodSpec` for pod restarting policy on failure                 | `string`  | `OnFailure`                 |
| `cron.suspend`         | `CronJobSpec` for telling the controller whether to suspend subsequent jobs | `boolean` | `false` (k8s default) |
| `resources`            | `PodSpec` for resource request and limit for CPU and memory    | `map`     | See `values.yaml`           |
| `nodeSelector`         | `PodSpec` for defining nodeSelector to deploy the pod on       | `map`     | `{}`                        |
| `affinity`             | `PodSpec` for defining affinity to deploy the pod on           | `map`     | `{}`                        |
| `tolerations`          | `PodSpec` for defining tolerations to deploy the pod on        | `map`     | `{}`                        |
