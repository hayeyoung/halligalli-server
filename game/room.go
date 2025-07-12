package game

import (
	"sync"
	"time"
)

// 단일 방 구조체
type Room struct {
	mu             sync.RWMutex
	Players        []*Player
	TurnIndex      int
	TotalCardCount int
	GameStarted    bool
	RevealedCards  []Card
	PlayerHands    [][]Card
	Deck           []Card
	LastActivity   time.Time
}

// 플레이어 구조체
type Player struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	IsReady  bool   `json:"isReady"`
	IsActive bool   `json:"isActive"`
}

// 전역 단일 방 인스턴스
var GlobalRoom = &Room{
	Players:       make([]*Player, 0),
	RevealedCards: make([]Card, 0),
	LastActivity:  time.Now(),
}

// 방에 플레이어 추가
func (r *Room) AddPlayer(clientID, username string) *Player {
	r.mu.Lock()
	defer r.mu.Unlock()

	// 이미 존재하는 플레이어인지 확인
	for _, player := range r.Players {
		if player.ID == clientID {
			player.Username = username
			player.IsActive = true
			return player
		}
	}

	// 새 플레이어 추가
	player := &Player{
		ID:       clientID,
		Username: username,
		IsReady:  false,
		IsActive: true,
	}
	r.Players = append(r.Players, player)
	r.LastActivity = time.Now()
	return player
}

// 방에서 플레이어 제거
func (r *Room) RemovePlayer(clientID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for i, player := range r.Players {
		if player.ID == clientID {
			player.IsActive = false
			// 비활성 플레이어 제거
			r.Players = append(r.Players[:i], r.Players[i+1:]...)
			break
		}
	}
	r.LastActivity = time.Now()
}

// 플레이어 준비 상태 변경
func (r *Room) TogglePlayerReady(clientID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, player := range r.Players {
		if player.ID == clientID {
			player.IsReady = !player.IsReady
			r.LastActivity = time.Now()
			return player.IsReady
		}
	}
	return false
}

// 게임 시작 가능 여부 확인
func (r *Room) CanStartGame() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.Players) < 2 {
		return false
	}

	allReady := true
	for _, player := range r.Players {
		if !player.IsReady {
			allReady = false
			break
		}
	}

	return allReady
}

// 게임 시작
func (r *Room) StartGame() bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.CanStartGame() {
		return false
	}

	r.GameStarted = true
	r.TurnIndex = 0
	r.TotalCardCount = len(r.Deck)

	// 카드 분배
	r.PlayerHands = DealCards(r.Deck, len(r.Players))
	r.LastActivity = time.Now()

	return true
}

// 게임 종료
func (r *Room) EndGame() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.GameStarted = false
	r.TurnIndex = 0
	r.RevealedCards = make([]Card, 0)
	r.PlayerHands = make([][]Card, 0)
	r.Deck = make([]Card, 0)
	r.LastActivity = time.Now()
}

// 방 정보 가져오기
func (r *Room) GetRoomInfo() map[string]interface{} {
	r.mu.RLock()
	defer r.mu.RUnlock()

	players := make([]map[string]interface{}, 0, len(r.Players))
	for _, player := range r.Players {
		players = append(players, map[string]interface{}{
			"id":       player.ID,
			"username": player.Username,
			"isReady":  player.IsReady,
			"isActive": player.IsActive,
		})
	}

	return map[string]interface{}{
		"players":       players,
		"gameStarted":   r.GameStarted,
		"turnIndex":     r.TurnIndex,
		"totalCards":    r.TotalCardCount,
		"revealedCards": len(r.RevealedCards),
	}
}
