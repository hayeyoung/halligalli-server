# halligalli-server

할리갈리 게임 서버입니다.

## 시스템 구조

이 서버는 기본적인 WebSocket 연결과 Ping/Pong 기능만 제공합니다.

### 기능
- WebSocket 연결 관리
- Ping/Pong 통신
- 클라이언트 연결 상태 관리

## 패킷 구조

### 서버 응답 패킷 (서버 → 클라이언트)

서버에서 클라이언트들에게 보내는 모든 패킷은 다음과 같은 일관된 구조를 따릅니다:

```json
{
  "signal": 1,
  "data": { 데이터 내용들 },
  "code": 200
}
```

#### 필드 설명

- **signal**: 패킷의 종류를 나타내는 정수값
  - `1`: Pong (핑 응답)
  - `2`: Error (에러)

- **data**: 패킷 종류에 따라 달라지는 데이터 내용
- **code**: 요청 처리 상태
  - `200`: 정상 처리
  - `400`: 에러

### 클라이언트 요청 패킷 (클라이언트 → 서버)

클라이언트에서 서버로 보내는 모든 요청은 다음과 같은 구조를 따릅니다:

```json
{
  "signal": 1,
  "data": {}
}
```

#### 필드 설명

- **signal**: 요청의 종류를 나타내는 정수값
  - `1`: Ping (핑 요청)

- **data**: 요청 종류에 따라 달라지는 데이터 내용

#### 요청 예시

```json
// Ping 요청
{
  "signal": 1,
  "data": {}
}
```

### 사용 예시

```go
// 성공 패킷 생성
packet := NewSuccessResponse(SignalPong, map[string]interface{}{
    "timestamp": time.Now().Unix(),
})

// 에러 패킷 생성 (요청 signal과 동일한 signal 사용)
errorPacket := NewErrorResponse(RequestPing, "잘못된 요청입니다")
```

### 패킷 생성 함수

- `NewResponse(signal, data, code)`: 기본 패킷 생성
- `NewSuccessResponse(signal, data)`: 성공 패킷 생성 (code: 200)
- `NewErrorResponse(requestSignal, message)`: 에러 패킷 생성 (signal: 요청과 동일, code: 400)

### 패킷 검증

- `ValidateRequestPacket(data)`: 클라이언트 요청 패킷 검증
- 잘못된 형식의 패킷은 자동으로 에러 응답을 반환합니다
- 에러 응답의 signal은 원본 요청의 signal과 동일합니다

### 에러 처리 예시

클라이언트가 다음과 같은 요청을 보냈을 때:
```json
{
  "signal": 1,
  "data": null
}
```

서버는 다음과 같은 에러 응답을 반환합니다:
```json
{
  "signal": 1,
  "data": {
    "message": "data가 nil입니다"
  },
  "code": 400
}
```