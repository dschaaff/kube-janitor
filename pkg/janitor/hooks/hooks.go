package hooks

import (
	"fmt"
)

// ResourceContextHook is a function that can extend the context with custom information
type ResourceContextHook func(resource interface{}, cache map[string]interface{}) map[string]interface{}

// Hook represents a resource context hook
type Hook struct {
	Name string
	Func ResourceContextHook
}

// RegisteredHooks contains all available hooks
var RegisteredHooks = map[string]Hook{
	"random_dice": {
		Name: "random_dice",
		Func: RandomDice,
	},
}

// GetHook returns a hook by name
func GetHook(name string) (ResourceContextHook, error) {
	if hook, ok := RegisteredHooks[name]; ok {
		return hook.Func, nil
	}
	return nil, fmt.Errorf("hook %q not found", name)
}
