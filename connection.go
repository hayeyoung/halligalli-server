package main


import (
    "encoding/json"
    "log"
    "net/http"
    "sync"

    "github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{}
var clients = make(map[*Client]bool)
var broadcast = make(chan []byte)
var mu sync.Mutex

type Client struct {
    conn *websocket.Conn
    send chan []byte
}

func handleConnection(w http.ResponseWriter, r *http.Request) {
    conn, err := upgrader.Upgrade(w, r, nil)
    if err != nil {
        log.Println("WebSocket 업그레이드 실패:", err)
        return
    }
    client := &Client{conn: conn, send: make(chan []byte)}
    mu.Lock()
    clients[client] = true
    mu.Unlock()
    defer func() {
        mu.Lock()
        delete(clients, client)
        mu.Unlock()
        conn.Close()
    }()

    go writePump(client)

    for {
        _, msg, err := conn.ReadMessage()
        if err != nil {
            log.Println("메시지 읽기 실패:", err)
            break
        }
        handlePacket(client, msg)
    }
}

func writePump(client *Client) {
    for msg := range client.send {
        client.conn.WriteMessage(websocket.TextMessage, msg)
    }
}

func broadcaster() {
    for {
        msg := <-broadcast
        mu.Lock()
        for c := range clients {
            select {
            case c.send <- msg:
            default:
                close(c.send)
                delete(clients, c)
            }
        }
        mu.Unlock()
    }
}