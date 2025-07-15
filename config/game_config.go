package config

// 게임 설정 상수들
const (

	MaxRooms = 6

	// 방 설정
	MaxPlayers = 8 // 방에 들어갈 수 있는 최대 플레이어 수

	MinPlayers = 2 // 방에 들어갈 수 있는 최소 플레이어 수

	// 벨 누르기 설정s
	BellRingingFruitCount = 5 // 종을 올바르게 치기 위한 과일 개수

	// 카드 공개 설정
	CardOpenInterval = 2 // 카드 공개 간격 (초)

	// 게임 시작 설정
	StartingCards = 10 // 게임 시작 시 각 플레이어가 받는 카드 수

	// 게임 제한시간 설정
	GameTimeLimit = 120 // 게임 제한시간 (초)

	// 감정표현 설정
	EmotionCooldown = 2 // 감정표현 사이 제한시간 (초)
)

// 게임 설정 구조체 (향후 확장성을 위해)
type GameConfig struct {
	MaxPlayers            int `json:"maxPlayers"`
	BellRingingFruitCount int `json:"bellRingingFruitCount"`
	CardOpenInterval      int `json:"cardOpenInterval"`
	StartingCards         int `json:"startingCards"`
	GameTimeLimit         int `json:"gameTimeLimit"`
}

// 기본 게임 설정 반환
func GetDefaultConfig() *GameConfig {
	return &GameConfig{
		MaxPlayers:            MaxPlayers,
		BellRingingFruitCount: BellRingingFruitCount,
		CardOpenInterval:      CardOpenInterval,
		StartingCards:         StartingCards,
		GameTimeLimit:         GameTimeLimit,
	}
}
