package socket

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"sync"
	"time"

	"math/rand"

	"main/config"
	"main/db"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/bcrypt"
)

// DB 사용 여부를 제어하는 변수 (로컬 테스트용)
var UseDatabase = true

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
	ID      int    // 방 ID
	Name    string // 방 이름
	mu      sync.RWMutex
	players map[string]*Player
	// 방 세팅 관련 값
	maxPlayers     int // 최대 인원 수
	fruitVariation int // 과일 종류 수
	fruitBellCount int // 종을 올바르게 치기 위한 과일 수
	gameTempo      int // 게임 템포
	isGameStarted  bool
	playerCards    []int           // 각 플레이어별 카드 개수 (인덱스 기반)
	readyPlayers   map[string]bool // 준비 완료한 플레이어들
	// 플레이어 인덱스 매핑 (게임 시작 시 설정)
	playerIndexes map[string]int // 플레이어 ID -> 인덱스 매핑
	// 카드 공개 관련 상태
	isCardGameStarted  bool        // 카드 게임이 시작되었는지
	currentPlayerIndex int         // 현재 카드를 낼 플레이어 인덱스
	cardTimer          *time.Timer // 카드 공개 타이머
	// 각 플레이어의 공개된 카드 정보 (인덱스 기반)
	publicFruitIndexes []int // 각 플레이어의 공개된 카드 과일 인덱스
	publicFruitCounts  []int // 각 플레이어의 공개된 카드 과일 개수
	openCards          []int // 각 플레이어가 공개한 카드 개수
	// 벨 누르기 관련 상태
	bellRung bool // 벨이 눌렸는지 여부 (새로운 카드 공개 전까지 유지)
	// 게임 제한시간 관련 상태
	gameTimer     *time.Timer // 게임 제한시간 타이머
	isTimeExpired bool        // 시간제한이 끝났는지 여부
	// 감정표현 관련 상태
	lastEmotionTimes map[string]time.Time // 각 클라이언트별 마지막 감정표현 시간
}

// 플레이어 정보 구조체
type Player struct {
	ID       string `json:"id"`
	Username string `json:"username"`
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
	RoomID   int    `json:"roomId"` // 참여 중인 방 ID
	Username string `json:"username"`
	// 로그인 상태
	IsLoggedIn   bool   `json:"isLoggedIn"`
	UserID       string `json:"userId"`       // 로그인한 사용자 ID
	UserNickname string `json:"userNickname"` // 로그인한 사용자 닉네임
}

// 핸들러 구조체
type Handler struct {
	clients    map[*Client]bool
	rooms      map[int]*Room // 방 ID -> Room 매핑
	broadcast  chan []byte
	register   chan *Client
	unregister chan *Client
	mu         sync.RWMutex
	roomMu     sync.RWMutex // 방 관리를 위한 별도 뮤텍스
}

// 새로운 핸들러 생성
func NewHandler() *Handler {
	return &Handler{
		clients:    make(map[*Client]bool),
		rooms:      make(map[int]*Room),
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
		h.handleEnterRoom(client, request)
	case RequestLeaveRoom:
		h.handleLeaveRoom(client)
	case RequestGetRoomList:
		h.handleGetRoomList(client)
	case RequestCreateRoom:
		h.handleCreateRoom(client, request)
	case RequestReadyGame:
		h.handleReadyGame(client)
	case RequestRingBell:
		h.handleRingBell(client)
	case RequestEmotion:
		h.handleEmotion(client, request)
	case RequestCreateAccount:
		h.handleCreateAccount(client, request)
	case RequestLogin:
		h.handleLogin(client, request)
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
func (h *Handler) handleEnterRoom(client *Client, request *RequestPacket) {
	// 이미 방에 참여한 상태인지 확인
	if client.IsInRoom {
		h.sendErrorWithSignal(client, RequestEnterRoom, "이미 방에 참여한 상태입니다")
		return
	}

	// 요청 데이터 파싱
	var enterRoomData RequestEnterRoomData

	// request.Data가 map[string]interface{}인 경우를 처리
	if dataMap, ok := request.Data.(map[string]interface{}); ok {
		// RoomID 확인
		if roomId, exists := dataMap["roomId"]; exists {
			if roomIdFloat, ok := roomId.(float64); ok {
				enterRoomData.RoomID = int(roomIdFloat)
			} else {
				log.Printf("방 ID가 숫자가 아님: %v", roomId)
				h.sendErrorWithSignal(client, RequestEnterRoom, "잘못된 방 ID 형식입니다")
				return
			}
		} else {
			log.Printf("방 입장 데이터에 roomId가 없음")
			h.sendErrorWithSignal(client, RequestEnterRoom, "방 ID가 없습니다")
			return
		}
	} else {
		log.Printf("방 입장 데이터 형식 오류: %v", request.Data)
		h.sendErrorWithSignal(client, RequestEnterRoom, "잘못된 방 입장 데이터 형식입니다")
		return
	}

	// 데이터 유효성 검사
	if enterRoomData.RoomID <= 0 {
		h.sendErrorWithSignal(client, RequestEnterRoom, "방 ID는 1 이상이어야 합니다")
		return
	}

	log.Printf("방 입장 요청: 클라이언트 %s -> 방 %d", client.ID, enterRoomData.RoomID)

	// 방 ID 설정
	roomID := enterRoomData.RoomID

	// 방이 존재하지 않으면 생성
	h.roomMu.Lock()
	room, roomExists := h.rooms[roomID]
	if !roomExists {
		room = &Room{
			ID:               roomID,
			Name:             fmt.Sprintf("방 %d", roomID),
			players:          make(map[string]*Player),
			maxPlayers:       config.MaxPlayers, // 기본값: 설정에서 가져온 최대 플레이어 수
			fruitVariation:   3,                 // 기본값: 3가지 과일
			fruitBellCount:   5,                 // 기본값: 5개
			gameTempo:        0,                 // 기본값: 3초 간격 (gameTempo 0)
			lastEmotionTimes: make(map[string]time.Time),
		}
		h.rooms[roomID] = room
		log.Printf("새로운 방 생성: %d (%s) - 최대인원:%d, 과일종류:%d, 벨개수:%d, 템포:%d",
			roomID, room.Name, room.maxPlayers, room.fruitVariation, room.fruitBellCount, room.gameTempo)
	}
	h.roomMu.Unlock()

	// 같은 ID의 플레이어가 이미 방에 있는지 확인
	room.mu.RLock()
	_, playerExists := room.players[client.ID]
	room.mu.RUnlock()

	if playerExists {
		h.sendErrorWithSignal(client, RequestEnterRoom, "같은 ID의 플레이어가 이미 방에 있습니다")
		return
	}

	// 방이 꽉 찼는지 확인
	room.mu.RLock()
	if len(room.players) >= room.maxPlayers {
		room.mu.RUnlock()
		h.sendErrorWithSignal(client, RequestEnterRoom, "방이 꽉 찼습니다")
		return
	}

	// 게임이 이미 시작된 상태인지 확인
	if room.isGameStarted {
		room.mu.RUnlock()
		h.sendErrorWithSignal(client, RequestEnterRoom, "게임이 이미 시작된 상태입니다")
		return
	}
	room.mu.RUnlock()

	// 플레이어를 방에 추가
	username := "Player" + generateRandomNumber(4) // 기본값: 랜덤 숫자 4개를 사용자명으로

	// 로그인된 사용자인 경우 닉네임 사용
	if client.IsUserLoggedIn() {
		username = client.UserNickname
	}

	player := &Player{
		ID:       client.ID,
		Username: username,
	}

	room.mu.Lock()
	room.players[client.ID] = player
	room.mu.Unlock()

	// 클라이언트 상태 업데이트
	client.mu.Lock()
	client.IsInRoom = true
	client.RoomID = roomID
	client.Username = player.Username
	client.mu.Unlock()

	// 방 입장 성공 응답
	response := NewSuccessResponse(ResponseEnterRoom, map[string]interface{}{
		"roomId":         roomID,
		"roomName":       room.Name,
		"maxPlayers":     room.maxPlayers,
		"fruitVariation": room.fruitVariation,
		"fruitBellCount": room.fruitBellCount,
		"gameTempo":      room.gameTempo,
	})
	h.sendToClient(client, response)

	log.Printf("플레이어 방 입장: %s (%s) -> %s", client.ID, player.Username, roomID)

	// 현재 방 상태 로그 출력
	room.mu.RLock()
	currentPlayerCount := len(room.players)
	room.mu.RUnlock()
	log.Printf("현재 방 인원: %d/%d", currentPlayerCount, room.maxPlayers)

	// 방의 모든 클라이언트에게 플레이어 수 변경 알림
	h.notifyPlayerCountChanged(room)

	// 게임 시작 조건 확인
	h.checkAndStartGame(room)
}

// 게임 시작 조건 확인 및 게임 시작
func (h *Handler) checkAndStartGame(room *Room) {
	room.mu.Lock()
	defer room.mu.Unlock()

	// 게임이 이미 시작된 상태인지 확인
	if room.isGameStarted {
		return
	}

	// 방에 최대 인원이 들어왔는지 확인
	if len(room.players) == room.maxPlayers {
		// 게임 시작 상태로 변경
		room.isGameStarted = true

		// 준비 완료 상태 초기화
		room.readyPlayers = make(map[string]bool)

		// 플레이어 정보를 랜덤한 순서로 수집
		playerNames := make([]string, 0, len(room.players))
		playerIDs := make([]string, 0, len(room.players))

		// 플레이어 ID를 배열로 수집
		playerIDList := make([]string, 0, len(room.players))
		for playerID := range room.players {
			playerIDList = append(playerIDList, playerID)
		}

		// 플레이어 ID를 랜덤하게 섞기
		shuffleStringSlice(playerIDList)

		for _, playerID := range playerIDList {
			player := room.players[playerID]
			playerNames = append(playerNames, player.Username)
			playerIDs = append(playerIDs, player.ID)
		}

		// 각 플레이어에게 카드 분배 (인덱스 기반)
		startingCards := config.StartingCards // 설정에서 가져온 시작 카드 수
		room.playerCards = make([]int, len(room.players))
		for i := range room.playerCards {
			room.playerCards[i] = startingCards
		}

		// 공개된 카드 배열 초기화
		room.publicFruitIndexes = make([]int, len(room.players))
		room.publicFruitCounts = make([]int, len(room.players))
		room.openCards = make([]int, len(room.players))
		// 초기값은 -1로 설정 (아직 카드가 공개되지 않음)
		for i := range room.publicFruitIndexes {
			room.publicFruitIndexes[i] = -1
			room.publicFruitCounts[i] = -1
			room.openCards[i] = 0
		}

		// 플레이어 인덱스 매핑 초기화 및 설정
		room.playerIndexes = make(map[string]int)
		for i, playerID := range playerIDs {
			room.playerIndexes[playerID] = i
		}

		// 벨 누르기 상태 초기화
		room.bellRung = false

		log.Printf("게임 시작! 플레이어 수: %d, 플레이어들: %v, 각자 카드 %d장", len(room.players), playerNames, startingCards)
		log.Printf("플레이어 인덱스 매핑: %v", room.playerIndexes)

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
						PlayerCount:   len(room.players),
						PlayerNames:   playerNames,
						MyIndex:       myIndex,
						StartingCards: config.StartingCards, // 설정에서 가져온 시작 카드 수
						GameTimeLimit: config.GameTimeLimit, // 설정에서 가져온 게임 제한시간
					}

					response := NewSuccessResponse(ResponseStartGame, gameStartData)
					h.sendToClient(client, response)

					log.Printf("클라이언트 %s (%s)에게 게임 시작 패킷 전송 - 인덱스: %d, 제한시간: %d초", client.ID, client.Username, myIndex, config.GameTimeLimit)
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
	h.roomMu.RLock()
	room, roomExists := h.rooms[client.RoomID]
	h.roomMu.RUnlock()

	if !roomExists {
		h.sendErrorWithSignal(client, RequestLeaveRoom, "존재하지 않는 방입니다")
		return
	}

	if room.isGameStarted {
		h.sendErrorWithSignal(client, RequestLeaveRoom, "게임이 이미 시작된 상태입니다")
		return
	}

	// 플레이어를 방에서 제거
	room.mu.Lock()
	delete(room.players, client.ID)
	room.mu.Unlock()

	// 클라이언트 상태 업데이트
	client.mu.Lock()
	client.IsInRoom = false
	client.Username = ""
	client.IsLoggedIn = false
	client.UserID = ""
	client.UserNickname = ""
	client.mu.Unlock()

	// 방 나가기 성공 응답
	response := NewSuccessResponse(ResponseLeaveRoom, map[string]interface{}{})
	h.sendToClient(client, response)

	log.Printf("플레이어 방 퇴장: %s", client.ID)

	// 게임이 시작된 상태였다면 게임 상태 리셋
	if room.isGameStarted {
		room.mu.Lock()
		room.isGameStarted = false
		room.playerCards = nil         // 카드 배열 초기화
		room.readyPlayers = nil        // 준비 완료 상태 초기화
		room.isCardGameStarted = false // 카드 게임 상태 초기화
		room.publicFruitIndexes = nil  // 공개된 카드 배열 초기화
		room.publicFruitCounts = nil   // 공개된 카드 배열 초기화
		room.openCards = nil           // 공개된 카드 개수 배열 초기화
		room.bellRung = false          // 벨 누르기 상태 초기화
		room.isTimeExpired = false     // 시간제한 상태 초기화
		room.playerIndexes = nil       // 플레이어 인덱스 매핑 초기화
		if room.cardTimer != nil {
			room.cardTimer.Stop() // 카드 타이머 정지
			room.cardTimer = nil
		}
		room.mu.Unlock()
		log.Printf("플레이어 퇴장으로 인한 게임 상태 리셋")
	}

	// 방이 비었으면 방 삭제
	room.mu.RLock()
	if len(room.players) == 0 {
		room.mu.RUnlock()
		h.roomMu.Lock()
		delete(h.rooms, client.RoomID)
		h.roomMu.Unlock()
		log.Printf("방이 비어서 삭제: %s", client.RoomID)
	} else {
		room.mu.RUnlock()
	}

	// 방의 모든 클라이언트에게 플레이어 수 변경 알림
	h.notifyPlayerCountChanged(room)
}

// 방의 모든 클라이언트에게 플레이어 수 변경 알림
func (h *Handler) notifyPlayerCountChanged(room *Room) {
	room.mu.RLock()
	playerCount := len(room.players)
	room.mu.RUnlock()

	// 플레이어 수 변경 데이터 생성
	responseData := &ResponsePlayerCountChangedData{
		PlayerCount: playerCount,
	}

	response := NewSuccessResponse(ResponsePlayerCountChanged, responseData)

	// 방에 속한 모든 클라이언트에게 전송
	h.mu.RLock()
	for client := range h.clients {
		if client.IsInRoom && client.RoomID == room.ID {
			h.sendToClient(client, response)
		}
	}
	h.mu.RUnlock()

	log.Printf("방 %d의 플레이어 수 변경 알림 전송: %d명", room.ID, playerCount)
}

// 준비 완료 처리
func (h *Handler) handleReadyGame(client *Client) {
	// 방에 참여하지 않은 상태인지 확인
	if !client.IsInRoom {
		h.sendErrorWithSignal(client, RequestReadyGame, "방에 참여하지 않은 상태입니다")
		return
	}

	// 게임이 시작되지 않은 상태인지 확인
	h.roomMu.RLock()
	room, roomExists := h.rooms[client.RoomID]
	h.roomMu.RUnlock()

	if !roomExists {
		h.sendErrorWithSignal(client, RequestReadyGame, "존재하지 않는 방입니다")
		return
	}

	if !room.isGameStarted {
		h.sendErrorWithSignal(client, RequestReadyGame, "게임이 시작되지 않은 상태입니다")
		return
	}

	// 플레이어를 준비 완료 상태로 설정
	room.mu.Lock()
	room.readyPlayers[client.ID] = true
	readyCount := len(room.readyPlayers)
	totalPlayers := len(room.players)
	room.mu.Unlock()

	log.Printf("플레이어 준비 완료: %s (%s) - 준비: %d/%d", client.ID, client.Username, readyCount, totalPlayers)

	// 모든 플레이어가 준비 완료했는지 확인
	if readyCount == totalPlayers {
		log.Printf("모든 플레이어 준비 완료! 게임 시작!")

		// 카드 게임 시작
		room.mu.Lock()
		room.isCardGameStarted = true
		room.currentPlayerIndex = 0 // 첫 번째 플레이어부터 시작
		room.mu.Unlock()

		// 카드 공개 타이머 시작
		h.startCardTimer(room)

		// 게임 제한시간 타이머 시작
		h.startGameTimer(room)

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
		if count == r.fruitBellCount {
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

	return totalCount == r.fruitBellCount
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

// 비밀번호 해싱 함수
func hashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(bytes), err
}

// 비밀번호 검증 함수
func checkPassword(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

// 클라이언트 로그인 상태 확인
func (c *Client) IsUserLoggedIn() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.IsLoggedIn
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

// 랜덤 숫자 생성 (지정된 자릿수)
func generateRandomNumber(digits int) string {
	const charset = "0123456789"
	b := make([]byte, digits)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
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
				h.roomMu.RLock()
				room, roomExists := h.rooms[client.RoomID]
				h.roomMu.RUnlock()

				if !roomExists {
					log.Printf("방이 존재하지 않음: %s", client.RoomID)
					client.mu.Lock()
					client.IsInRoom = false
					client.RoomID = 0
					client.mu.Unlock()
					continue
				}

				if !room.isGameStarted {
					// 게임이 시작되지 않은 상태: LeaveRoom과 동일하게 처리
					log.Printf("게임 시작 전 플레이어 연결 해제: %s (%s)", client.ID, client.Username)

					// 플레이어를 방에서 제거
					room.mu.Lock()
					delete(room.players, client.ID)
					room.mu.Unlock()

					// 클라이언트 상태 업데이트
					client.mu.Lock()
					client.IsInRoom = false
					client.RoomID = 0
					client.Username = ""
					client.IsLoggedIn = false
					client.UserID = ""
					client.UserNickname = ""
					client.mu.Unlock()

					log.Printf("플레이어 방에서 제거: %s", client.ID)

					// 방이 비었으면 방 삭제
					room.mu.RLock()
					if len(room.players) == 0 {
						room.mu.RUnlock()
						h.roomMu.Lock()
						delete(h.rooms, client.RoomID)
						h.roomMu.Unlock()
						log.Printf("방이 비어서 삭제: %s", client.RoomID)
					} else {
						room.mu.RUnlock()
					}
				} else {
					// 게임이 시작된 상태: 단순히 브로드캐스트에서 제외
					log.Printf("게임 진행 중 플레이어 연결 해제: %s (%s) - 브로드캐스트에서 제외", client.ID, client.Username)

					// 클라이언트 상태만 업데이트 (방에서는 제거하지 않음)
					client.mu.Lock()
					client.IsInRoom = false
					client.RoomID = 0
					client.Username = ""
					client.IsLoggedIn = false
					client.UserID = ""
					client.UserNickname = ""
					client.mu.Unlock()
				}

				// 모든 플레이어가 연결을 끊었는지 확인
				h.checkAllPlayersDisconnected(room)
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
func (h *Handler) checkAllPlayersDisconnected(room *Room) {
	room.mu.RLock()
	isGameStarted := room.isGameStarted
	room.mu.RUnlock()

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

		room.mu.Lock()
		// 게임 상태 초기화
		room.isGameStarted = false
		room.isCardGameStarted = false
		room.playerCards = nil
		room.readyPlayers = nil
		room.publicFruitIndexes = nil           // 공개된 카드 배열 초기화
		room.publicFruitCounts = nil            // 공개된 카드 배열 초기화
		room.bellRung = false                   // 벨 누르기 상태 초기화
		room.isTimeExpired = false              // 시간제한 상태 초기화
		room.playerIndexes = nil                // 플레이어 인덱스 매핑 초기화
		room.players = make(map[string]*Player) // 방 비우기

		// 카드 타이머 정지
		if room.cardTimer != nil {
			room.cardTimer.Stop()
			room.cardTimer = nil
		}
		room.mu.Unlock()

		log.Printf("게임 상태 초기화 완료")
	}
}

// 게임 템포를 시간 간격으로 변환
func getGameTempoInterval(gameTempo int) time.Duration {
	switch gameTempo {
	case 0:
		return 3 * time.Second
	case 1:
		return 2 * time.Second
	case 2:
		return 1500 * time.Millisecond // 1.5초
	case 3:
		return 1 * time.Second
	default:
		return 3 * time.Second // 기본값
	}
}

// 카드 공개 타이머 시작
func (h *Handler) startCardTimer(room *Room) {
	// 기존 타이머가 있다면 정지
	if room.cardTimer != nil {
		room.cardTimer.Stop()
	}

	// 게임 템포에 따른 간격으로 카드 공개
	interval := getGameTempoInterval(room.gameTempo)
	room.cardTimer = time.AfterFunc(interval, func() {
		h.openCard(room)
	})
}

// 카드 공개
func (h *Handler) openCard(room *Room) {
	room.mu.Lock()
	defer room.mu.Unlock()

	// 카드 게임이 시작되지 않았으면 무시
	if !room.isCardGameStarted {
		return
	}

	// 플레이어가 없으면 무시
	totalPlayers := len(room.players)
	if totalPlayers == 0 {
		log.Printf("플레이어가 없어서 카드 공개 중단")
		return
	}

	// 랜덤 과일 인덱스 (0 ~ fruitVariation-1)
	fruitIndex := rand.Intn(room.fruitVariation)

	// 랜덤 과일 개수 (1 ~ 5)
	fruitCount := rand.Intn(5) + 1

	// 현재 플레이어 인덱스
	playerIndex := room.currentPlayerIndex

	// 카드를 가진 플레이어를 찾을 때까지 순환
	originalPlayerIndex := playerIndex
	for room.playerCards[playerIndex] <= 0 {
		// 다음 플레이어로 순환
		room.currentPlayerIndex = (room.currentPlayerIndex + 1) % totalPlayers
		playerIndex = room.currentPlayerIndex

		// 한 바퀴 돌았는데도 카드를 가진 플레이어가 없으면 게임 종료
		if playerIndex == originalPlayerIndex {
			log.Printf("모든 플레이어가 카드를 가지고 있지 않아서 게임 종료")

			// 각 플레이어가 공개한 카드를 자신의 손패로 되돌리기
			room.returnOpenCardsToPlayers()

			log.Printf("=== openCard에서 endGameInternal 호출 ===")
			h.endGameInternal(room)
			return
		}
	}

	// 플레이어 손패에서 카드 1장 제거
	room.playerCards[playerIndex]--
	room.openCards[playerIndex]++

	// 해당 플레이어의 공개된 카드 정보 업데이트
	room.publicFruitIndexes[playerIndex] = fruitIndex
	room.publicFruitCounts[playerIndex] = fruitCount

	// 다음 플레이어로 순환 (카드를 낸 후)
	room.currentPlayerIndex = (room.currentPlayerIndex + 1) % totalPlayers

	// 벨 누르기 상태 리셋 (새로운 카드가 공개됨)
	room.bellRung = false

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
	interval := getGameTempoInterval(room.gameTempo)
	room.cardTimer = time.AfterFunc(interval, func() {
		h.openCard(room)
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
	h.roomMu.RLock()
	room, roomExists := h.rooms[client.RoomID]
	h.roomMu.RUnlock()

	if !roomExists {
		h.sendErrorWithSignal(client, RequestRingBell, "존재하지 않는 방입니다")
		return
	}

	if !room.isGameStarted {
		h.sendErrorWithSignal(client, RequestRingBell, "게임이 시작되지 않은 상태입니다")
		return
	}

	// 이미 벨이 눌렸는지 확인
	room.mu.Lock()
	if room.bellRung {
		room.mu.Unlock()
		log.Printf("플레이어 벨 누름 무시: %s (%s) - 이미 벨이 눌린 상태", client.ID, client.Username)
		return
	}

	// 벨 누르기 상태 설정
	room.bellRung = true
	room.mu.Unlock()

	// 종을 칠 수 있는 타이밍인지 확인
	isBellRingingTime := room.IsBellRingingTime()

	// 벨을 누른 플레이어의 인덱스 찾기 (게임 시작 시 설정된 인덱스 사용)
	room.mu.RLock()
	playerIndex, exists := room.playerIndexes[client.ID]
	room.mu.RUnlock()

	if !exists {
		log.Printf("플레이어 인덱스를 찾을 수 없음: %s (%s)", client.ID, client.Username)
		h.sendErrorWithSignal(client, RequestRingBell, "플레이어 인덱스를 찾을 수 없습니다")
		return
	}

	log.Printf("플레이어 벨 누름: %s (%s) - 종을 칠 수 있는 타이밍: %v, 플레이어 인덱스: %d", client.ID, client.Username, isBellRingingTime, playerIndex)

	// OpenCard 타이머 초기화
	h.resetCardTimer(room)

	// 벨 누르기 결과 처리
	if isBellRingingTime {
		// 벨을 올바르게 누른 경우, 공개된 모든 카드를 해당 플레이어의 손패에 추가
		room.AddAllPublicCardsToPlayer(playerIndex)
		log.Printf("벨 누르기 성공! 플레이어 %d의 손패에 공개된 모든 카드 추가", playerIndex)

		// 업데이트된 카드 개수 배열 가져오기
		room.mu.RLock()
		updatedPlayerCards := make([]int, len(room.playerCards))
		copy(updatedPlayerCards, room.playerCards)
		isTimeExpired := room.isTimeExpired
		room.mu.RUnlock()

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

		// 시간제한이 끝난 후 올바르게 종을 친 경우 게임 종료
		if isTimeExpired {
			log.Printf("시간제한 후 올바른 벨 누르기로 게임 종료")
			h.endGame(room)
		}
	} else {
		// 벨을 잘못 누른 경우, 다른 플레이어들에게 카드 분배
		cardGivenTo := room.DistributeCardsFromPlayer(playerIndex)
		log.Printf("벨 누르기 실패! 플레이어 %d가 다른 플레이어들에게 카드 분배", playerIndex)

		// 업데이트된 카드 개수 배열 다시 가져오기
		room.mu.RLock()
		updatedPlayerCards := make([]int, len(room.playerCards))
		copy(updatedPlayerCards, room.playerCards)
		room.mu.RUnlock()

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

// 감정표현 처리
func (h *Handler) handleEmotion(client *Client, request *RequestPacket) {
	// 방에 참여하지 않은 상태인지 확인
	if !client.IsInRoom {
		h.sendErrorWithSignal(client, RequestEmotion, "방에 참여하지 않은 상태입니다")
		return
	}

	// 게임이 시작되지 않은 상태인지 확인
	h.roomMu.RLock()
	room, roomExists := h.rooms[client.RoomID]
	h.roomMu.RUnlock()

	if !roomExists {
		h.sendErrorWithSignal(client, RequestEmotion, "존재하지 않는 방입니다")
		return
	}

	if !room.isGameStarted {
		h.sendErrorWithSignal(client, RequestEmotion, "게임이 시작되지 않은 상태입니다")
		return
	}

	// 요청 데이터 파싱
	var emotionData RequestEmotionData

	// request.Data가 map[string]interface{}인 경우를 처리
	if dataMap, ok := request.Data.(map[string]interface{}); ok {
		if emotionType, exists := dataMap["emotionType"]; exists {
			if emotionTypeFloat, ok := emotionType.(float64); ok {
				emotionData.EmotionType = int(emotionTypeFloat)
			} else {
				log.Printf("감정표현 타입이 숫자가 아님: %v", emotionType)
				h.sendErrorWithSignal(client, RequestEmotion, "잘못된 감정표현 타입입니다")
				return
			}
		} else {
			log.Printf("감정표현 데이터에 emotionType이 없음")
			h.sendErrorWithSignal(client, RequestEmotion, "감정표현 타입이 없습니다")
			return
		}
	} else {
		log.Printf("감정표현 데이터 형식 오류: %v", request.Data)
		h.sendErrorWithSignal(client, RequestEmotion, "잘못된 감정표현 데이터 형식입니다")
		return
	}

	// 1초 이내 중복 감정표현 체크
	room.mu.Lock()
	lastTime, exists := room.lastEmotionTimes[client.ID]
	now := time.Now()

	if exists && now.Sub(lastTime) < time.Duration(config.EmotionCooldown)*time.Second {
		room.mu.Unlock()
		log.Printf("감정표현 무시: %s (%s) - %d초 이내 중복 감정표현", client.ID, client.Username, config.EmotionCooldown)
		return
	}

	// 마지막 감정표현 시간 업데이트
	room.lastEmotionTimes[client.ID] = now
	room.mu.Unlock()

	// 플레이어 인덱스 찾기
	room.mu.RLock()
	playerIndex, exists := room.playerIndexes[client.ID]
	room.mu.RUnlock()

	if !exists {
		log.Printf("플레이어 인덱스를 찾을 수 없음: %s (%s)", client.ID, client.Username)
		h.sendErrorWithSignal(client, RequestEmotion, "플레이어 인덱스를 찾을 수 없습니다")
		return
	}

	log.Printf("감정표현: %s (%s) - 감정타입: %d, 플레이어 인덱스: %d", client.ID, client.Username, emotionData.EmotionType, playerIndex)

	// 감정표현 응답 데이터 생성
	responseEmotionData := &ResponseEmotionData{
		PlayerIndex: playerIndex,
		EmotionType: emotionData.EmotionType,
	}

	// 모든 클라이언트에게 감정표현 패킷 전송
	h.mu.RLock()
	for c := range h.clients {
		if c.IsInRoom {
			response := NewSuccessResponse(ResponseEmotion, responseEmotionData)
			h.sendToClient(c, response)
		}
	}
	h.mu.RUnlock()

	log.Printf("감정표현 전송 완료 - 플레이어 인덱스: %d, 감정타입: %d", playerIndex, emotionData.EmotionType)
}

// 계정 생성 처리
func (h *Handler) handleCreateAccount(client *Client, request *RequestPacket) {
	// 요청 데이터 파싱
	var createAccountData RequestCreateAccountData

	// request.Data가 map[string]interface{}인 경우를 처리
	if dataMap, ok := request.Data.(map[string]interface{}); ok {
		// ID 확인
		if id, exists := dataMap["id"]; exists {
			if idStr, ok := id.(string); ok {
				createAccountData.ID = idStr
			} else {
				log.Printf("계정 생성 ID가 문자열이 아님: %v", id)
				h.sendErrorWithSignal(client, RequestCreateAccount, "잘못된 ID 형식입니다")
				return
			}
		} else {
			log.Printf("계정 생성 데이터에 ID가 없음")
			h.sendErrorWithSignal(client, RequestCreateAccount, "ID가 없습니다")
			return
		}

		// Password 확인
		if password, exists := dataMap["password"]; exists {
			if passwordStr, ok := password.(string); ok {
				createAccountData.Password = passwordStr
			} else {
				log.Printf("계정 생성 Password가 문자열이 아님: %v", password)
				h.sendErrorWithSignal(client, RequestCreateAccount, "잘못된 Password 형식입니다")
				return
			}
		} else {
			log.Printf("계정 생성 데이터에 Password가 없음")
			h.sendErrorWithSignal(client, RequestCreateAccount, "Password가 없습니다")
			return
		}

		// Nickname 확인
		if nickname, exists := dataMap["nickname"]; exists {
			if nicknameStr, ok := nickname.(string); ok {
				createAccountData.Nickname = nicknameStr
			} else {
				log.Printf("계정 생성 Nickname이 문자열이 아님: %v", nickname)
				h.sendErrorWithSignal(client, RequestCreateAccount, "잘못된 Nickname 형식입니다")
				return
			}
		} else {
			log.Printf("계정 생성 데이터에 Nickname이 없음")
			h.sendErrorWithSignal(client, RequestCreateAccount, "Nickname이 없습니다")
			return
		}
	} else {
		log.Printf("계정 생성 데이터 형식 오류: %v", request.Data)
		h.sendErrorWithSignal(client, RequestCreateAccount, "잘못된 계정 생성 데이터 형식입니다")
		return
	}

	// 데이터 유효성 검사
	if createAccountData.ID == "" {
		h.sendErrorWithSignal(client, RequestCreateAccount, "ID는 비어있을 수 없습니다")
		return
	}
	if len(createAccountData.ID) > 10 {
		h.sendErrorWithSignal(client, RequestCreateAccount, "ID는 10자를 넘을 수 없습니다")
		return
	}
	if createAccountData.Password == "" {
		h.sendErrorWithSignal(client, RequestCreateAccount, "Password는 비어있을 수 없습니다")
		return
	}
	if len(createAccountData.Password) > 10 {
		h.sendErrorWithSignal(client, RequestCreateAccount, "Password는 10자를 넘을 수 없습니다")
		return
	}
	if createAccountData.Nickname == "" {
		h.sendErrorWithSignal(client, RequestCreateAccount, "Nickname은 비어있을 수 없습니다")
		return
	}
	if len(createAccountData.Nickname) > 10 {
		h.sendErrorWithSignal(client, RequestCreateAccount, "Nickname은 10자를 넘을 수 없습니다")
		return
	}

	log.Printf("계정 생성 요청: ID=%s, Nickname=%s", createAccountData.ID, createAccountData.Nickname)

	// DB에 계정 정보 저장
	if err := h.saveAccountToDB(createAccountData); err != nil {
		log.Printf("계정 생성 실패: ID=%s, 오류=%v", createAccountData.ID, err)
		h.sendErrorWithSignal(client, RequestCreateAccount, "계정 생성에 실패했습니다")
		return
	}

	// 계정 생성 성공 응답
	responseData := &ResponseCreateAccountData{
		ID: createAccountData.ID,
	}

	response := NewSuccessResponse(ResponseCreateAccount, responseData)
	h.sendToClient(client, response)

	log.Printf("계정 생성 성공: ID=%s", createAccountData.ID)
}

// 방 목록 조회 처리
func (h *Handler) handleGetRoomList(client *Client) {
	h.roomMu.RLock()
	defer h.roomMu.RUnlock()

	var roomList []RoomInfo

	// 모든 방을 순회하면서 게임 중이 아닌 방만 필터링
	for roomID, room := range h.rooms {
		room.mu.RLock()
		isGameStarted := room.isGameStarted
		playerCount := len(room.players)
		room.mu.RUnlock()

		// 게임 중이 아닌 방만 목록에 추가
		if !isGameStarted {
			roomInfo := RoomInfo{
				RoomID:         roomID,
				RoomName:       room.Name,
				PlayerCount:    playerCount,
				MaxPlayerCount: room.maxPlayers,
				FruitVariation: room.fruitVariation,
				FruitCount:     room.fruitBellCount,
				Speed:          room.gameTempo,
			}
			roomList = append(roomList, roomInfo)
		}
	}

	// 방 목록 응답 전송 (빈 배열 보장)
	if roomList == nil {
		roomList = []RoomInfo{}
	}
	response := NewSuccessResponse(ResponseGetRoomList, map[string]interface{}{
		"rooms": roomList,
	})
	h.sendToClient(client, response)

	log.Printf("방 목록 조회: %d개의 방 정보 전송", len(roomList))
}

// 방 생성 처리
func (h *Handler) handleCreateRoom(client *Client, request *RequestPacket) {
	// 이미 방에 참여한 상태인지 확인
	if client.IsInRoom {
		h.sendErrorWithSignal(client, RequestCreateRoom, "이미 방에 참여한 상태입니다")
		return
	}

	// 요청 데이터 파싱
	var createRoomData RequestCreateRoomData

	// request.Data가 map[string]interface{}인 경우를 처리
	if dataMap, ok := request.Data.(map[string]interface{}); ok {
		// RoomName 확인
		if roomName, exists := dataMap["roomName"]; exists {
			if roomNameStr, ok := roomName.(string); ok {
				createRoomData.RoomName = roomNameStr
			} else {
				log.Printf("방 이름이 문자열이 아님: %v", roomName)
				h.sendErrorWithSignal(client, RequestCreateRoom, "잘못된 방 이름 형식입니다")
				return
			}
		} else {
			log.Printf("방 생성 데이터에 roomName이 없음")
			h.sendErrorWithSignal(client, RequestCreateRoom, "방 이름이 없습니다")
			return
		}

		// MaxPlayerCount 확인
		if maxPlayerCount, exists := dataMap["maxPlayerCount"]; exists {
			if maxPlayerCountFloat, ok := maxPlayerCount.(float64); ok {
				createRoomData.MaxPlayerCount = int(maxPlayerCountFloat)
			} else {
				log.Printf("최대 플레이어 수가 숫자가 아님: %v", maxPlayerCount)
				h.sendErrorWithSignal(client, RequestCreateRoom, "잘못된 최대 플레이어 수 형식입니다")
				return
			}
		} else {
			log.Printf("방 생성 데이터에 maxPlayerCount가 없음")
			h.sendErrorWithSignal(client, RequestCreateRoom, "최대 플레이어 수가 없습니다")
			return
		}

		// FruitVariation 확인
		if fruitVariation, exists := dataMap["fruitVariation"]; exists {
			if fruitVariationFloat, ok := fruitVariation.(float64); ok {
				createRoomData.FruitVariation = int(fruitVariationFloat)
			} else {
				log.Printf("과일 종류 수가 숫자가 아님: %v", fruitVariation)
				h.sendErrorWithSignal(client, RequestCreateRoom, "잘못된 과일 종류 수 형식입니다")
				return
			}
		} else {
			log.Printf("방 생성 데이터에 fruitVariation이 없음")
			h.sendErrorWithSignal(client, RequestCreateRoom, "과일 종류 수가 없습니다")
			return
		}

		// FruitCount 확인
		if fruitCount, exists := dataMap["fruitCount"]; exists {
			if fruitCountFloat, ok := fruitCount.(float64); ok {
				createRoomData.FruitCount = int(fruitCountFloat)
			} else {
				log.Printf("과일 개수가 숫자가 아님: %v", fruitCount)
				h.sendErrorWithSignal(client, RequestCreateRoom, "잘못된 과일 개수 형식입니다")
				return
			}
		} else {
			log.Printf("방 생성 데이터에 fruitCount가 없음")
			h.sendErrorWithSignal(client, RequestCreateRoom, "과일 개수가 없습니다")
			return
		}

		// Speed 확인
		if speed, exists := dataMap["speed"]; exists {
			if speedFloat, ok := speed.(float64); ok {
				createRoomData.Speed = int(speedFloat)
			} else {
				log.Printf("게임 템포가 숫자가 아님: %v", speed)
				h.sendErrorWithSignal(client, RequestCreateRoom, "잘못된 게임 템포 형식입니다")
				return
			}
		} else {
			log.Printf("방 생성 데이터에 speed가 없음")
			h.sendErrorWithSignal(client, RequestCreateRoom, "게임 템포가 없습니다")
			return
		}
	} else {
		log.Printf("방 생성 데이터 형식 오류: %v", request.Data)
		h.sendErrorWithSignal(client, RequestCreateRoom, "잘못된 방 생성 데이터 형식입니다")
		return
	}

	// 데이터 유효성 검사
	if createRoomData.RoomName == "" {
		h.sendErrorWithSignal(client, RequestCreateRoom, "방 이름은 비어있을 수 없습니다")
		return
	}
	if createRoomData.MaxPlayerCount < 2 || createRoomData.MaxPlayerCount > 8 {
		h.sendErrorWithSignal(client, RequestCreateRoom, "최대 플레이어 수는 2-8명이어야 합니다")
		return
	}
	if createRoomData.FruitVariation < 2 || createRoomData.FruitVariation > 6 {
		h.sendErrorWithSignal(client, RequestCreateRoom, "과일 종류 수는 2-6개여야 합니다")
		return
	}
	if createRoomData.FruitCount < 3 || createRoomData.FruitCount > 8 {
		h.sendErrorWithSignal(client, RequestCreateRoom, "과일 개수는 3-8개여야 합니다")
		return
	}
	if createRoomData.Speed < 0 || createRoomData.Speed > 3 {
		h.sendErrorWithSignal(client, RequestCreateRoom, "게임 템포는 0-3이어야 합니다")
		return
	}

	log.Printf("방 생성 요청: %s (최대인원:%d, 과일종류:%d, 벨개수:%d, 템포:%d)",
		createRoomData.RoomName, createRoomData.MaxPlayerCount, createRoomData.FruitVariation,
		createRoomData.FruitCount, createRoomData.Speed)

	// 새로운 방 ID 생성
	h.roomMu.Lock()
	newRoomID := 1
	for id := range h.rooms {
		if id >= newRoomID {
			newRoomID = id + 1
		}
	}
	h.roomMu.Unlock()

	// 새 방 생성
	newRoom := &Room{
		ID:               newRoomID,
		Name:             createRoomData.RoomName,
		players:          make(map[string]*Player),
		maxPlayers:       createRoomData.MaxPlayerCount,
		fruitVariation:   createRoomData.FruitVariation,
		fruitBellCount:   createRoomData.FruitCount,
		gameTempo:        createRoomData.Speed,
		lastEmotionTimes: make(map[string]time.Time),
	}

	// 방을 핸들러에 등록
	h.roomMu.Lock()
	h.rooms[newRoomID] = newRoom
	h.roomMu.Unlock()

	// 플레이어를 방에 추가
	username := "Player" + generateRandomNumber(4) // 기본값: 랜덤 숫자 4개를 사용자명으로

	// 로그인된 사용자인 경우 닉네임 사용
	if client.IsUserLoggedIn() {
		username = client.UserNickname
	}

	player := &Player{
		ID:       client.ID,
		Username: username,
	}

	newRoom.mu.Lock()
	newRoom.players[client.ID] = player
	newRoom.mu.Unlock()

	// 클라이언트 상태 업데이트
	client.mu.Lock()
	client.IsInRoom = true
	client.RoomID = newRoomID
	client.Username = player.Username
	client.mu.Unlock()

	// 방 생성 성공 응답
	responseData := &ResponseCreateRoomData{
		RoomID: newRoomID,
	}

	response := NewSuccessResponse(ResponseCreateRoom, responseData)
	h.sendToClient(client, response)

	log.Printf("방 생성 성공: %s (ID:%d) - 플레이어: %s", createRoomData.RoomName, newRoomID, player.Username)

	// 방 입장 성공 응답 (즉시 전송)
	enterResponse := NewSuccessResponse(ResponseEnterRoom, map[string]interface{}{
		"roomId":         newRoomID,
		"roomName":       newRoom.Name,
		"maxPlayers":     newRoom.maxPlayers,
		"fruitVariation": newRoom.fruitVariation,
		"fruitBellCount": newRoom.fruitBellCount,
		"gameTempo":      newRoom.gameTempo,
	})
	h.sendToClient(client, enterResponse)

	log.Printf("플레이어 방 입장: %s (%s) -> %s", client.ID, player.Username, newRoomID)

	// 방의 모든 클라이언트에게 플레이어 수 변경 알림
	h.notifyPlayerCountChanged(newRoom)
}

// DB에 계정 정보 저장
func (h *Handler) saveAccountToDB(accountData RequestCreateAccountData) error {
	if !UseDatabase || db.DB == nil {
		// 로컬 테스트 모드이거나 DB가 연결되지 않은 경우 DB 작업을 건너뛰고 성공으로 반환
		log.Printf("로컬 테스트 모드 또는 DB 미연결: DB 저장 건너뛰기 - ID=%s", accountData.ID)
		return nil
	}

	// 중복 ID 검사
	var existingID string
	err := db.DB.QueryRow("SELECT id FROM Users WHERE id = $1", accountData.ID).Scan(&existingID)
	if err == nil {
		// 이미 존재하는 ID
		return fmt.Errorf("이미 존재하는 ID입니다")
	} else if err != sql.ErrNoRows {
		// DB 오류
		return fmt.Errorf("DB 조회 오류: %v", err)
	}

	// 비밀번호 해싱
	hashedPassword, err := hashPassword(accountData.Password)
	if err != nil {
		return fmt.Errorf("비밀번호 해싱 오류: %v", err)
	}

	// 새 계정 저장 (해싱된 비밀번호 저장)
	_, err = db.DB.Exec("INSERT INTO Users (id, password, nickname) VALUES ($1, $2, $3)",
		accountData.ID, hashedPassword, accountData.Nickname)
	if err != nil {
		return fmt.Errorf("계정 저장 오류: %v", err)
	}

	return nil
}

// 로그인 처리
func (h *Handler) handleLogin(client *Client, request *RequestPacket) {
	// 요청 데이터 파싱
	var loginData RequestLoginData

	// request.Data가 map[string]interface{}인 경우를 처리
	if dataMap, ok := request.Data.(map[string]interface{}); ok {
		// ID 확인
		if id, exists := dataMap["id"]; exists {
			if idStr, ok := id.(string); ok {
				loginData.ID = idStr
			} else {
				log.Printf("로그인 ID가 문자열이 아님: %v", id)
				h.sendErrorWithSignal(client, RequestLogin, "잘못된 ID 형식입니다")
				return
			}
		} else {
			log.Printf("로그인 데이터에 ID가 없음")
			h.sendErrorWithSignal(client, RequestLogin, "ID가 없습니다")
			return
		}

		// Password 확인
		if password, exists := dataMap["password"]; exists {
			if passwordStr, ok := password.(string); ok {
				loginData.Password = passwordStr
			} else {
				log.Printf("로그인 Password가 문자열이 아님: %v", password)
				h.sendErrorWithSignal(client, RequestLogin, "잘못된 Password 형식입니다")
				return
			}
		} else {
			log.Printf("로그인 데이터에 Password가 없음")
			h.sendErrorWithSignal(client, RequestLogin, "Password가 없습니다")
			return
		}
	} else {
		log.Printf("로그인 데이터 형식 오류: %v", request.Data)
		h.sendErrorWithSignal(client, RequestLogin, "잘못된 로그인 데이터 형식입니다")
		return
	}

	// 데이터 유효성 검사
	if loginData.ID == "" {
		h.sendErrorWithSignal(client, RequestLogin, "ID는 비어있을 수 없습니다")
		return
	}
	if loginData.Password == "" {
		h.sendErrorWithSignal(client, RequestLogin, "Password는 비어있을 수 없습니다")
		return
	}

	log.Printf("로그인 요청: ID=%s", loginData.ID)

	// DB에서 사용자 정보 확인
	userData, err := h.verifyUserLogin(loginData)
	if err != nil {
		log.Printf("로그인 실패: ID=%s, 오류=%v", loginData.ID, err)
		h.sendErrorWithSignal(client, RequestLogin, "로그인에 실패했습니다")
		return
	}

	// 클라이언트 로그인 상태 업데이트
	client.mu.Lock()
	client.IsLoggedIn = true
	client.UserID = userData.ID
	client.UserNickname = userData.Nickname
	client.mu.Unlock()

	// 로그인 성공 응답
	responseData := &ResponseLoginData{
		ID:       userData.ID,
		Nickname: userData.Nickname,
	}

	response := NewSuccessResponse(ResponseLogin, responseData)
	h.sendToClient(client, response)

	log.Printf("로그인 성공: ID=%s, Nickname=%s", userData.ID, userData.Nickname)
}

// DB에서 사용자 로그인 확인
func (h *Handler) verifyUserLogin(loginData RequestLoginData) (*ResponseLoginData, error) {
	if !UseDatabase || db.DB == nil {
		// 로컬 테스트 모드이거나 DB가 연결되지 않은 경우 DB 작업을 건너뛰고 성공으로 반환
		log.Printf("로컬 테스트 모드 또는 DB 미연결: DB 로그인 확인 건너뛰기 - ID=%s", loginData.ID)
		return &ResponseLoginData{
			ID:       loginData.ID,
			Nickname: "LocalUser" + loginData.ID, // 로컬 테스트용 닉네임
		}, nil
	}

	var id, nickname string
	var password string

	// DB에서 사용자 정보 조회
	err := db.DB.QueryRow("SELECT id, password, nickname FROM Users WHERE id = $1", loginData.ID).Scan(&id, &password, &nickname)
	if err != nil {
		if err == sql.ErrNoRows {
			// 사용자가 존재하지 않음
			return nil, fmt.Errorf("존재하지 않는 ID입니다")
		}
		// DB 오류
		return nil, fmt.Errorf("DB 조회 오류: %v", err)
	}

	// 비밀번호 확인 (해싱된 비밀번호와 비교)
	if !checkPassword(loginData.Password, password) {
		return nil, fmt.Errorf("비밀번호가 일치하지 않습니다")
	}

	// 로그인 성공
	return &ResponseLoginData{
		ID:       id,
		Nickname: nickname,
	}, nil
}

// 공개된 모든 카드를 특정 플레이어의 손패에 추가
func (r *Room) AddAllPublicCardsToPlayer(playerIndex int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	totalCards := 0
	for i := 0; i < len(r.openCards); i++ {
		totalCards += r.openCards[i]
	}

	// 현재 플레이어의 카드 개수에 추가
	if playerIndex < len(r.playerCards) {
		r.playerCards[playerIndex] += totalCards
		log.Printf("플레이어 %d의 손패에 공개된 모든 카드 %d장 추가", playerIndex, totalCards)
	}

	// 공개된 카드 정보 초기화
	for i := 0; i < len(r.publicFruitIndexes); i++ {
		r.publicFruitIndexes[i] = -1
		r.publicFruitCounts[i] = -1
		r.openCards[i] = 0
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

// string 슬라이스를 섞는 함수
func shuffleStringSlice(slice []string) {
	for i := len(slice) - 1; i > 0; i-- {
		j := rand.Intn(i + 1)
		slice[i], slice[j] = slice[j], slice[i]
	}
}

// 공개된 카드를 각 플레이어의 손패로 되돌리는 함수
func (r *Room) returnOpenCardsToPlayers() {
	for i := 0; i < len(r.playerCards); i++ {
		if r.openCards[i] > 0 {
			r.playerCards[i] += r.openCards[i]
			log.Printf("플레이어 %d의 공개된 카드 %d장을 손패로 되돌림", i, r.openCards[i])
		}
	}

	// 공개된 카드 개수 초기화
	for i := 0; i < len(r.openCards); i++ {
		r.openCards[i] = 0
	}
	log.Printf("모든 플레이어의 공개된 카드 개수를 0으로 초기화")
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

// 게임 종료 처리 (뮤텍스가 이미 잠겨있는 경우를 위한 내부 함수)
func (h *Handler) endGameInternal(room *Room) {
	log.Printf("=== 게임 종료 함수 호출됨 ===")
	log.Printf("게임 제한시간 종료 - 게임 종료")

	// 각 플레이어가 공개한 카드를 자신의 손패로 되돌리기
	room.returnOpenCardsToPlayers()

	// 현재 플레이어 카드 개수와 순위 계산
	playerCards := make([]int, len(room.playerCards))
	copy(playerCards, room.playerCards)
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
	room.isGameStarted = false
	room.isCardGameStarted = false
	room.playerCards = nil
	room.readyPlayers = nil
	room.publicFruitIndexes = nil
	room.publicFruitCounts = nil
	room.openCards = nil
	room.bellRung = false
	room.isTimeExpired = false
	room.playerIndexes = nil
	room.players = make(map[string]*Player)
	room.lastEmotionTimes = make(map[string]time.Time)

	// 모든 클라이언트의 방 참여 상태 초기화
	h.mu.RLock()
	for c := range h.clients {
		if c.RoomID == room.ID {
			c.IsInRoom = false
			c.RoomID = 0
		}
	}
	h.mu.RUnlock()

	// 타이머들 정지
	if room.cardTimer != nil {
		room.cardTimer.Stop()
		room.cardTimer = nil
	}
	if room.gameTimer != nil {
		room.gameTimer.Stop()
		room.gameTimer = nil
	}

	log.Printf("게임 종료 완료 - 순위: %v", playerRanks)
}

// 게임 종료 처리 (외부에서 호출되는 함수)
func (h *Handler) endGame(room *Room) {
	room.mu.Lock()
	defer room.mu.Unlock()
	h.endGameInternal(room)
}

// 게임 타이머 시작
func (h *Handler) startGameTimer(room *Room) {
	// 기존 게임 타이머가 있다면 정지
	if room.gameTimer != nil {
		room.gameTimer.Stop()
	}

	// 설정된 제한시간 후 시간제한 플래그 설정
	room.gameTimer = time.AfterFunc(time.Duration(config.GameTimeLimit)*time.Second, func() {
		room.mu.Lock()
		room.isTimeExpired = true
		room.mu.Unlock()
		log.Printf("게임 제한시간 종료 - 누군가가 올바르게 종을 칠 때까지 게임 계속 진행")
	})

	log.Printf("게임 타이머 시작 - %d초 후 시간제한", config.GameTimeLimit)
}

// OpenCard 타이머 초기화
func (h *Handler) resetCardTimer(room *Room) {
	room.mu.Lock()
	defer room.mu.Unlock()

	// 기존 타이머가 있다면 정지
	if room.cardTimer != nil {
		room.cardTimer.Stop()
		room.cardTimer = nil
	}

	// 새로운 타이머 시작 (설정된 간격 후)
	room.cardTimer = time.AfterFunc(time.Duration(config.CardOpenInterval)*time.Second, func() {
		h.openCard(room)
	})

	log.Printf("OpenCard 타이머 초기화 완료")
}
