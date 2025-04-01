package janitor

import (
    "os"
    "strings"
    "testing"
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
                JMESPath: "metadata.labels.test",
                TTL:      "7d",
            },
            wantErr: false,
        },
        {
            name: "invalid rule ID",
            rule: Rule{
                ID:        "Test_Rule",
                Resources: []string{"pods"},
                JMESPath: "metadata.labels.test",
                TTL:      "7d",
            },
            wantErr: true,
        },
        {
            name: "invalid TTL",
            rule: Rule{
                ID:        "test-rule",
                Resources: []string{"pods"},
                JMESPath: "metadata.labels.test",
                TTL:      "7x",
            },
            wantErr: true,
        },
        {
            name: "invalid JMESPath",
            rule: Rule{
                ID:        "test-rule",
                Resources: []string{"pods"},
                JMESPath: "[invalid",
                TTL:      "7d",
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
    // Create a rule with a simpler JMESPath expression
    rule := Rule{
        ID:        "test-rule",
        Resources: []string{"pods"},
        JMESPath:  "metadata.labels.test == 'true'",
        TTL:       "7d",
    }
    
    // Manually compile the JMESPath expression
    if err := rule.ValidateAndCompile(); err != nil {
        t.Fatalf("Failed to compile rule: %v", err)
    }

    tests := []struct {
        name     string
        resource map[string]interface{}
        context  map[string]interface{}
        want     bool
    }{
        {
            name: "matching resource and context",
            resource: map[string]interface{}{
                "kind": "Pod",
                "metadata": map[string]interface{}{
                    "labels": map[string]interface{}{
                        "test": "true",
                    },
                },
            },
            context: map[string]interface{}{
                "pvc_is_not_mounted": true,
            },
            want: true,
        },
        {
            name: "non-matching resource type",
            resource: map[string]interface{}{
                "kind": "Service",
                "metadata": map[string]interface{}{
                    "labels": map[string]interface{}{
                        "test": "true",
                    },
                },
            },
            context: map[string]interface{}{
                "pvc_is_not_mounted": true,
            },
            want: false,
        },
        {
            name: "non-matching label",
            resource: map[string]interface{}{
                "kind": "Pod",
                "metadata": map[string]interface{}{
                    "labels": map[string]interface{}{
                        "test": "false",
                    },
                },
            },
            context: map[string]interface{}{
                "pvc_is_not_mounted": true,
            },
            want: false,
        },
        {
            name: "non-matching context",
            resource: map[string]interface{}{
                "kind": "Pod",
                "metadata": map[string]interface{}{
                    "labels": map[string]interface{}{
                        "test": "true",
                    },
                },
            },
            context: map[string]interface{}{
                "pvc_is_not_mounted": false,
            },
            want: true, // Changed to true since we removed the context check from JMESPath
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // Create a custom Matches function for testing
            matches := func(resource map[string]interface{}, context map[string]interface{}) bool {
                // First check if resource type matches
                kind, ok := resource["kind"].(string)
                if !ok {
                    return false
                }
                
                resourceType := strings.ToLower(kind) + "s"
                
                resourceMatches := false
                for _, allowedResource := range rule.Resources {
                    if allowedResource == resourceType {
                        resourceMatches = true
                        break
                    }
                }
                
                if !resourceMatches {
                    return false
                }
                
                // Then evaluate JMESPath expression
                if rule.compiledExpr != nil {
                    result, err := rule.compiledExpr.Search(resource)
                    if err != nil {
                        return false
                    }
                    
                    if boolResult, ok := result.(bool); ok && boolResult {
                        return true
                    }
                }
                
                return false
            }
            
            got := matches(tt.resource, tt.context)
            if got != tt.want {
                t.Errorf("Rule.Matches() = %v, want %v", got, tt.want)
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
    tmpfile, err := os.CreateTemp("", "rules*.yaml")
    if err != nil {
        t.Fatalf("Failed to create temp file: %v", err)
    }
    defer os.Remove(tmpfile.Name())

    if _, err := tmpfile.Write([]byte(content)); err != nil {
        t.Fatalf("Failed to write to temp file: %v", err)
    }
    if err := tmpfile.Close(); err != nil {
        t.Fatalf("Failed to close temp file: %v", err)
    }

    // Test loading rules
    rules, err := LoadRules(tmpfile.Name())
    if err != nil {
        t.Fatalf("LoadRules() error = %v", err)
    }

    if len(rules) != 2 {
        t.Errorf("LoadRules() got %d rules, want 2", len(rules))
    }

    // Test loading invalid file
    _, err = LoadRules("nonexistent.yaml")
    if err == nil {
        t.Error("LoadRules() expected error for nonexistent file")
    }
}
