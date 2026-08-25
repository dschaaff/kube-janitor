package janitor

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestRuleValidation(t *testing.T) {
	tests := []struct {
		name    string
		rule    Rule
		wantErr bool
	}{
		{
			name: "valid rule",
			rule: Rule{
				ID:        "test-rule",
				Resources: []string{"pods"},
				JMESPath:  "metadata.labels.test",
				TTL:       "7d",
			},
			wantErr: false,
		},
		{
			name: "invalid rule ID",
			rule: Rule{
				ID:        "Test_Rule",
				Resources: []string{"pods"},
				JMESPath:  "metadata.labels.test",
				TTL:       "7d",
			},
			wantErr: true,
		},
		{
			name: "invalid TTL",
			rule: Rule{
				ID:        "test-rule",
				Resources: []string{"pods"},
				JMESPath:  "metadata.labels.test",
				TTL:       "7x",
			},
			wantErr: true,
		},
		{
			name: "invalid JMESPath",
			rule: Rule{
				ID:        "test-rule",
				Resources: []string{"pods"},
				JMESPath:  "[invalid",
				TTL:       "7d",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.rule.ValidateAndCompile()
			if (err != nil) != tt.wantErr {
				t.Errorf("Rule.ValidateAndCompile() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestRuleMatches(t *testing.T) {
	// Held constant in every case whose axis is the resources list rather than the
	// expression.
	const labelIsTrue = "metadata.labels.test == 'true'"

	services := ResourceType{Version: "v1", Kind: "Service", Plural: "services", Namespaced: true}

	labelled := func(rt ResourceType, value string) Target {
		return mustTarget(t, &unstructured.Unstructured{Object: map[string]interface{}{
			"metadata": map[string]interface{}{
				"name":      "x",
				"namespace": "default",
				"labels":    map[string]interface{}{"test": value},
			},
		}}, rt)
	}

	tests := []struct {
		name      string
		resources []string
		jmespath  string
		target    Target
		context   map[string]interface{}
		want      bool
	}{
		{
			name:      "matching resource type and expression",
			resources: []string{"pods"},
			jmespath:  labelIsTrue,
			target:    labelled(podResourceType, "true"),
			want:      true,
		},
		{
			name:      "non-matching resource type",
			resources: []string{"pods"},
			jmespath:  labelIsTrue,
			target:    labelled(services, "true"),
			want:      false,
		},
		{
			name:      "non-matching expression",
			resources: []string{"pods"},
			jmespath:  labelIsTrue,
			target:    labelled(podResourceType, "false"),
			want:      false,
		},
		{
			name:      "star matches any resource type",
			resources: []string{"*"},
			jmespath:  labelIsTrue,
			target:    labelled(services, "true"),
			want:      true,
		},
		{
			// The plural comes from the listed Resource type, so a rule naming an
			// irregular plural applies. Guessing it from the kind produced
			// "ingresss", and such a rule never fired.
			name:      "irregular plural",
			resources: []string{"ingresses"},
			jmespath:  labelIsTrue,
			target:    labelled(ingressResourceType, "true"),
			want:      true,
		},
		{
			name:      "resource context is available to the expression",
			resources: []string{"*"},
			jmespath:  "_context.pvc_is_not_mounted",
			target:    labelled(podResourceType, "true"),
			context:   map[string]interface{}{"pvc_is_not_mounted": true},
			want:      true,
		},
		{
			name:      "resource context that does not hold",
			resources: []string{"*"},
			jmespath:  "_context.pvc_is_not_mounted",
			target:    labelled(podResourceType, "true"),
			context:   map[string]interface{}{"pvc_is_not_mounted": false},
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := Rule{ID: "test-rule", Resources: tt.resources, JMESPath: tt.jmespath, TTL: "7d"}
			if err := rule.ValidateAndCompile(); err != nil {
				t.Fatalf("ValidateAndCompile() error = %v", err)
			}

			if got := rule.Matches(tt.target, tt.context); got != tt.want {
				t.Errorf("Matches() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLoadRules(t *testing.T) {
	// Create a temporary rules file
	content := `
rules:
- id: test-rule-1
  resources: ["pods"]
  jmespath: "metadata.labels.test == 'true'"
  ttl: "7d"
- id: test-rule-2
  resources: ["deployments"]
  jmespath: "metadata.labels.environment == 'test'"
  ttl: "24h"
`
	path := writeRulesFile(t, content)

	// Test loading rules
	rules, err := loadRules(path)
	if err != nil {
		t.Fatalf("loadRules() error = %v", err)
	}

	if len(rules) != 2 {
		t.Errorf("loadRules() got %d rules, want 2", len(rules))
	}

	// Test loading invalid file
	_, err = loadRules("nonexistent.yaml")
	if err == nil {
		t.Error("loadRules() expected error for nonexistent file")
	}
}
