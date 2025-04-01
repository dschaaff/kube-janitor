package hooks

import (
    "math/rand"
    "time"
)

const CacheKeyRandomDice = "random_dice"

// RandomDice is a built-in example resource context hook that sets _context.random_dice 
// to a random dice value (1-6)
func RandomDice(resource interface{}, cache map[string]interface{}) map[string]interface{} {
    // Re-use any value from the cache to have only one dice roll per janitor run
    if diceValue, ok := cache[CacheKeyRandomDice]; ok {
        return map[string]interface{}{
            "random_dice": diceValue,
        }
    }

    // Roll the dice
    r := rand.New(rand.NewSource(time.Now().UnixNano()))
    diceValue := r.Intn(6) + 1 // 1-6
    
    // Cache the value
    cache[CacheKeyRandomDice] = diceValue

    return map[string]interface{}{
        "random_dice": diceValue,
    }
}
