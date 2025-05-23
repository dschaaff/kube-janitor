# Kubernetes Janitor

**NOTE This started as a fork of [Henning Jacob's project](https://codeberg.org/dschaaff/kube-janitor/src/branch/main). **

Kubernetes Janitor cleans up (deletes) Kubernetes resources on (1) a
configured TTL (time to live) or (2) a configured expiry date (absolute
timestamp).

It processes all namespaces and all namespaced resources including
custom resource definitions (CRDs) and will delete them (1) if the
`janitor/ttl` annotation or a TTL rule indicates the resource as
expired, or (2) if the `janitor/expires` annotation marks the resource
as expired.

## Example Use Cases

### TTL

- Deploy the janitor to a test (non-prod) cluster and use namespaces
  with a TTL of 7 days (`janitor/ttl: 7d` on the namespace object) for
  prototyping
- Annotate your temporary manual test nginx deployment with
  `kubectl annotate deploy nginx janitor/ttl=24h` to automatically
  delete it after 24 hours
- Automatically set `janitor/ttl` on resources created by your CI/CD
  pipeline for pull requests (so PR tests can run and resources are
  cleaned up later)
- Define a rule to automatically delete resources after 4 days if
  required labels were not set (see Rules File below)

### Expiry date

- Deploy the janitor to a test (non-prod) cluster and use namespaces
  with an expiry date (`janitor/expires: 2020-01-17T15:14:38Z` on the
  namespace object)
- Annotate your temporary manual test nginx deployment with
  `kubectl annotate deploy nginx janitor/expires=2020-01-01` to
  automatically delete it at midnight (UTC) of 1st of January 2020.

### Unused Persistent Volume Claims

- Use a rules file with to delete all unused PVCs
  (`jmespath: "_context.pvc_is_not_mounted && _context.pvc_is_not_referenced"`)

## Usage

Deploy the janitor into your cluster via (also works with
[Minikube](https://github.com/kubernetes/minikube)):

> Warning: if you want to deploy janitor to namespace other than
> `default`, you need to edit `/deploy/rbac.yaml` first.

```{.sourceCode .bash}
$ kubectl apply -k deploy/
```

The example configuration uses the `--dry-run` as a safety flag to
prevent any deletion \-\-- remove it to enable the janitor, e.g. by
editing the deployment:

```{.sourceCode .bash}
$ kubectl edit deploy kube-janitor
```

To see the janitor in action, deploy a simple nginx and annotate it
accordingly:

```{.sourceCode .bash}
$ kubectl run temp-nginx --image=nginx
$ kubectl annotate deploy temp-nginx janitor/ttl=5m
```

You should see the `temp-nginx` deployment being deleted after 5
minutes.

Edit the example rules file via `kubectl edit configmap kube-janitor` to
try out generic TTL rules (needs a pod restart to reload rules).

## Configuration

The janitor is configured via command line args, environment variables,
Kubernetes annotations, and an optional YAML rules file.

Supported Kubernetes annotations:

`janitor/ttl`

: Maximum time to live (TTL) for the annotated resource. Annotation
value must be a string composed of a integer value and a unit suffix
(one of `s`, `m`, `h`, `d`, or `w`), e.g. `120s` (120 seconds), `5m`
(5 minutes), `8h` (8 hours), `7d` (7 days), or `2w` (2 weeks). In
the case that the resource should not be deleted by Janitor, the
special value `forever` can be specified as TTL. Note that the
actual time of deletion depends on the Janitor\'s clean up interval.
The resource will be deleted if its age (delta between now and the
resource creation time) is greater than the specified TTL.

`janitor/expires`

: Absolute timestamp in the format `YYYY-MM-DDTHH:MM:SSZ`,
`YYYY-MM-DDTHH:MM` or `YYYY-MM-DD` to mark the resource for deletion
after the specified date/time. The date format `YYYY-MM-DD` is short
for `YYYY-MM-DDT00:00:00Z`, i.e. the resource will expire at
midnight UTC of the specified date. Example annotation values:
`2019-02-28T20:40:00Z`, `2019-02-28T20:40`, `2019-02-28`.

Available command line options:

`--dry-run`

: Dry run mode: do not change anything, just print what would be done

`--debug`

: Debug mode: print more information

`--quiet`

: Quiet mode: Hides cleanup logs but keeps deletion logs

`--log-format`

: Set custom log format for structured logging output.

`--once`

: Run only once and exit. This is useful if you run the Kubernetes
Janitor as a `CronJob`.

`--interval`

: Loop interval (default: 30s). This option only makes sense when the
`--once` flag is not set.

`--wait-after-delete`

: How long to wait after issuing a delete (default: 0s). This option
does not take effect for dry runs.

`--include-resources`

: Include resources for clean up (default: all resources), can also be
configured via environment variable `INCLUDE_RESOURCES`. This option
can be used if you want to clean up only certain resource types,
e.g. only `deployments`.

`--exclude-resources`

: Exclude resources from clean up (default:
events,controllerrevisions), can also be configured via environment
variable `EXCLUDE_RESOURCES`. This option takes precedence over
`--include-resources`, i.e. `--exclude-resources=foos` in
combination with `--include-resources=foos,bars` would make
`kube-janitor` only process `bars` resources.

`--include-namespaces`

: Include namespaces for clean up (default: all namespaces), can also
be configured via environment variable `INCLUDE_NAMESPACES`

`--exclude-namespaces`

: Exclude namespaces from clean up (default: kube-system), can also be
configured via environment variable `EXCLUDE_NAMESPACES`. This
option takes precedence over `--include-namespaces`, i.e.
`--exclude-namespaces=ns1` in combination with
`--include-namespaces=ns1,ns2` would only process resources in the
`ns2` namespace.

`--rules-file`

: Optional: filename pointing to a YAML file with a list of rules to
apply TTL values to arbitrary Kubernetes objects, e.g. to delete all
deployments without a certain label automatically after N days. See
Rules File configuration section below.

`--deployment-time-annotation`

: Optional: name of the annotation that would be used instead of the
creation timestamp of the resource. This option should be used if
you want the resources to not be cleaned up if they\'ve been
recently redeployed, and your deployment tooling can set this
annotation.

`--resource-context-hook`

: Optional: string pointing to a Go function to populate the
`_context` object with additional information, e.g. by calling
external services. Built-in example to set `_context.random_dice` to
a random dice value (1-6):
`--resource-context-hook=hooks.RandomDice`.

`--include-cluster-resources`

: Optional: enable deletion of cluster-scoped resources. If this flag
is not set, the only cluster-scoped resources that will be handled is
`Namespaces`.

Example flags:

`--interval=20`

`--include-resources=deployments,pods`

`--exclude-resources=replicasets`

`--include-namespaces=default,kube-public`

`--exclude-namespaces=kube-system`

`--rules-file=/config/my-rules-file.yml`

`--log-format='{"level":"%(levelname)s","ts":"%(created)s","logger":"%(name)s","msg":"%(message)s"}'`

## Rules File

When using the `--rules-file` option, the path needs to point to a valid
YAML file with the following format:

```{.sourceCode .yaml}
rules:
# remove deployments and statefulsets without a label "application"
- id: require-application-label
  resources:
  - deployments
  - statefulsets
  jmespath: "!(spec.template.metadata.labels.application)"
  ttl: 4d
# delete all deployments with a name starting with "pr-*"
- id: temporary-pr-deployments
  resources:
  - deployments
  jmespath: "starts_with(metadata.name, 'pr-')"
  ttl: 4h
# delete all resources within the "temp" namespace after 3 days
- id: temp-namespace-cleanup
  resources:
  - "*"
  jmespath: "metadata.namespace == 'temp'"
  ttl: 3d
# require the "foo" pod label for all new deployments starting April 2019
- id: require-foo-label-april-2019
  resources:
  - deployments
  - statefulsets
  jmespath: "!(spec.template.metadata.labels.foo) && metadata.creationTimestamp > '2019-04-01'"
  ttl: 7d
# delete all PVCs which are not mounted and not referenced by StatefulSets
- id: remove-unused-pvcs
  resources:
  - persistentvolumeclaims
  jmespath: "_context.pvc_is_not_mounted && _context.pvc_is_not_referenced"
  ttl: 4d
```

The first matching rule will define the TTL (`ttl` field). Kubernetes
objects with a `janitor/ttl` annotation will not be matched against any
rule.

A rule matches for a given Kubernetes object if all of the following
criteria is true:

- the object has no `janitor/ttl` annotation (otherwise the TTL value
  from the annotation is applied)
- the object\'s type is included in the `resources` list of the rule
  or the special value `*` is part of the `resources` list (similar to
  Kubernetes RBAC)
- the [JMESPath](http://jmespath.org/) evaluates to a truth-like value
  (boolean `true`, non-empty list, non-empty object, or non-empty
  string)

The first matching rule will define the TTL for the object (as if the
object would have a `janitor/ttl` annotation with the same value).

Each rule has the following attributes:

`id`

: Some string identifying the rule (e.g. for log output), must be
lowercase and match the regex `^[a-z][a-z0-9-]*$`. The ID has no
special meaning and is only used to refer to the rule in log
output/statistics.

`resources`

: List of resources (e.g. `deployments`, `namespaces`, ..) this rule
should be applied to. The special value `*` will match all resource
types.

`jmespath`

: [JMESPath](http://jmespath.org/) expression to evaluate on the
resource object. The rule will only match if the expression
evaluates to true. The expression will get the Kubernetes object as
input. The expression `metadata.labels.foo` would evaluate to true
if the object has the label `foo` and it has a non-empty string as
value. Additional context for PersistentVolumeClaim objects is
available in the `_context` property: `_context.pvc_is_not_mounted`
evaluates to true if the PVC is not mounted by any Pod.
`_context.pvc_is_not_referenced` is true if the PVC does not match
any StatefulSet volumeClaimTemplate.

`ttl`

: TTL value (e.g. `15m`) to apply to the object if the rule matches.

## Releases

This project uses [GoReleaser](https://goreleaser.com/) to manage releases.

### Creating a new release

1. Make sure all your changes are committed and pushed to the main branch
2. Create and push a new tag:
   ```bash
   git tag -a v1.0.0 -m "Release v1.0.0"
   git push origin v1.0.0
   ```
3. The release workflow will automatically:
   - Create a GitHub release with binaries for multiple platforms
   - Generate a changelog based on commit messages
   - Tag the existing Docker images with the version number

### Commit message format

To generate better changelogs, please use conventional commit messages:

- `feat: add new feature` (for new features)
- `fix: resolve issue with X` (for bug fixes)
- `docs: update README` (for documentation)
- `chore: update dependencies` (for maintenance tasks)

## Contributing

Easiest way to contribute is to provide feedback! We would love to hear
what you like and what you think is missing. Create an issue

PRs are welcome.

## Local Development

You can run Kubernetes Janitor against your current kubeconfig context,
e.g. local [Minikube](https://github.com/kubernetes/minikube):

```{.sourceCode .bash}
$ go mod download
$ go run cmd/kube-janitor/main.go --dry-run --debug --once
```

To run tests and generate coverage report:

```{.sourceCode .bash}
$ go test ./... -v -cover
```
