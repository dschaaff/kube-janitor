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

// rulesFile represents the structure of the YAML rules file
type rulesFile struct {
	Rules []Rule `yaml:"rules"`
}

// ValidateAndCompile validates the rule and compiles its JMESPath expression
func (r *Rule) ValidateAndCompile() error {
	// Validate rule ID
	if !ruleIDPattern.MatchString(r.ID) {
		return fmt.Errorf("invalid rule ID %q: must match ^[a-z][a-z0-9-]*$", r.ID)
	}

	// Validate TTL format
	if _, err := parseTTL(r.TTL); err != nil {
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

// Matches checks if the rule matches the given target and context. The
// resources list is matched against the plural the target was listed as, so a
// rule naming an irregular plural applies to it.
func (r *Rule) Matches(t Target, context map[string]interface{}) bool {
	matches := false
	for _, allowedResource := range r.Resources {
		if allowedResource == "*" || allowedResource == t.plural() {
			matches = true
			break
		}
	}
	if !matches {
		return false
	}

	// Add context to resource for JMESPath evaluation
	data := make(map[string]interface{}, len(t.Raw)+1)
	for k, v := range t.Raw {
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

// loadRules loads rules from a YAML file
func loadRules(filename string) ([]Rule, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read rules file: %v", err)
	}

	var file rulesFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("failed to parse rules file: %v", err)
	}

	// Validate and compile all rules
	for i := range file.Rules {
		if err := file.Rules[i].ValidateAndCompile(); err != nil {
			return nil, fmt.Errorf("invalid rule #%d: %v", i, err)
		}
	}

	return file.Rules, nil
}
