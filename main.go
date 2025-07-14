package main

import (
	"context"
	"log"
	"main/socket"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	// 로그 포맷 설정
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	// 소켓 핸들러 생성
	handler := socket.NewHandler()

	// 서버 포트 설정 (환경변수에서 가져오거나 기본값 사용)
	port := ":80"
	if envPort := os.Getenv("PORT"); envPort != "" {
		port = ":" + envPort
	}

	// HTTP 서버 생성
	server := &http.Server{
		Addr:    port,
		Handler: nil, // 기본 mux 사용
	}

	// WebSocket 엔드포인트 설정
	http.HandleFunc("/ws", handler.HandleWebSocket)

	// 헬스체크 엔드포인트 추가
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// 핸들러 실행 (고루틴으로)
	go handler.Run()

	log.Printf("=== HalliGalli 서버 시작 ===")
	log.Printf("서버 포트: %s", port)
	log.Printf("=== 접속 방법 ===")
	log.Printf("로컬 테스트: ws://localhost%s/ws", port)
	log.Printf("원격 접속: ws://서버IP%s/ws", port)
	log.Printf("헬스체크: http://localhost%s/health", port)
	log.Printf("서버 시작 시간: %s", time.Now().Format("2006-01-02 15:04:05"))

	// Graceful shutdown을 위한 채널 설정
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	// 서버 시작 (고루틴으로)
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("서버 시작 실패: %v", err)
		}
	}()

	// 종료 신호 대기
	<-stop
	log.Println("서버 종료 신호 수신...")

	// Graceful shutdown (30초 타임아웃)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Printf("서버 종료 중 오류: %v", err)
	} else {
		log.Println("서버가 정상적으로 종료되었습니다.")
	}
}
