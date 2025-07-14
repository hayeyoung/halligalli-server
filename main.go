package main

import (
	"log"

	// "main/auth"
	// "main/db"

	"main/db"
	"main/socket"

	"github.com/gin-gonic/gin"
)

const (
	// 서버 포트 설정
	DefaultPort = ":8081"
)

func main() {

	db.Init()

	r := gin.Default()

	// ✅ Google OAuth 라우터 (비활성화)
	// r.GET("/google/auth/login", auth.GoogleLoginHandler)
	// r.GET("/google/oauth2", auth.GoogleCallbackHandler)

	// ✅ WebSocket 핸들러
	handler := socket.NewHandler()
	go handler.Run()
	r.GET("/ws", func(c *gin.Context) {
		handler.HandleWebSocket(c.Writer, c.Request)
	})

	// ✅ 서버 실행
	log.Printf("서버 시작: %s 포트", DefaultPort)
	if err := r.Run(DefaultPort); err != nil {
		log.Fatal("서버 실행 실패:", err)
	}
}
