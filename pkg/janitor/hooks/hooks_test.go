package hooks

import (
	"testing"
)

func TestRandomDice(t *testing.T) {
	// Test with empty cache
	cache := make(map[string]interface{})
	resource := map[string]interface{}{
		"kind": "Pod",
		"metadata": map[string]interface{}{
			"name": "test-pod",
		},
	}

	result := RandomDice(resource, cache)

	// Verify result format
	diceValue, ok := result["random_dice"].(int)
	if !ok {
		t.Error("Expected random_dice to be an integer")
	}

	// Verify value range
	if diceValue < 1 || diceValue > 6 {
		t.Errorf("Expected dice value between 1 and 6, got %d", diceValue)
	}

	// Test cache reuse
	cached := RandomDice(resource, cache)
	if cached["random_dice"] != diceValue {
		t.Error("Expected cached dice value to be reused")
	}
}
