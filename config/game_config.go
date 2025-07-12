package config

// 게임 설정 상수들
const (
	// 방 설정
	MaxPlayers = 4 // 방에 들어갈 수 있는 최대 플레이어 수

	// 벨 누르기 설정s
	BellRingingFruitCount = 5 // 종을 올바르게 치기 위한 과일 개수

	// 카드 공개 설정
	CardOpenInterval = 3 // 카드 공개 간격 (초)

	// 게임 시작 설정
	StartingCards = 5 // 게임 시작 시 각 플레이어가 받는 카드 수
)

// 게임 설정 구조체 (향후 확장성을 위해)
type GameConfig struct {
	MaxPlayers            int `json:"maxPlayers"`
	BellRingingFruitCount int `json:"bellRingingFruitCount"`
	CardOpenInterval      int `json:"cardOpenInterval"`
	StartingCards         int `json:"startingCards"`
}

// 기본 게임 설정 반환
func GetDefaultConfig() *GameConfig {
	return &GameConfig{
		MaxPlayers:            MaxPlayers,
		BellRingingFruitCount: BellRingingFruitCount,
		CardOpenInterval:      CardOpenInterval,
		StartingCards:         StartingCards,
	}
}
