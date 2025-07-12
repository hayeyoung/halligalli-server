package main

import (
	"log"
	"main/socket"
	"net/http"
)

func main() {
	// 소켓 핸들러 생성
	handler := socket.NewHandler()

	// WebSocket 엔드포인트 설정
	http.HandleFunc("/ws", handler.HandleWebSocket)

	// 핸들러 실행 (고루틴으로)
	go handler.Run()

	log.Println("서버 시작: :8080 포트")
	log.Println("WebSocket 엔드포인트: ws://localhost:8080/ws")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
