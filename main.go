package main

import (
	"fmt"
	"log"

	"os"
	"main/auth"
	"main/db"
	"main/socket"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

func main() {
	// ✅ .env 로드
	err := godotenv.Load()
	if err != nil {
		log.Fatal(".env 파일 로드 실패:", err)
	}
	fmt.Println("✅ GOOGLE_CLIENT_ID:", os.Getenv("GOOGLE_CLIENT_ID"))
	fmt.Println("✅ GOOGLE_CLIENT_SECRET:", os.Getenv("GOOGLE_CLIENT_SECRET"))

	// ✅ 설정
	auth.SetupGoogleOAuth()
	db.Init()

	r := gin.Default()

	// ✅ Google OAuth 라우터
	r.GET("/google/auth/login", auth.GoogleLoginHandler)
	r.GET("/google/oauth2", auth.GoogleCallbackHandler)

	// ✅ WebSocket 핸들러
	handler := socket.NewHandler()
	go handler.Run()
	r.GET("/ws", func(c *gin.Context) {
		handler.HandleWebSocket(c.Writer, c.Request)
	})

	// ✅ 서버 실행
	log.Println("서버 시작: :8080 포트")
	if err := r.Run(":8080"); err != nil {
		log.Fatal("서버 실행 실패:", err)
	}
}
