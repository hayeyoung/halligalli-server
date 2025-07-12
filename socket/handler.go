package socket

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// WebSocket 업그레이더 설정
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // CORS 허용 (개발용)
	},
}

// 클라이언트 구조체 (소켓 연결 정보)
type Client struct {
	ID       string          `json:"id"`
	Conn     *websocket.Conn `json:"-"`
	Send     chan []byte     `json:"-"`
	LastPing time.Time       `json:"-"`
	mu       sync.Mutex      `json:"-"`
}

// 핸들러 구조체
type Handler struct {
	clients    map[*Client]bool
	broadcast  chan []byte
	register   chan *Client
	unregister chan *Client
	mu         sync.RWMutex
}

// 새로운 핸들러 생성
func NewHandler() *Handler {
	return &Handler{
		clients:    make(map[*Client]bool),
		broadcast:  make(chan []byte),
		register:   make(chan *Client),
		unregister: make(chan *Client),
	}
}

// WebSocket 연결 핸들러
func (h *Handler) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket 업그레이드 실패: %v", err)
		return
	}

	client := &Client{
		ID:       generateClientID(),
		Conn:     conn,
		Send:     make(chan []byte, 256),
		LastPing: time.Now(),
	}

	// 클라이언트 등록
	h.register <- client

	// 연결 성공 메시지 전송
	response := NewSuccessResponse(ResponsePong, map[string]interface{}{
		"clientId": client.ID,
		"message":  "연결이 성공적으로 설정되었습니다.",
	})
	h.sendToClient(client, response)

	// 클라이언트 메시지 처리 고루틴 시작
	go h.readPump(client)
	go h.writePump(client)
}

// 클라이언트로부터 메시지 읽기
func (h *Handler) readPump(client *Client) {
	defer func() {
		h.unregister <- client
		client.Conn.Close()
	}()

	client.Conn.SetReadLimit(512) // 메시지 크기 제한
	client.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	client.Conn.SetPongHandler(func(string) error {
		client.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, message, err := client.Conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WebSocket 읽기 오류: %v", err)
			}
			break
		}

		// 메시지 처리
		h.handleMessage(client, message)
	}
}

// 클라이언트에게 메시지 쓰기
func (h *Handler) writePump(client *Client) {
	ticker := time.NewTicker(54 * time.Second)
	defer func() {
		ticker.Stop()
		client.Conn.Close()
	}()

	for {
		select {
		case message, ok := <-client.Send:
			client.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				client.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := client.Conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(message)

			if err := w.Close(); err != nil {
				return
			}
		case <-ticker.C:
			client.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := client.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// 메시지 처리
func (h *Handler) handleMessage(client *Client, message []byte) {
	// 클라이언트 요청 패킷 검증
	request, err := ValidateRequestPacket(message)
	if err != nil {
		log.Printf("잘못된 패킷 형식: %v", err)
		// 원본 메시지에서 signal 추출 시도
		var rawRequest map[string]interface{}
		if json.Unmarshal(message, &rawRequest) == nil {
			if signal, ok := rawRequest["signal"].(float64); ok {
				h.sendErrorWithSignal(client, int(signal), "잘못된 패킷 형식입니다")
				return
			}
		}
		h.sendErrorWithSignal(client, 0, "잘못된 패킷 형식입니다")
		return
	}

	// signal에 따른 요청 처리
	switch request.Signal {
	case RequestPing:
		h.handlePing(client)
	default:
		log.Printf("알 수 없는 요청 signal: %d", request.Signal)
		h.sendErrorWithSignal(client, request.Signal, "알 수 없는 요청입니다")
	}
}

// 핑 처리
func (h *Handler) handlePing(client *Client) {
	response := NewSuccessResponse(ResponsePong, map[string]interface{}{
		"timestamp": time.Now().Unix(),
	})
	h.sendToClient(client, response)
}

// 클라이언트에게 메시지 전송
func (h *Handler) sendToClient(client *Client, message interface{}) {
	var data []byte
	var err error

	// Packet 타입인 경우 ToJSONWithLog 사용
	if packet, ok := message.(*ResponsePacket); ok {
		data, err = packet.ToJSONWithLog()
	} else {
		// 기존 호환성을 위한 fallback
		data, err = json.Marshal(message)
		if err != nil {
			log.Printf("메시지 마샬링 오류: %v", err)
			return
		}
	}

	if err != nil {
		return
	}

	select {
	case client.Send <- data:
	default:
		close(client.Send)
		delete(h.clients, client)
	}
}

// 모든 클라이언트에게 브로드캐스트
func (h *Handler) broadcastToAll(message interface{}) {
	var data []byte
	var err error

	// Packet 타입인 경우 ToJSONWithLog 사용
	if packet, ok := message.(*ResponsePacket); ok {
		data, err = packet.ToJSONWithLog()
	} else {
		// 기존 호환성을 위한 fallback
		data, err = json.Marshal(message)
		if err != nil {
			log.Printf("메시지 마샬링 오류: %v", err)
			return
		}
	}

	if err != nil {
		return
	}

	h.mu.RLock()
	for client := range h.clients {
		select {
		case client.Send <- data:
		default:
			close(client.Send)
			delete(h.clients, client)
		}
	}
	h.mu.RUnlock()
}

// 특정 클라이언트를 제외한 모든 클라이언트에게 브로드캐스트
func (h *Handler) broadcastToOthers(excludeClient *Client, message interface{}) {
	var data []byte
	var err error

	// Packet 타입인 경우 ToJSONWithLog 사용
	if packet, ok := message.(*ResponsePacket); ok {
		data, err = packet.ToJSONWithLog()
	} else {
		// 기존 호환성을 위한 fallback
		data, err = json.Marshal(message)
		if err != nil {
			log.Printf("메시지 마샬링 오류: %v", err)
			return
		}
	}

	if err != nil {
		return
	}

	h.mu.RLock()
	for client := range h.clients {
		if client != excludeClient {
			select {
			case client.Send <- data:
			default:
				close(client.Send)
				delete(h.clients, client)
			}
		}
	}
	h.mu.RUnlock()
}

// 에러 메시지 전송 (기본 signal 0 사용)
func (h *Handler) sendError(client *Client, message string) {
	errorResponse := NewErrorResponse(0, message)
	h.sendToClient(client, errorResponse)
}

// 에러 메시지 전송 (특정 signal 사용)
func (h *Handler) sendErrorWithSignal(client *Client, signal int, message string) {
	errorResponse := NewErrorResponse(signal, message)
	h.sendToClient(client, errorResponse)
}

// 클라이언트 ID 생성
func generateClientID() string {
	return time.Now().Format("20060102150405") + "-" + randomString(6)
}

// 랜덤 문자열 생성
func randomString(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[time.Now().UnixNano()%int64(len(charset))]
	}
	return string(b)
}

// 핸들러 실행
func (h *Handler) Run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()
			log.Printf("클라이언트 연결: %s", client.ID)

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.Send)
				log.Printf("클라이언트 연결 해제: %s", client.ID)
			}
			h.mu.Unlock()

		case message := <-h.broadcast:
			h.mu.RLock()
			for client := range h.clients {
				select {
				case client.Send <- message:
				default:
					close(client.Send)
					delete(h.clients, client)
				}
			}
			h.mu.RUnlock()
		}
	}
}
