// filepath: /Users/hayeyeong/Desktop/APPS/APPS_/halligalli-server/packet.go
package main

import (
    "encoding/json"
    "log"
    "halligalli-server/game"
)

type Packet struct {
    Type string `json:"type"`
}

type GameStartPacket struct {
    Type             string `json:"type"`
    TotalPlayerCount int    `json:"totalPlayerCount"`
    StartCardCount   int    `json:"startCardCount"`
}

type CardOpenPacket struct {
    Type        string `json:"type"`
    PlayerIndex int    `json:"playerIndex"`
    FruitIndex  int    `json:"fruitIndex"`
    FruitCount  int    `json:"fruitCount"`
}

type BellCorrectPacket struct {
    Type        string `json:"type"`
    PlayerIndex int    `json:"playerIndex"`
}

type BellIncorrectPacket struct {
    Type        string `json:"type"`
    PlayerIndex int    `json:"playerIndex"`
}

type GameEndPacket struct {
    Type        string `json:"type"`
    WinnerIndex int    `json:"winnerIndex"`
}

type BellRingPacket struct {
    Type string `json:"type"`
}

func handlePacket(client *Client, msg []byte) {
    var base Packet
    if err := json.Unmarshal(msg, &base); err != nil {
        log.Println("패킷 파싱 실패:", err)
        return
    }

    switch base.Type {
    case "GameStart":
        if client.playerIdx == 0 && !game.room.GameStarted {
            game.room.GameStarted = true
            deck := game.CreateDeck()
            game.ShuffleDeck(deck)
            hands := game.DealCards(deck, len(game.room.Players))
            game.room.PlayerHands = hands

            start := GameStartPacket{
                Type:             "GameStart",
                TotalPlayerCount: len(game.room.Players),
                StartCardCount:   len(hands[0]),
            }
            sendToAll(start)
        }
    case "CardOpen":
        if game.room.GameStarted {
            idx := client.playerIdx
            if len(game.room.PlayerHands[idx]) == 0 {
                return
            }
            card := game.room.PlayerHands[idx][0]
            game.room.PlayerHands[idx] = game.room.PlayerHands[idx][1:]
            game.room.RevealedCards = append(game.room.RevealedCards, card)
            pkt := CardOpenPacket{
                Type:        "CardOpen",
                PlayerIndex: idx,
                FruitIndex:  card.FruitIndex,
                FruitCount:  card.FruitCount,
            }
            sendToAll(pkt)

            if len(game.room.PlayerHands[idx]) == 0 {
                end := GameEndPacket{
                    Type:        "GameEnd",
                    WinnerIndex: idx,
                }
                sendToAll(end)
                game.room.GameStarted = false
            }
        }
    case "BellRing":
    if game.room.GameStarted {
        // 과일별 개수 집계
        fruitCount := make(map[int]int)
        for _, card := range game.room.RevealedCards {
            fruitCount[card.FruitIndex] += card.FruitCount
        }
        correct := false
        for _, count := range fruitCount {
            if count == 5 {
                correct = true
                break
            }
        }
        if correct {
            sendToAll(BellCorrectPacket{Type: "BellCorrect", PlayerIndex: client.playerIdx})
        } else {
            sendToAll(BellIncorrectPacket{Type: "BellIncorrect", PlayerIndex: client.playerIdx})
        }
    }
}

// func sendToAll(v interface{}) {
//     data, _ := json.Marshal(v)
//     broadcast <- data