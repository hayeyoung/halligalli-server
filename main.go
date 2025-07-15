package main

import (
	"log"
	"os"
	"strconv"

	"main/db"
	"main/socket"

	"github.com/gin-gonic/gin"
)

const (
	// 서버 포트 설정
	DefaultPort = ":8081"
)

func main() {
	// DB 사용 여부 설정 (환경변수 USE_DATABASE로 제어)
	useDatabase := true
	if envUseDB := os.Getenv("USE_DATABASE"); envUseDB != "" {
		if parsed, err := strconv.ParseBool(envUseDB); err == nil {
			useDatabase = parsed
		}
	}

	// socket 패키지의 UseDatabase 변수 설정
	socket.UseDatabase = useDatabase

	log.Printf("DB 사용 여부: %v", useDatabase)

	// DB 사용 시에만 DB 초기화
	if useDatabase {
		db.Init()
	} else {
		log.Printf("로컬 테스트 모드: DB 초기화 건너뛰기")
	}

	r := gin.Default()

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
