package main

import (
    "log"
    "net/http"
)

func main() {
    http.HandleFunc("/ws", handleConnection)
    go broadcaster()
    log.Println("서버 시작: :8080 포트")
    log.Fatal(http.ListenAndServe(":8080", nil))
}