package socket

import (
    "errors"
    "sync"
    "time"

    "main/game"
)

// socket/room_manager.go

type RoomManager struct {
    mu    sync.RWMutex
    rooms map[string]*game.Room
}

// 생성자: hostID 없이 cfg 만
func NewRoomManager() *RoomManager {
    return &RoomManager{
        rooms: make(map[string]*game.Room),
    }
}

// CreateRoom: 최대 방 개수 & 플레이어 수 검증만, hostID 제거
func (m *RoomManager) CreateRoom(roomID string, cfg game.RoomConfig) error {
    m.mu.Lock()
    defer m.mu.Unlock()

    if len(m.rooms) >= MaxRooms {
        return errors.New("방은 최대 6개까지만 생성할 수 있습니다")
    }
    if cfg.MaxPlayers < MinPlayersPerRoom || cfg.MaxPlayers > MaxPlayersPerRoom {
        return errors.New("인원 수는 2~8명만 가능합니다")
    }

    m.rooms[roomID] = game.NewRoom(cfg)
    return nil
}

// JoinRoom: 플레이어 추가 후 “가득 찼으면” 즉시 게임 시작
func (m *RoomManager) JoinRoom(roomID, clientID string) error {
    m.mu.RLock()
    r, ok := m.rooms[roomID]
    m.mu.RUnlock()
    if !ok {
        return errors.New("존재하지 않는 방입니다")
    }
    if err := r.AddPlayer(clientID); err != nil {
        return err
    }

    // 자동 시작: 플레이어 수 == 설정된 최대치라면
    if r.PlayerCount() == r.Config().MaxPlayers {
        // CanStartGame 내부에서 준비(Ready) 없이 곧바로 StartGame 하도록 바꿔두고…
        r.StartGame()
    }
    return nil
}

// LeaveRoom: 나가면 “비어 있거나” 게임이 끝났을 때 방 삭제
func (m *RoomManager) LeaveRoom(roomID, clientID string) {
    m.mu.RLock()
    r, ok := m.rooms[roomID]
    m.mu.RUnlock()
    if !ok {
        return
    }
    r.RemovePlayer(clientID)

    // 폭파 조건
    if r.PlayerCount() == 0 || r.HasEnded() {
        m.DeleteRoom(roomID)
    }
}

// DeleteRoom 그대로 두면 됩니다
func (m *RoomManager) DeleteRoom(roomID string) {
    m.mu.Lock()
    defer m.mu.Unlock()
    delete(m.rooms, roomID)
}


// ListRooms: 방 목록 조회
func (m *RoomManager) ListRooms() []game.RoomInfo {
		m.mu.RLock()
		defer m.mu.RUnlock()

		rooms := make([]game.RoomInfo, 0, len(m.rooms))
		for _, r := range m.rooms {
				rooms = append(rooms, r.Info())
		}
		return rooms
}

// GetRoom: 특정 방 정보 조회
func (m *RoomManager) GetRoom(roomID string) (*game.Room, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	room, ok := m.rooms[roomID]
	if !ok {
		return nil, errors.New("존재하지 않는 방입니다")
	}
	return room, nil
}