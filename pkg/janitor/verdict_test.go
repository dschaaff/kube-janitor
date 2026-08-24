package janitor

import (
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// now is the fixed clock every case in this file is judged against.
var now = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

// podObject builds a pod created at the given age, with the given annotations.
func podObject(namespace, name string, age time.Duration, annotations map[string]string) *unstructured.Unstructured {
	metadata := map[string]interface{}{
		"name":              name,
		"namespace":         namespace,
		"uid":               namespace + "/" + name,
		"creationTimestamp": now.Add(-age).Format(time.RFC3339),
	}
	if annotations != nil {
		metadata["annotations"] = toStringMap(annotations)
	}

	return &unstructured.Unstructured{Object: map[string]interface{}{
		"kind":       "Pod",
		"apiVersion": "v1",
		"metadata":   metadata,
	}}
}

// pod builds a target for the same pod.
func pod(t *testing.T, age time.Duration, annotations map[string]string) Target {
	t.Helper()

	return mustTarget(t, podObject("staging", "web", age, annotations), podResourceType)
}

func toStringMap(in map[string]string) map[string]interface{} {
	out := map[string]interface{}{}
	for k, v := range in {
		out[k] = v
	}
	return out
}

func mustRules(t *testing.T, rules ...Rule) []Rule {
	t.Helper()

	for i := range rules {
		if err := rules[i].ValidateAndCompile(); err != nil {
			t.Fatalf("rule %s does not compile: %v", rules[i].ID, err)
		}
	}
	return rules
}

func TestActionString(t *testing.T) {
	for action, want := range map[Action]string{
		ActionNone:   "none",
		ActionDelete: "delete",
		ActionNotify: "notify",
		Action(99):   "none",
	} {
		if got := action.String(); got != want {
			t.Errorf("Action(%d).String() = %q, want %q", action, got, want)
		}
	}
}

func TestDecideAction(t *testing.T) {
	matchAll := func(id, ttl string) Rule {
		return Rule{ID: id, Resources: []string{"*"}, JMESPath: "metadata.name", TTL: ttl}
	}

	tests := []struct {
		name       string
		target     Target
		cfg        *Config
		wantAction Action
		wantSource string
	}{
		{
			name:       "no annotations and no rules",
			target:     pod(t, time.Hour, nil),
			cfg:        &Config{},
			wantAction: ActionNone,
		},
		{
			name:       "expiry in the past deletes",
			target:     pod(t, 0, map[string]string{ExpiryAnnotation: "2026-05-01T00:00:00Z"}),
			cfg:        &Config{},
			wantAction: ActionDelete,
			wantSource: "annotation " + ExpiryAnnotation,
		},
		{
			name:       "expiry in the future waits",
			target:     pod(t, 0, map[string]string{ExpiryAnnotation: "2026-07-01T00:00:00Z"}),
			cfg:        &Config{},
			wantAction: ActionNone,
			wantSource: "annotation " + ExpiryAnnotation,
		},
		{
			name:       "date-only expiry is midnight UTC",
			target:     pod(t, 0, map[string]string{ExpiryAnnotation: "2026-05-31"}),
			cfg:        &Config{},
			wantAction: ActionDelete,
			wantSource: "annotation " + ExpiryAnnotation,
		},
		{
			name:       "ttl older than the resource deletes",
			target:     pod(t, 2*time.Hour, map[string]string{TTLAnnotation: "1h"}),
			cfg:        &Config{},
			wantAction: ActionDelete,
			wantSource: "annotation " + TTLAnnotation,
		},
		{
			name:       "ttl not yet reached waits",
			target:     pod(t, 30*time.Minute, map[string]string{TTLAnnotation: "1h"}),
			cfg:        &Config{},
			wantAction: ActionNone,
			wantSource: "annotation " + TTLAnnotation,
		},
		{
			name:       "unlimited ttl is never deleted",
			target:     pod(t, 10000*time.Hour, map[string]string{TTLAnnotation: TTLUnlimited}),
			cfg:        &Config{},
			wantAction: ActionNone,
			wantSource: "annotation " + TTLAnnotation,
		},
		{
			name:       "notification window reached",
			target:     pod(t, 55*time.Minute, map[string]string{TTLAnnotation: "1h"}),
			cfg:        &Config{DeleteNotification: 600},
			wantAction: ActionNotify,
			wantSource: "annotation " + TTLAnnotation,
		},
		{
			name:       "notification window not yet reached",
			target:     pod(t, 30*time.Minute, map[string]string{TTLAnnotation: "1h"}),
			cfg:        &Config{DeleteNotification: 600},
			wantAction: ActionNone,
			wantSource: "annotation " + TTLAnnotation,
		},
		{
			name: "already notified is not notified again",
			target: pod(t, 55*time.Minute, map[string]string{
				TTLAnnotation:      "1h",
				NotifiedAnnotation: "yes",
			}),
			cfg:        &Config{DeleteNotification: 600},
			wantAction: ActionNone,
			wantSource: "annotation " + TTLAnnotation,
		},
		{
			name:       "notification is not sent when the deadline has passed",
			target:     pod(t, 2*time.Hour, map[string]string{TTLAnnotation: "1h"}),
			cfg:        &Config{DeleteNotification: 600},
			wantAction: ActionDelete,
			wantSource: "annotation " + TTLAnnotation,
		},
		{
			name:       "matching rule deletes",
			target:     pod(t, 2*time.Hour, nil),
			cfg:        &Config{Rules: mustRules(t, matchAll("stale-pods", "1h"))},
			wantAction: ActionDelete,
			wantSource: "rule stale-pods",
		},
		{
			name:   "first matching rule wins",
			target: pod(t, 2*time.Hour, nil),
			cfg: &Config{Rules: mustRules(t,
				matchAll("first", "1h"),
				matchAll("second", "10m"),
			)},
			wantAction: ActionDelete,
			wantSource: "rule first",
		},
		{
			name:   "a rule with an unlimited ttl is passed over",
			target: pod(t, 2*time.Hour, nil),
			cfg: &Config{Rules: mustRules(t,
				matchAll("keep", TTLUnlimited),
				matchAll("reap", "1h"),
			)},
			wantAction: ActionDelete,
			wantSource: "rule reap",
		},
		{
			name:   "a rule that does not match is skipped",
			target: pod(t, 2*time.Hour, nil),
			cfg: &Config{Rules: mustRules(t,
				Rule{ID: "other", Resources: []string{"Deployments"}, JMESPath: "metadata.name", TTL: "1h"},
			)},
			wantAction: ActionNone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Decide(tt.target, tt.cfg, now, nil)
			if err != nil {
				t.Fatalf("Decide() error = %v", err)
			}
			if got.Action != tt.wantAction {
				t.Errorf("Action = %v, want %v", got.Action, tt.wantAction)
			}
			if got.Source != tt.wantSource {
				t.Errorf("Source = %q, want %q", got.Source, tt.wantSource)
			}
		})
	}
}

// The expiry annotation is checked before the TTL annotation, and only one
// deadline is ever acted on. See docs/adr/0001-deadline-precedence.md.
func TestDecidePrecedence(t *testing.T) {
	tests := []struct {
		name       string
		target     Target
		wantAction Action
		wantSource string
	}{
		{
			name: "expiry wins over an expired ttl",
			target: pod(t, 2*time.Hour, map[string]string{
				ExpiryAnnotation: "2026-07-01T00:00:00Z",
				TTLAnnotation:    "1h",
			}),
			// The TTL alone would delete. The unexpired expiry annotation wins, so
			// nothing happens.
			wantAction: ActionNone,
			wantSource: "annotation " + ExpiryAnnotation,
		},
		{
			name: "expired expiry deletes once, not twice",
			target: pod(t, 2*time.Hour, map[string]string{
				ExpiryAnnotation: "2026-05-01T00:00:00Z",
				TTLAnnotation:    "1h",
			}),
			wantAction: ActionDelete,
			wantSource: "annotation " + ExpiryAnnotation,
		},
		{
			name: "ttl annotation wins over a matching rule",
			target: pod(t, 30*time.Minute, map[string]string{
				TTLAnnotation: "1h",
			}),
			wantAction: ActionNone,
			wantSource: "annotation " + TTLAnnotation,
		},
	}

	rules := mustRules(t, Rule{
		ID: "reap-everything", Resources: []string{"*"}, JMESPath: "metadata.name", TTL: "1s",
	})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Decide(tt.target, &Config{Rules: rules}, now, nil)
			if err != nil {
				t.Fatalf("Decide() error = %v", err)
			}
			if got.Action != tt.wantAction {
				t.Errorf("Action = %v, want %v", got.Action, tt.wantAction)
			}
			if got.Source != tt.wantSource {
				t.Errorf("Source = %q, want %q", got.Source, tt.wantSource)
			}
		})
	}
}

// Rules must be evaluated for resources that carry no annotations at all. The
// code this replaced returned early on a nil annotation map, so such resources
// never reached rule evaluation.
func TestDecideAppliesRulesToUnannotatedResources(t *testing.T) {
	target := mustTarget(t, &unstructured.Unstructured{Object: map[string]interface{}{
		"kind":       "Deployment",
		"apiVersion": "apps/v1",
		"metadata": map[string]interface{}{
			"name":              "no-annotations-at-all",
			"namespace":         "staging",
			"creationTimestamp": now.Add(-2 * time.Hour).Format(time.RFC3339),
		},
	}}, deploymentResourceType)

	if target.Annotations != nil {
		t.Fatalf("Annotations = %v, want nil for this case to be meaningful", target.Annotations)
	}

	cfg := &Config{Rules: mustRules(t, Rule{
		ID: "unlabelled", Resources: []string{"*"}, JMESPath: "metadata.name", TTL: "1h",
	})}

	got, err := Decide(target, cfg, now, nil)
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if got.Action != ActionDelete {
		t.Errorf("Action = %v, want %v", got.Action, ActionDelete)
	}
}

// Namespaces are listed through the typed client, which leaves TypeMeta empty.
// Matching on the raw resource's "kind" therefore rejected every namespace before
// any JMESPath ran, which made the temporary-pr-namespaces example in
// deploy/rules.yaml a no-op. Matching on the listed plural fixes it.
func TestDecideMatchesRulesAgainstNamespaces(t *testing.T) {
	target := mustTarget(t, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "pr-42",
			CreationTimestamp: metav1.Time{Time: now.Add(-2 * time.Hour)},
		},
	}, namespaceResourceType)

	for _, resources := range [][]string{{"*"}, {"namespaces"}} {
		cfg := &Config{Rules: mustRules(t, Rule{
			ID: "temporary-pr-namespaces", Resources: resources,
			JMESPath: "starts_with(metadata.name, 'pr-')", TTL: "1h",
		})}

		got, err := Decide(target, cfg, now, nil)
		if err != nil {
			t.Fatalf("Decide() error = %v", err)
		}
		if got.Action != ActionDelete {
			t.Errorf("resources %v: Action = %v, want %v", resources, got.Action, ActionDelete)
		}
	}
}

func TestDecideInvalidValues(t *testing.T) {
	tests := []struct {
		name   string
		target Target
		cfg    *Config
	}{
		{
			name:   "unparseable expiry",
			target: pod(t, 0, map[string]string{ExpiryAnnotation: "invalid-date"}),
			cfg:    &Config{},
		},
		{
			name:   "unparseable ttl",
			target: pod(t, 0, map[string]string{TTLAnnotation: "1 fortnight"}),
			cfg:    &Config{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Decide(tt.target, tt.cfg, now, nil); err == nil {
				t.Error("Decide() error = nil, want an error")
			}
		})
	}
}

func TestDecideDeploymentTimeAnnotation(t *testing.T) {
	cfg := &Config{DeploymentTimeAnnotation: "deployed-at"}

	tests := []struct {
		name       string
		deployedAt string
		wantAction Action
	}{
		{
			// Deployed recently, so a 1h TTL has not run out even though the
			// resource itself is old.
			name:       "annotation is used in place of the creation timestamp",
			deployedAt: now.Add(-10 * time.Minute).Format(time.RFC3339),
			wantAction: ActionNone,
		},
		{
			name:       "unparseable annotation falls back to the creation timestamp",
			deployedAt: "yesterday",
			wantAction: ActionDelete,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := pod(t, 5*time.Hour, map[string]string{
				TTLAnnotation: "1h",
				"deployed-at": tt.deployedAt,
			})

			got, err := Decide(target, cfg, now, nil)
			if err != nil {
				t.Fatalf("Decide() error = %v", err)
			}
			if got.Action != tt.wantAction {
				t.Errorf("Action = %v, want %v", got.Action, tt.wantAction)
			}
		})
	}
}

// Building resource context costs several cluster reads, so it must only happen
// when rule evaluation is actually reached.
func TestDecideResolvesResourceContextLazily(t *testing.T) {
	tests := []struct {
		name       string
		target     Target
		cfg        *Config
		wantCalls  int
		wantAction Action
	}{
		{
			name:       "ttl annotation short-circuits rule evaluation",
			target:     pod(t, 2*time.Hour, map[string]string{TTLAnnotation: "1h"}),
			cfg:        &Config{Rules: mustRules(t, Rule{ID: "r", Resources: []string{"*"}, JMESPath: "_context.reap", TTL: "1h"})},
			wantCalls:  0,
			wantAction: ActionDelete,
		},
		{
			name:       "no rules configured needs no context",
			target:     pod(t, 2*time.Hour, nil),
			cfg:        &Config{},
			wantCalls:  0,
			wantAction: ActionNone,
		},
		{
			name:       "rules are evaluated against the context",
			target:     pod(t, 2*time.Hour, nil),
			cfg:        &Config{Rules: mustRules(t, Rule{ID: "r", Resources: []string{"*"}, JMESPath: "_context.reap", TTL: "1h"})},
			wantCalls:  1,
			wantAction: ActionDelete,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			contextFn := func() map[string]interface{} {
				calls++
				return map[string]interface{}{"reap": true}
			}

			got, err := Decide(tt.target, tt.cfg, now, contextFn)
			if err != nil {
				t.Fatalf("Decide() error = %v", err)
			}
			if calls != tt.wantCalls {
				t.Errorf("resource context resolved %d times, want %d", calls, tt.wantCalls)
			}
			if got.Action != tt.wantAction {
				t.Errorf("Action = %v, want %v", got.Action, tt.wantAction)
			}
		})
	}
}

func TestDecideMessages(t *testing.T) {
	// Timestamps in messages are rendered in whatever zone the resource's
	// creation timestamp parsed into, so expectations are built from the target
	// rather than hard-coded.
	tests := []struct {
		name            string
		target          Target
		cfg             *Config
		wantEventReason string
		wantMessage     func(t Target) string
	}{
		{
			// The raw annotation value is quoted verbatim, not the parsed deadline
			// reformatted: a date-only annotation stays "2026-05-01" rather than
			// becoming "2026-05-01T00:00:00Z".
			name:            "expiry deletion quotes the annotation value verbatim",
			target:          pod(t, 0, map[string]string{ExpiryAnnotation: "2026-05-01"}),
			cfg:             &Config{},
			wantEventReason: "ExpiryTimeReached",
			wantMessage: func(Target) string {
				return "Pod staging/web expired on 2026-05-01 and will be deleted " +
					"(annotation janitor/expires is set)"
			},
		},
		{
			name:            "ttl deletion quotes the ttl and its origin",
			target:          pod(t, 2*time.Hour, map[string]string{TTLAnnotation: "1h"}),
			cfg:             &Config{},
			wantEventReason: "TTLExpired",
			wantMessage: func(target Target) string {
				from := target.CreatedAt
				return "Pod staging/web expired on " + from.Add(time.Hour).Format(time.RFC3339) +
					" and will be deleted (TTL 1h from " + from.Format(time.RFC3339) + ")"
			},
		},
		{
			name:   "rule deletion names the rule",
			target: pod(t, 2*time.Hour, nil),
			cfg: &Config{Rules: mustRules(t, Rule{
				ID: "stale-pods", Resources: []string{"*"}, JMESPath: "metadata.name", TTL: "1h",
			})},
			wantEventReason: "RuleTTLExpired",
			wantMessage: func(target Target) string {
				from := target.CreatedAt
				return "Pod staging/web expired on " + from.Add(time.Hour).Format(time.RFC3339) +
					" and will be deleted (rule stale-pods, TTL 1h from " + from.Format(time.RFC3339) + ")"
			},
		},
		{
			name:            "notification states when the deletion will happen",
			target:          pod(t, 55*time.Minute, map[string]string{TTLAnnotation: "1h"}),
			cfg:             &Config{DeleteNotification: 600},
			wantEventReason: "DeleteNotification",
			wantMessage: func(target Target) string {
				from := target.CreatedAt
				return "Pod staging/web will be deleted at " + from.Add(time.Hour).Format(time.RFC3339) +
					" (TTL 1h from " + from.Format(time.RFC3339) + ")"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Decide(tt.target, tt.cfg, now, nil)
			if err != nil {
				t.Fatalf("Decide() error = %v", err)
			}
			if got.EventReason != tt.wantEventReason {
				t.Errorf("EventReason = %q, want %q", got.EventReason, tt.wantEventReason)
			}
			if want := tt.wantMessage(tt.target); got.Message != want {
				t.Errorf("Message  = %q\nwant     = %q", got.Message, want)
			}
		})
	}
}

// The cluster a Notification came from is named when the Verdict is reached, so
// everything that goes on to report it says the same thing.
func TestDecideNamesTheClusterInANotification(t *testing.T) {
	target := pod(t, 55*time.Minute, map[string]string{TTLAnnotation: "1h"})

	got, err := Decide(target, &Config{DeleteNotification: 600, ContextName: "prod-eu"}, now, nil)
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}

	if got.Action != ActionNotify {
		t.Fatalf("Action = %v, want %v", got.Action, ActionNotify)
	}
	if !strings.HasPrefix(got.Message, "[prod-eu] ") {
		t.Errorf("Message = %q, want it to name the cluster", got.Message)
	}
}

// A delete says nothing about the cluster: only a Notification leaves it.
func TestDecideNamesNoClusterInADelete(t *testing.T) {
	target := pod(t, 2*time.Hour, map[string]string{TTLAnnotation: "1h"})

	got, err := Decide(target, &Config{DeleteNotification: 600, ContextName: "prod-eu"}, now, nil)
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}

	if got.Action != ActionDelete {
		t.Fatalf("Action = %v, want %v", got.Action, ActionDelete)
	}
	if strings.Contains(got.Message, "prod-eu") {
		t.Errorf("Message = %q, want no cluster name on a delete", got.Message)
	}
}
