package hooks

import (
    "testing"
)

func TestRandomDiceCache(t *testing.T) {
    cache := make(map[string]interface{})
    resource := map[string]interface{}{
        "kind": "Pod",
        "metadata": map[string]interface{}{
            "name": "test-pod",
        },
    }

    // First call should generate and cache a value
    result1 := RandomDice(resource, cache)
    diceValue1, ok := result1["random_dice"].(int)
    if !ok {
        t.Error("Expected random_dice to be an integer")
    }
    if diceValue1 < 1 || diceValue1 > 6 {
        t.Errorf("Expected dice value between 1 and 6, got %d", diceValue1)
    }

    // Second call should use cached value
    result2 := RandomDice(resource, cache)
    diceValue2, ok := result2["random_dice"].(int)
    if !ok {
        t.Error("Expected random_dice to be an integer")
    }
    if diceValue1 != diceValue2 {
        t.Errorf("Expected same dice value, got %d and %d", diceValue1, diceValue2)
    }

    // Verify cache key exists
    if _, exists := cache[CacheKeyRandomDice]; !exists {
        t.Error("Expected dice value to be cached")
    }
}
