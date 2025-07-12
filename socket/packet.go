package socket

import (
	"encoding/json"
	"log"
)

// 패킷 시그널 상수 (서버 -> 클라이언트)
const (
	ResponsePong = 1
)

// 클라이언트 요청 시그널 상수 (클라이언트 -> 서버)
const (
	RequestPing = 1
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
	return NewResponse(requestSignal, map[string]interface{}{
		"message": message,
	}, CodeError)
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
		RequestPing: true,
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
