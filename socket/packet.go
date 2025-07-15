package socket

import (
	"encoding/json"
	"log"
)

// 패킷 시그널 상수 (서버 -> 클라이언트)
const (
	ResponsePong               = 1
	ResponseEnterRoom          = 1001
	ResponseLeaveRoom          = 1002
	ResponseGetRoomList        = 1003
	ResponseCreateRoom         = 1004
	ResponsePlayerCountChanged = 1005

	ResponseStartGame = 1010
	ResponseReadyGame = 1011

	ResponseOpenCard        = 2000
	ResponseRingBellCorrect = 2002
	ResponseRingBellWrong   = 2003
	ResponseEmotion         = 2004

	ResponseEndGame = 3000

	ResponseCreateAccount = 4000
	ResponseLogin         = 4001
)

// 클라이언트 요청 시그널 상수 (클라이언트 -> 서버)
const (
	RequestPing        = 1
	RequestEnterRoom   = 1001
	RequestLeaveRoom   = 1002
	RequestGetRoomList = 1003
	RequestCreateRoom  = 1004

	RequestReadyGame = 1011
	RequestRingBell  = 2001
	RequestEmotion   = 2004

	RequestCreateAccount = 4000
	RequestLogin         = 4001
)

// 패킷 구조체 - 모든 클라이언트 응답에 사용
type ResponsePacket struct {
	Signal int         `json:"signal"`
	Data   interface{} `json:"data"`
	Code   int         `json:"code"`
}

// 클라이언트 요청 패킷 구조체
type RequestPacket struct {
	Signal int         `json:"signal"`
	Data   interface{} `json:"data"`
}

// 성공 코드
const (
	CodeSuccess = 200
	CodeError   = 400
)

// 패킷 생성 함수들
func NewResponse(signal int, data interface{}, code int) *ResponsePacket {
	return &ResponsePacket{
		Signal: signal,
		Data:   data,
		Code:   code,
	}
}

func NewSuccessResponse(signal int, data interface{}) *ResponsePacket {
	return NewResponse(signal, data, CodeSuccess)
}

func NewErrorResponse(requestSignal int, message string) *ResponsePacket {
	return NewResponse(requestSignal, map[string]interface{}{}, CodeError)
}

// 패킷을 JSON으로 마샬링
func (p *ResponsePacket) ToJSON() ([]byte, error) {
	return json.Marshal(p)
}

// 패킷을 JSON으로 마샬링하고 로그 출력
func (p *ResponsePacket) ToJSONWithLog() ([]byte, error) {
	data, err := json.Marshal(p)
	if err != nil {
		log.Printf("패킷 마샬링 오류: %v", err)
		return nil, err
	}
	log.Printf("전송 패킷: %s", string(data))
	return data, nil
}

// 클라이언트 요청 패킷 검증
func ValidateRequestPacket(data []byte) (*RequestPacket, error) {
	var request RequestPacket
	if err := json.Unmarshal(data, &request); err != nil {
		return nil, err
	}

	// signal이 유효한지 확인
	validSignals := map[int]bool{
		RequestPing:          true,
		RequestEnterRoom:     true,
		RequestLeaveRoom:     true,
		RequestGetRoomList:   true,
		RequestReadyGame:     true,
		RequestRingBell:      true,
		RequestEmotion:       true,
		RequestCreateAccount: true,
		RequestLogin:         true,
		RequestCreateRoom:    true,
	}

	if !validSignals[request.Signal] {
		return nil, &InvalidPacketError{Message: "유효하지 않은 signal"}
	}

	// data가 nil이 아닌지 확인
	if request.Data == nil {
		return nil, &InvalidPacketError{Message: "data가 nil입니다"}
	}

	return &request, nil
}

// 잘못된 패킷 에러
type InvalidPacketError struct {
	Message string
}

func (e *InvalidPacketError) Error() string {
	return e.Message
}

// 게임 시작 데이터 구조체
type GameStartData struct {
	PlayerCount   int      `json:"playerCount"`
	PlayerNames   []string `json:"playerNames"`
	MyIndex       int      `json:"myIndex"`
	StartingCards int      `json:"startingCards"`
	GameTimeLimit int      `json:"gameTimeLimit"` // 게임 제한시간 (초)
}

// 카드 공개 데이터 구조체
type OpenCardData struct {
	FruitIndex  int `json:"fruitIndex"`  // 0-2 (과일 종류)
	FruitCount  int `json:"fruitCount"`  // 1-5 (과일 개수)
	PlayerIndex int `json:"playerIndex"` // 카드를 낸 플레이어 인덱스
}

// 벨 누르기 성공 데이터 구조체
type RingBellCorrectData struct {
	PlayerIndex int   `json:"playerIndex"` // 벨을 누른 플레이어 인덱스
	PlayerCards []int `json:"playerCards"` // 각 플레이어별 덱의 카드 개수 배열
}

// 벨 누르기 실패 데이터 구조체
type RingBellWrongData struct {
	PlayerIndex int    `json:"playerIndex"` // 벨을 누른 플레이어 인덱스
	CardGivenTo []bool `json:"cardGivenTo"` // 카드를 받은 플레이어들 (bool 배열, 인덱스는 플레이어 인덱스)
	PlayerCards []int  `json:"playerCards"` // 각 플레이어별 덱의 카드 개수 배열
}

// 게임 종료 데이터 구조체
type EndGameData struct {
	PlayerCards []int `json:"playerCards"` // 각 플레이어의 카드 개수 배열
	PlayerRanks []int `json:"playerRanks"` // 각 플레이어의 순위 배열 (1등부터 시작)
}

// 감정표현 요청 데이터 구조체
type RequestEmotionData struct {
	EmotionType int `json:"emotionType"` // 감정표현 타입
}

// 감정표현 응답 데이터 구조체
type ResponseEmotionData struct {
	PlayerIndex int `json:"playerIndex"` // 감정표현을 한 플레이어 인덱스
	EmotionType int `json:"emotionType"` // 감정표현 타입
}

// 계정 생성 요청 데이터 구조체
type RequestCreateAccountData struct {
	ID       string `json:"id"`       // 아이디
	Password string `json:"password"` // 비밀번호
	Nickname string `json:"nickname"` // 닉네임
}

// 계정 생성 응답 데이터 구조체
type ResponseCreateAccountData struct {
	ID string `json:"id"` // 생성된 계정의 아이디
}

// 로그인 요청 데이터 구조체
type RequestLoginData struct {
	ID       string `json:"id"`       // 아이디
	Password string `json:"password"` // 비밀번호
}

// 로그인 응답 데이터 구조체
type ResponseLoginData struct {
	ID       string `json:"id"`       // 로그인한 유저의 아이디
	Nickname string `json:"nickname"` // 로그인한 유저의 닉네임
}

// 방 정보 데이터 구조체
type RoomInfo struct {
	RoomID         int    `json:"roomID"`         // 방 ID
	RoomName       string `json:"roomName"`       // 방 이름
	PlayerCount    int    `json:"playerCount"`    // 현재 플레이어 수
	MaxPlayerCount int    `json:"maxPlayerCount"` // 최대 플레이어 수
	FruitVariation int    `json:"fruitVariation"` // 과일 종류 수
	FruitCount     int    `json:"fruitCount"`     // 종을 올바르게 치기 위한 과일 수
	Speed          int    `json:"speed"`          // 게임 템포
}

// 방 생성 요청 데이터 구조체
type RequestCreateRoomData struct {
	RoomName       string `json:"roomName"`       // 방 이름
	MaxPlayerCount int    `json:"maxPlayerCount"` // 최대 플레이어 수
	FruitVariation int    `json:"fruitVariation"` // 과일 종류 수
	FruitCount     int    `json:"fruitCount"`     // 종을 올바르게 치기 위한 과일 수
	Speed          int    `json:"speed"`          // 게임 템포
}

// 방 생성 응답 데이터 구조체
type ResponseCreateRoomData struct {
	RoomID int `json:"roomID"` // 생성된 방의 ID
}

// 방 입장 요청 데이터 구조체
type RequestEnterRoomData struct {
	RoomID int `json:"roomId"` // 입장할 방 ID
}

// 플레이어 수 변경 응답 데이터 구조체
type ResponsePlayerCountChangedData struct {
	PlayerCount int `json:"playerCount"` // 현재 방의 플레이어 수
}
