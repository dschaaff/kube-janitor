package janitor

import (
	"fmt"
	"os"
	"regexp"

	"github.com/jmespath/go-jmespath"
	"gopkg.in/yaml.v3"
)

var ruleIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// Rule defines a TTL rule that can be applied to Kubernetes resources
type Rule struct {
	ID        string   `yaml:"id"`
	Resources []string `yaml:"resources"`
	JMESPath  string   `yaml:"jmespath"`
	TTL       string   `yaml:"ttl"`

	// Compiled JMESPath expression
	compiledExpr *jmespath.JMESPath
}

// RulesFile represents the structure of the YAML rules file
type RulesFile struct {
	Rules []Rule `yaml:"rules"`
}


// ValidateAndCompile validates the rule and compiles its JMESPath expression
func (r *Rule) ValidateAndCompile() error {
	// Validate rule ID
	if !ruleIDPattern.MatchString(r.ID) {
		return fmt.Errorf("invalid rule ID %q: must match ^[a-z][a-z0-9-]*$", r.ID)
	}

	// Validate TTL format
	if _, err := ParseTTL(r.TTL); err != nil {
		return fmt.Errorf("invalid TTL %q in rule %s: %v", r.TTL, r.ID, err)
	}

	// Compile JMESPath expression
	expr, err := jmespath.Compile(r.JMESPath)
	if err != nil {
		return fmt.Errorf("invalid JMESPath expression in rule %s: %v", r.ID, err)
	}
	r.compiledExpr = expr

	return nil
}

// Matches checks if the rule matches the given resource and context
func (r *Rule) Matches(resource map[string]interface{}, context map[string]interface{}) bool {
	// Check if resource type matches
	kind, ok := resource["kind"].(string)
	if !ok {
		return false
	}
	resourceType := fmt.Sprintf("%ss", kind)

	matches := false
	for _, allowedResource := range r.Resources {
		if allowedResource == "*" || allowedResource == resourceType {
			matches = true
			break
		}
	}
	if !matches {
		return false
	}

	// Add context to resource for JMESPath evaluation
	data := make(map[string]interface{})
	for k, v := range resource {
		data[k] = v
	}
	data["_context"] = context

	// Evaluate JMESPath expression
	result, err := r.compiledExpr.Search(data)
	if err != nil {
		return false
	}

	// Convert result to boolean
	switch v := result.(type) {
	case bool:
		return v
	case string:
		return v != ""
	case []interface{}:
		return len(v) > 0
	case map[string]interface{}:
		return len(v) > 0
	default:
		return false
	}
}

// LoadRules loads rules from a YAML file
func LoadRules(filename string) ([]Rule, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read rules file: %v", err)
	}

	var rulesFile RulesFile
	if err := yaml.Unmarshal(data, &rulesFile); err != nil {
		return nil, fmt.Errorf("failed to parse rules file: %v", err)
	}

	// Validate and compile all rules
	for i := range rulesFile.Rules {
		if err := rulesFile.Rules[i].ValidateAndCompile(); err != nil {
			return nil, fmt.Errorf("invalid rule #%d: %v", i, err)
		}
	}

	return rulesFile.Rules, nil
}
