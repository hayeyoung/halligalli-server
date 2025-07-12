package game

import (
    "math/rand"
    "time"
)

func CreateDeck() []Card {
    var deck []Card
    for fruit := 0; fruit < 4; fruit++ {
        for count := 1; count <= 5; count++ {
            for i := 0; i < 4; i++ {
                deck = append(deck, Card{FruitIndex: fruit, FruitCount: count})
            }
        }
    }
    return deck
}

func ShuffleDeck(deck []Card) {
    rand.Seed(time.Now().UnixNano())
    rand.Shuffle(len(deck), func(i, j int) {
        deck[i], deck[j] = deck[j], deck[i]
    })
}

func DealCards(deck []Card, playerCount int) [][]Card {
    cardsPerPlayer := len(deck) / playerCount
    hands := make([][]Card, playerCount)
    for i := 0; i < playerCount; i++ {
        start := i * cardsPerPlayer
        end := start + cardsPerPlayer
        hands[i] = deck[start:end]
    }
    return hands
}