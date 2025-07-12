package socket

import (
	"encoding/json"
	"log"
	"net/http"
	"sort"
	"sync"
	"time"

	"math/rand"

	"main/config"

	"github.com/gorilla/websocket"
)

// WebSocket 업그레이더 설정
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // CORS 허용 (개발용)
	},
}

// rand 시드 초기화
func init() {
	rand.Seed(time.Now().UnixNano())
}

// 방 정보 구조체
type Room struct {
	mu            sync.RWMutex
	players       map[string]*Player
	maxPlayers    int
	isGameStarted bool
	playerCards   []int           // 각 플레이어별 카드 개수 (인덱스 기반)
	readyPlayers  map[string]bool // 준비 완료한 플레이어들
	// 플레이어 인덱스 매핑 (게임 시작 시 설정)
	playerIndexes map[string]int // 플레이어 ID -> 인덱스 매핑
	// 카드 공개 관련 상태
	isCardGameStarted  bool        // 카드 게임이 시작되었는지
	currentPlayerIndex int         // 현재 카드를 낼 플레이어 인덱스
	cardTimer          *time.Timer // 카드 공개 타이머
	// 각 플레이어의 공개된 카드 정보 (인덱스 기반)
	publicFruitIndexes []int // 각 플레이어의 공개된 카드 과일 인덱스
	publicFruitCounts  []int // 각 플레이어의 공개된 카드 과일 개수
	// 벨 누르기 관련 상태
	bellRung bool // 벨이 눌렸는지 여부 (새로운 카드 공개 전까지 유지)
	// 게임 제한시간 관련 상태
	gameTimer *time.Timer // 게임 제한시간 타이머
}

// 플레이어 정보 구조체
type Player struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

// 전역 방 인스턴스
var GlobalRoom = &Room{
	players:    make(map[string]*Player),
	maxPlayers: config.MaxPlayers, // 설정에서 가져온 최대 플레이어 수
}

// 클라이언트 구조체 (소켓 연결 정보)
type Client struct {
	ID       string          `json:"id"`
	Conn     *websocket.Conn `json:"-"`
	Send     chan []byte     `json:"-"`
	LastPing time.Time       `json:"-"`
	mu       sync.Mutex      `json:"-"`
	// 방 참여 상태
	IsInRoom bool   `json:"isInRoom"`
	Username string `json:"username"`
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
	case RequestEnterRoom:
		h.handleEnterRoom(client)
	case RequestLeaveRoom:
		h.handleLeaveRoom(client)
	case RequestReadyGame:
		h.handleReadyGame(client)
	case RequestRingBell:
		h.handleRingBell(client)
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

// 방 입장 처리
func (h *Handler) handleEnterRoom(client *Client) {
	// 이미 방에 참여한 상태인지 확인
	if client.IsInRoom {
		h.sendErrorWithSignal(client, RequestEnterRoom, "이미 방에 참여한 상태입니다")
		return
	}

	// 방 상태 확인
	GlobalRoom.mu.RLock()
	playerCount := len(GlobalRoom.players)
	isGameStarted := GlobalRoom.isGameStarted
	GlobalRoom.mu.RUnlock()

	// 방이 꽉 찼는지 확인
	if playerCount >= GlobalRoom.maxPlayers {
		h.sendErrorWithSignal(client, RequestEnterRoom, "방이 꽉 찼습니다")
		return
	}

	// 게임이 이미 시작된 상태인지 확인
	if isGameStarted {
		h.sendErrorWithSignal(client, RequestEnterRoom, "게임이 이미 시작된 상태입니다")
		return
	}

	// 플레이어를 방에 추가
	player := &Player{
		ID:       client.ID,
		Username: "Player" + client.ID[len(client.ID)-4:], // ID의 마지막 4자리를 사용자명으로
	}

	GlobalRoom.mu.Lock()
	GlobalRoom.players[client.ID] = player
	GlobalRoom.mu.Unlock()

	// 클라이언트 상태 업데이트
	client.mu.Lock()
	client.IsInRoom = true
	client.Username = player.Username
	client.mu.Unlock()

	// 방 입장 성공 응답
	response := NewSuccessResponse(ResponseEnterRoom, map[string]interface{}{})
	h.sendToClient(client, response)

	log.Printf("플레이어 방 입장: %s (%s)", client.ID, player.Username)

	// 현재 방 상태 로그 출력
	GlobalRoom.mu.RLock()
	currentPlayerCount := len(GlobalRoom.players)
	GlobalRoom.mu.RUnlock()
	log.Printf("현재 방 인원: %d/%d", currentPlayerCount, GlobalRoom.maxPlayers)

	// 게임 시작 조건 확인
	h.checkAndStartGame()
}

// 게임 시작 조건 확인 및 게임 시작
func (h *Handler) checkAndStartGame() {
	GlobalRoom.mu.Lock()
	defer GlobalRoom.mu.Unlock()

	// 게임이 이미 시작된 상태인지 확인
	if GlobalRoom.isGameStarted {
		return
	}

	// 방에 최대 인원이 들어왔는지 확인
	if len(GlobalRoom.players) == GlobalRoom.maxPlayers {
		// 게임 시작 상태로 변경
		GlobalRoom.isGameStarted = true

		// 준비 완료 상태 초기화
		GlobalRoom.readyPlayers = make(map[string]bool)

		// 플레이어 정보를 일관된 순서로 수집
		playerNames := make([]string, 0, len(GlobalRoom.players))
		playerIDs := make([]string, 0, len(GlobalRoom.players))

		// 플레이어 ID를 정렬하여 일관된 순서 보장
		sortedPlayerIDs := make([]string, 0, len(GlobalRoom.players))
		for playerID := range GlobalRoom.players {
			sortedPlayerIDs = append(sortedPlayerIDs, playerID)
		}

		// 플레이어 ID를 정렬 (일관된 순서 보장)
		sort.Strings(sortedPlayerIDs)

		for _, playerID := range sortedPlayerIDs {
			player := GlobalRoom.players[playerID]
			playerNames = append(playerNames, player.Username)
			playerIDs = append(playerIDs, player.ID)
		}

		// 각 플레이어에게 카드 분배 (인덱스 기반)
		startingCards := config.StartingCards // 설정에서 가져온 시작 카드 수
		GlobalRoom.playerCards = make([]int, len(GlobalRoom.players))
		for i := range GlobalRoom.playerCards {
			GlobalRoom.playerCards[i] = startingCards
		}

		// 공개된 카드 배열 초기화
		GlobalRoom.publicFruitIndexes = make([]int, len(GlobalRoom.players))
		GlobalRoom.publicFruitCounts = make([]int, len(GlobalRoom.players))
		// 초기값은 -1로 설정 (아직 카드가 공개되지 않음)
		for i := range GlobalRoom.publicFruitIndexes {
			GlobalRoom.publicFruitIndexes[i] = -1
			GlobalRoom.publicFruitCounts[i] = -1
		}

		// 플레이어 인덱스 매핑 초기화 및 설정
		GlobalRoom.playerIndexes = make(map[string]int)
		for i, playerID := range playerIDs {
			GlobalRoom.playerIndexes[playerID] = i
		}

		// 벨 누르기 상태 초기화
		GlobalRoom.bellRung = false

		log.Printf("게임 시작! 플레이어 수: %d, 플레이어들: %v, 각자 카드 %d장", len(GlobalRoom.players), playerNames, startingCards)
		log.Printf("플레이어 인덱스 매핑: %v", GlobalRoom.playerIndexes)

		// 각 클라이언트에게 게임 시작 패킷 전송
		h.mu.RLock()
		for client := range h.clients {
			if client.IsInRoom {
				// 클라이언트의 인덱스 찾기
				myIndex := -1
				for i, playerID := range playerIDs {
					if playerID == client.ID {
						myIndex = i
						break
					}
				}

				if myIndex != -1 {
					gameStartData := &GameStartData{
						PlayerCount:   len(GlobalRoom.players),
						PlayerNames:   playerNames,
						MyIndex:       myIndex,
						StartingCards: config.StartingCards, // 설정에서 가져온 시작 카드 수
					}

					response := NewSuccessResponse(ResponseStartGame, gameStartData)
					h.sendToClient(client, response)

					log.Printf("클라이언트 %s (%s)에게 게임 시작 패킷 전송 - 인덱스: %d", client.ID, client.Username, myIndex)
				}
			}
		}
		h.mu.RUnlock()
	}
}

// 방 나가기 처리
func (h *Handler) handleLeaveRoom(client *Client) {
	// 방에 참여하지 않은 상태인지 확인
	if !client.IsInRoom {
		h.sendErrorWithSignal(client, RequestLeaveRoom, "방에 참여하지 않은 상태입니다")
		return
	}

	// 게임이 시작된 상태인지 확인
	GlobalRoom.mu.RLock()
	isGameStarted := GlobalRoom.isGameStarted
	GlobalRoom.mu.RUnlock()

	// 게임이 이미 시작된 상태인지 확인
	if isGameStarted {
		h.sendErrorWithSignal(client, RequestLeaveRoom, "게임이 이미 시작된 상태입니다")
		return
	}

	// 플레이어를 방에서 제거
	GlobalRoom.mu.Lock()
	delete(GlobalRoom.players, client.ID)
	GlobalRoom.mu.Unlock()

	// 클라이언트 상태 업데이트
	client.mu.Lock()
	client.IsInRoom = false
	client.Username = ""
	client.mu.Unlock()

	// 방 나가기 성공 응답
	response := NewSuccessResponse(ResponseLeaveRoom, map[string]interface{}{})
	h.sendToClient(client, response)

	log.Printf("플레이어 방 퇴장: %s", client.ID)

	// 게임이 시작된 상태였다면 게임 상태 리셋
	if isGameStarted {
		GlobalRoom.mu.Lock()
		GlobalRoom.isGameStarted = false
		GlobalRoom.playerCards = nil         // 카드 배열 초기화
		GlobalRoom.readyPlayers = nil        // 준비 완료 상태 초기화
		GlobalRoom.isCardGameStarted = false // 카드 게임 상태 초기화
		GlobalRoom.publicFruitIndexes = nil  // 공개된 카드 배열 초기화
		GlobalRoom.publicFruitCounts = nil   // 공개된 카드 배열 초기화
		GlobalRoom.bellRung = false          // 벨 누르기 상태 초기화
		GlobalRoom.playerIndexes = nil       // 플레이어 인덱스 매핑 초기화
		if GlobalRoom.cardTimer != nil {
			GlobalRoom.cardTimer.Stop() // 카드 타이머 정지
			GlobalRoom.cardTimer = nil
		}
		GlobalRoom.mu.Unlock()
		log.Printf("플레이어 퇴장으로 인한 게임 상태 리셋")
	}
}

// 준비 완료 처리
func (h *Handler) handleReadyGame(client *Client) {
	// 방에 참여하지 않은 상태인지 확인
	if !client.IsInRoom {
		h.sendErrorWithSignal(client, RequestReadyGame, "방에 참여하지 않은 상태입니다")
		return
	}

	// 게임이 시작되지 않은 상태인지 확인
	GlobalRoom.mu.RLock()
	isGameStarted := GlobalRoom.isGameStarted
	GlobalRoom.mu.RUnlock()

	if !isGameStarted {
		h.sendErrorWithSignal(client, RequestReadyGame, "게임이 시작되지 않은 상태입니다")
		return
	}

	// 플레이어를 준비 완료 상태로 설정
	GlobalRoom.mu.Lock()
	GlobalRoom.readyPlayers[client.ID] = true
	readyCount := len(GlobalRoom.readyPlayers)
	totalPlayers := len(GlobalRoom.players)
	GlobalRoom.mu.Unlock()

	log.Printf("플레이어 준비 완료: %s (%s) - 준비: %d/%d", client.ID, client.Username, readyCount, totalPlayers)

	// 모든 플레이어가 준비 완료했는지 확인
	if readyCount == totalPlayers {
		log.Printf("모든 플레이어 준비 완료! 게임 시작!")

		// 카드 게임 시작
		GlobalRoom.mu.Lock()
		GlobalRoom.isCardGameStarted = true
		GlobalRoom.currentPlayerIndex = 0 // 첫 번째 플레이어부터 시작
		GlobalRoom.mu.Unlock()

		// 카드 공개 타이머 시작
		h.startCardTimer()

		// 모든 클라이언트에게 게임 시작 패킷 전송
		h.mu.RLock()
		for c := range h.clients {
			if c.IsInRoom {
				response := NewSuccessResponse(ResponseReadyGame, map[string]interface{}{})
				h.sendToClient(c, response)
				log.Printf("클라이언트 %s (%s)에게 게임 시작 패킷 전송", c.ID, c.Username)
			}
		}
		h.mu.RUnlock()
	}
}

// 플레이어 인덱스로 카드 개수 조회
func (r *Room) GetPlayerCardCount(playerIndex int) int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if playerIndex < 0 || playerIndex >= len(r.playerCards) {
		return 0
	}
	return r.playerCards[playerIndex]
}

// 플레이어 인덱스로 카드 개수 설정
func (r *Room) SetPlayerCardCount(playerIndex int, cardCount int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if playerIndex >= 0 && playerIndex < len(r.playerCards) {
		r.playerCards[playerIndex] = cardCount
	}
}

// 플레이어 인덱스로 공개된 카드 과일 인덱스 조회
func (r *Room) GetPublicFruitIndex(playerIndex int) int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if playerIndex < 0 || playerIndex >= len(r.publicFruitIndexes) {
		return -1
	}
	return r.publicFruitIndexes[playerIndex]
}

// 플레이어 인덱스로 공개된 카드 과일 개수 조회
func (r *Room) GetPublicFruitCount(playerIndex int) int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if playerIndex < 0 || playerIndex >= len(r.publicFruitCounts) {
		return -1
	}
	return r.publicFruitCounts[playerIndex]
}

// 모든 플레이어의 공개된 카드 정보 조회
func (r *Room) GetAllPublicCards() ([]int, []int) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	fruitIndexes := make([]int, len(r.publicFruitIndexes))
	fruitCounts := make([]int, len(r.publicFruitCounts))
	copy(fruitIndexes, r.publicFruitIndexes)
	copy(fruitCounts, r.publicFruitCounts)

	return fruitIndexes, fruitCounts
}

// 같은 종류의 과일이 정확히 5개가 공개되어 있는지 확인
func (r *Room) IsBellRingingTime() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// 각 과일 종류별로 개수를 세기
	fruitCounts := make(map[int]int)

	for i, fruitIndex := range r.publicFruitIndexes {
		// 카드가 공개되지 않은 경우 (-1) 무시
		if fruitIndex == -1 {
			continue
		}

		// 해당 과일의 개수에 현재 카드의 과일 개수를 더함
		fruitCounts[fruitIndex] += r.publicFruitCounts[i]
	}

	// 어떤 과일이라도 정확히 설정된 개수가 있으면 true 반환
	for _, count := range fruitCounts {
		if count == config.BellRingingFruitCount {
			return true
		}
	}

	return false
}

// 특정 과일 종류가 정확히 5개가 공개되어 있는지 확인
func (r *Room) IsSpecificFruitBellRingingTime(fruitIndex int) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	totalCount := 0

	for i, publicFruitIndex := range r.publicFruitIndexes {
		// 카드가 공개되지 않은 경우 (-1) 무시
		if publicFruitIndex == -1 {
			continue
		}

		// 지정된 과일 종류와 일치하는 경우 개수에 추가
		if publicFruitIndex == fruitIndex {
			totalCount += r.publicFruitCounts[i]
		}
	}

	return totalCount == config.BellRingingFruitCount
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
	log.Printf("에러 발생: %s", message)
	errorResponse := NewErrorResponse(0, message)
	h.sendToClient(client, errorResponse)
}

// 에러 메시지 전송 (특정 signal 사용)
func (h *Handler) sendErrorWithSignal(client *Client, signal int, message string) {
	log.Printf("에러 발생 (signal: %d): %s", signal, message)
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

			// 방에 참여한 상태라면 처리
			if client.IsInRoom {
				GlobalRoom.mu.RLock()
				isGameStarted := GlobalRoom.isGameStarted
				GlobalRoom.mu.RUnlock()

				if !isGameStarted {
					// 게임이 시작되지 않은 상태: LeaveRoom과 동일하게 처리
					log.Printf("게임 시작 전 플레이어 연결 해제: %s (%s)", client.ID, client.Username)

					// 플레이어를 방에서 제거
					GlobalRoom.mu.Lock()
					delete(GlobalRoom.players, client.ID)
					GlobalRoom.mu.Unlock()

					// 클라이언트 상태 업데이트
					client.mu.Lock()
					client.IsInRoom = false
					client.Username = ""
					client.mu.Unlock()

					log.Printf("플레이어 방에서 제거: %s", client.ID)
				} else {
					// 게임이 시작된 상태: 단순히 브로드캐스트에서 제외
					log.Printf("게임 진행 중 플레이어 연결 해제: %s (%s) - 브로드캐스트에서 제외", client.ID, client.Username)

					// 클라이언트 상태만 업데이트 (방에서는 제거하지 않음)
					client.mu.Lock()
					client.IsInRoom = false
					client.Username = ""
					client.mu.Unlock()
				}

				// 모든 플레이어가 연결을 끊었는지 확인
				h.checkAllPlayersDisconnected()
			}

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

// 모든 플레이어가 연결을 끊었는지 확인하고 게임 종료
func (h *Handler) checkAllPlayersDisconnected() {
	GlobalRoom.mu.RLock()
	isGameStarted := GlobalRoom.isGameStarted
	GlobalRoom.mu.RUnlock()

	// 게임이 시작되지 않았으면 무시
	if !isGameStarted {
		return
	}

	// 연결된 플레이어 수 확인
	h.mu.RLock()
	connectedPlayers := 0
	for client := range h.clients {
		if client.IsInRoom {
			connectedPlayers++
		}
	}
	h.mu.RUnlock()

	// 모든 플레이어가 연결을 끊었으면 게임 종료
	if connectedPlayers == 0 {
		log.Printf("모든 플레이어가 연결을 끊어서 게임 종료")

		GlobalRoom.mu.Lock()
		// 게임 상태 초기화
		GlobalRoom.isGameStarted = false
		GlobalRoom.isCardGameStarted = false
		GlobalRoom.playerCards = nil
		GlobalRoom.readyPlayers = nil
		GlobalRoom.publicFruitIndexes = nil           // 공개된 카드 배열 초기화
		GlobalRoom.publicFruitCounts = nil            // 공개된 카드 배열 초기화
		GlobalRoom.bellRung = false                   // 벨 누르기 상태 초기화
		GlobalRoom.playerIndexes = nil                // 플레이어 인덱스 매핑 초기화
		GlobalRoom.players = make(map[string]*Player) // 방 비우기

		// 카드 타이머 정지
		if GlobalRoom.cardTimer != nil {
			GlobalRoom.cardTimer.Stop()
			GlobalRoom.cardTimer = nil
		}
		GlobalRoom.mu.Unlock()

		log.Printf("게임 상태 초기화 완료")
	}
}

// 카드 공개 타이머 시작
func (h *Handler) startCardTimer() {
	// 기존 타이머가 있다면 정지
	if GlobalRoom.cardTimer != nil {
		GlobalRoom.cardTimer.Stop()
	}

	// 설정된 간격마다 카드 공개
	GlobalRoom.cardTimer = time.AfterFunc(time.Duration(config.CardOpenInterval)*time.Second, func() {
		h.openCard()
	})
}

// 카드 공개
func (h *Handler) openCard() {
	GlobalRoom.mu.Lock()
	defer GlobalRoom.mu.Unlock()

	// 카드 게임이 시작되지 않았으면 무시
	if !GlobalRoom.isCardGameStarted {
		return
	}

	// 플레이어가 없으면 무시
	totalPlayers := len(GlobalRoom.players)
	if totalPlayers == 0 {
		log.Printf("플레이어가 없어서 카드 공개 중단")
		return
	}

	// 랜덤 과일 인덱스 (0-2)
	fruitIndex := rand.Intn(3)

	// 랜덤 과일 개수 (1-5)
	fruitCount := rand.Intn(5) + 1

	// 현재 플레이어 인덱스
	playerIndex := GlobalRoom.currentPlayerIndex

	// 카드를 가진 플레이어를 찾을 때까지 순환
	originalPlayerIndex := playerIndex
	for GlobalRoom.playerCards[playerIndex] <= 0 {
		// 다음 플레이어로 순환
		GlobalRoom.currentPlayerIndex = (GlobalRoom.currentPlayerIndex + 1) % totalPlayers
		playerIndex = GlobalRoom.currentPlayerIndex

		// 한 바퀴 돌았는데도 카드를 가진 플레이어가 없으면 게임 종료
		if playerIndex == originalPlayerIndex {
			log.Printf("모든 플레이어가 카드를 가지고 있지 않아서 게임 종료")
			// 게임 종료 로직 추가 가능
			return
		}
	}

	// 플레이어 손패에서 카드 1장 제거
	GlobalRoom.playerCards[playerIndex]--

	// 해당 플레이어의 공개된 카드 정보 업데이트
	GlobalRoom.publicFruitIndexes[playerIndex] = fruitIndex
	GlobalRoom.publicFruitCounts[playerIndex] = fruitCount

	// 벨 누르기 상태 리셋 (새로운 카드가 공개됨)
	GlobalRoom.bellRung = false

	// 카드 공개 데이터 생성
	openCardData := &OpenCardData{
		FruitIndex:  fruitIndex,
		FruitCount:  fruitCount,
		PlayerIndex: playerIndex,
	}

	// 모든 클라이언트에게 카드 공개 패킷 전송
	h.mu.RLock()
	for client := range h.clients {
		if client.IsInRoom {
			response := NewSuccessResponse(ResponseOpenCard, openCardData)
			h.sendToClient(client, response)
		}
	}
	h.mu.RUnlock()

	log.Printf("카드 공개: 과일%d, 개수%d, 플레이어%d", fruitIndex, fruitCount, playerIndex)

	// 다음 카드 공개 타이머 설정
	GlobalRoom.cardTimer = time.AfterFunc(time.Duration(config.CardOpenInterval)*time.Second, func() {
		h.openCard()
	})
}

// 벨 누르기 처리
func (h *Handler) handleRingBell(client *Client) {
	// 방에 참여하지 않은 상태인지 확인
	if !client.IsInRoom {
		h.sendErrorWithSignal(client, RequestRingBell, "방에 참여하지 않은 상태입니다")
		return
	}

	// 게임이 시작되지 않은 상태인지 확인
	GlobalRoom.mu.RLock()
	isGameStarted := GlobalRoom.isGameStarted
	GlobalRoom.mu.RUnlock()

	if !isGameStarted {
		h.sendErrorWithSignal(client, RequestRingBell, "게임이 시작되지 않은 상태입니다")
		return
	}

	// 이미 벨이 눌렸는지 확인
	GlobalRoom.mu.Lock()
	if GlobalRoom.bellRung {
		GlobalRoom.mu.Unlock()
		log.Printf("플레이어 벨 누름 무시: %s (%s) - 이미 벨이 눌린 상태", client.ID, client.Username)
		return
	}

	// 벨 누르기 상태 설정
	GlobalRoom.bellRung = true
	GlobalRoom.mu.Unlock()

	// 종을 칠 수 있는 타이밍인지 확인
	isBellRingingTime := GlobalRoom.IsBellRingingTime()

	// 벨을 누른 플레이어의 인덱스 찾기 (게임 시작 시 설정된 인덱스 사용)
	GlobalRoom.mu.RLock()
	playerIndex, exists := GlobalRoom.playerIndexes[client.ID]
	GlobalRoom.mu.RUnlock()

	if !exists {
		log.Printf("플레이어 인덱스를 찾을 수 없음: %s (%s)", client.ID, client.Username)
		h.sendErrorWithSignal(client, RequestRingBell, "플레이어 인덱스를 찾을 수 없습니다")
		return
	}

	log.Printf("플레이어 벨 누름: %s (%s) - 종을 칠 수 있는 타이밍: %v, 플레이어 인덱스: %d", client.ID, client.Username, isBellRingingTime, playerIndex)

	// OpenCard 타이머 초기화
	h.resetCardTimer()

	// 벨 누르기 결과 처리
	if isBellRingingTime {
		// 벨을 올바르게 누른 경우, 공개된 모든 카드를 해당 플레이어의 손패에 추가
		GlobalRoom.AddAllPublicCardsToPlayer(playerIndex)
		log.Printf("벨 누르기 성공! 플레이어 %d의 손패에 공개된 모든 카드 추가", playerIndex)

		// 업데이트된 카드 개수 배열 가져오기
		GlobalRoom.mu.RLock()
		updatedPlayerCards := make([]int, len(GlobalRoom.playerCards))
		copy(updatedPlayerCards, GlobalRoom.playerCards)
		GlobalRoom.mu.RUnlock()

		// 성공 데이터 생성
		ringBellCorrectData := &RingBellCorrectData{
			PlayerIndex: playerIndex,
			PlayerCards: updatedPlayerCards,
		}

		// 모든 클라이언트에게 성공 결과 전송
		h.mu.RLock()
		for c := range h.clients {
			if c.IsInRoom {
				response := NewSuccessResponse(ResponseRingBellCorrect, ringBellCorrectData)
				h.sendToClient(c, response)
			}
		}
		h.mu.RUnlock()

		log.Printf("벨 누르기 성공! 플레이어 인덱스: %d", playerIndex)
	} else {
		// 벨을 잘못 누른 경우, 다른 플레이어들에게 카드 분배
		cardGivenTo := GlobalRoom.DistributeCardsFromPlayer(playerIndex)
		log.Printf("벨 누르기 실패! 플레이어 %d가 다른 플레이어들에게 카드 분배", playerIndex)

		// 업데이트된 카드 개수 배열 다시 가져오기
		GlobalRoom.mu.RLock()
		updatedPlayerCards := make([]int, len(GlobalRoom.playerCards))
		copy(updatedPlayerCards, GlobalRoom.playerCards)
		GlobalRoom.mu.RUnlock()

		// 실패 데이터 생성
		ringBellWrongData := &RingBellWrongData{
			PlayerIndex: playerIndex,
			CardGivenTo: cardGivenTo,
			PlayerCards: updatedPlayerCards,
		}

		// 모든 클라이언트에게 실패 결과 전송
		h.mu.RLock()
		for c := range h.clients {
			if c.IsInRoom {
				response := NewSuccessResponse(ResponseRingBellWrong, ringBellWrongData)
				h.sendToClient(c, response)
			}
		}
		h.mu.RUnlock()

		log.Printf("벨 누르기 실패! 플레이어 인덱스: %d", playerIndex)
	}
}

// 공개된 모든 카드를 특정 플레이어의 손패에 추가
func (r *Room) AddAllPublicCardsToPlayer(playerIndex int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	totalCards := 0
	for i := 0; i < len(r.publicFruitCounts); i++ {
		if r.publicFruitCounts[i] > 0 {
			totalCards += r.publicFruitCounts[i]
		}
	}

	// 현재 플레이어의 카드 개수에 추가
	if playerIndex < len(r.playerCards) {
		r.playerCards[playerIndex] += totalCards
		log.Printf("플레이어 %d의 손패에 공개된 모든 카드 %d장 추가", playerIndex, totalCards)
	}

	// 공개된 카드 정보 초기화
	for i := 0; i < len(r.publicFruitIndexes); i++ {
		r.publicFruitIndexes[i] = 0
		r.publicFruitCounts[i] = 0
	}
}

// 벨을 잘못 친 플레이어가 다른 플레이어들에게 카드를 나누어주는 함수
func (r *Room) DistributeCardsFromPlayer(playerIndex int) []bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	totalPlayers := len(r.playerCards)
	if totalPlayers == 0 || playerIndex >= totalPlayers {
		return make([]bool, totalPlayers)
	}

	// 카드를 받을 플레이어들 (벨을 친 플레이어 제외)
	receivers := make([]int, 0)
	for i := 0; i < totalPlayers; i++ {
		if i != playerIndex {
			receivers = append(receivers, i)
		}
	}

	// 벨을 친 플레이어가 가진 카드 수
	availableCards := r.playerCards[playerIndex]

	// 카드가 부족한 경우, 랜덤하게 선택된 플레이어들에게만 나누어줌
	if availableCards < len(receivers) {
		shuffleIntSlice(receivers)
		receivers = receivers[:availableCards]
	}

	// 카드 분배 실행
	cardGivenTo := make([]bool, totalPlayers)
	for _, receiverIndex := range receivers {
		if r.playerCards[playerIndex] > 0 {
			r.playerCards[playerIndex]--
			r.playerCards[receiverIndex]++
			cardGivenTo[receiverIndex] = true
			log.Printf("플레이어 %d가 플레이어 %d에게 카드 1장 전달", playerIndex, receiverIndex)
		}
	}

	return cardGivenTo
}

// int 슬라이스를 섞는 함수
func shuffleIntSlice(slice []int) {
	for i := len(slice) - 1; i > 0; i-- {
		j := rand.Intn(i + 1)
		slice[i], slice[j] = slice[j], slice[i]
	}
}

// 순위 계산 함수
func calculatePlayerRanks(playerCards []int) []int {
	// 플레이어 인덱스와 카드 개수를 함께 저장
	type PlayerCardInfo struct {
		playerIndex int
		cardCount   int
	}

	// 플레이어 정보 배열 생성
	playerInfos := make([]PlayerCardInfo, len(playerCards))
	for i, cardCount := range playerCards {
		playerInfos[i] = PlayerCardInfo{
			playerIndex: i,
			cardCount:   cardCount,
		}
	}

	// 카드 개수 기준으로 내림차순 정렬 (카드가 많을수록 높은 순위)
	sort.Slice(playerInfos, func(i, j int) bool {
		return playerInfos[i].cardCount > playerInfos[j].cardCount
	})

	// 순위 배열 생성 (1등부터 시작)
	ranks := make([]int, len(playerCards))
	for i := range ranks {
		ranks[i] = i + 1 // 기본값으로 인덱스+1 설정
	}

	// 실제 순위로 업데이트 (공동 순위 처리)
	currentRank := 1
	currentCardCount := -1

	for i, playerInfo := range playerInfos {
		// 카드 개수가 바뀌면 순위 증가
		if playerInfo.cardCount != currentCardCount {
			currentRank = i + 1
			currentCardCount = playerInfo.cardCount
		}

		// 현재 순위를 해당 플레이어에게 할당
		ranks[playerInfo.playerIndex] = currentRank
	}

	return ranks
}

// 게임 종료 처리
func (h *Handler) endGame() {
	GlobalRoom.mu.Lock()
	defer GlobalRoom.mu.Unlock()

	log.Printf("게임 제한시간 종료 - 게임 종료")

	// 현재 플레이어 카드 개수와 순위 계산
	playerCards := make([]int, len(GlobalRoom.playerCards))
	copy(playerCards, GlobalRoom.playerCards)
	playerRanks := calculatePlayerRanks(playerCards)

	// 게임 종료 데이터 생성
	endGameData := &EndGameData{
		PlayerCards: playerCards,
		PlayerRanks: playerRanks,
	}

	// 모든 클라이언트에게 게임 종료 패킷 전송
	h.mu.RLock()
	for c := range h.clients {
		if c.IsInRoom {
			response := NewSuccessResponse(ResponseEndGame, endGameData)
			h.sendToClient(c, response)
		}
	}
	h.mu.RUnlock()

	// 게임 상태 초기화
	GlobalRoom.isGameStarted = false
	GlobalRoom.isCardGameStarted = false
	GlobalRoom.playerCards = nil
	GlobalRoom.readyPlayers = nil
	GlobalRoom.publicFruitIndexes = nil
	GlobalRoom.publicFruitCounts = nil
	GlobalRoom.bellRung = false
	GlobalRoom.playerIndexes = nil
	GlobalRoom.players = make(map[string]*Player)

	// 타이머들 정지
	if GlobalRoom.cardTimer != nil {
		GlobalRoom.cardTimer.Stop()
		GlobalRoom.cardTimer = nil
	}
	if GlobalRoom.gameTimer != nil {
		GlobalRoom.gameTimer.Stop()
		GlobalRoom.gameTimer = nil
	}

	log.Printf("게임 종료 완료 - 순위: %v", playerRanks)
}

// 게임 타이머 시작
func (h *Handler) startGameTimer() {
	// 기존 게임 타이머가 있다면 정지
	if GlobalRoom.gameTimer != nil {
		GlobalRoom.gameTimer.Stop()
	}

	// 설정된 제한시간 후 게임 종료 (120초)
	GlobalRoom.gameTimer = time.AfterFunc(120*time.Second, func() {
		h.endGame()
	})

	log.Printf("게임 타이머 시작 - 120초 후 종료")
}

// OpenCard 타이머 초기화
func (h *Handler) resetCardTimer() {
	GlobalRoom.mu.Lock()
	defer GlobalRoom.mu.Unlock()

	// 기존 타이머가 있다면 정지
	if GlobalRoom.cardTimer != nil {
		GlobalRoom.cardTimer.Stop()
		GlobalRoom.cardTimer = nil
	}

	// 새로운 타이머 시작 (설정된 간격 후)
	GlobalRoom.cardTimer = time.AfterFunc(time.Duration(config.CardOpenInterval)*time.Second, func() {
		h.openCard()
	})

	log.Printf("OpenCard 타이머 초기화 완료")
}
