# halligalli-server

할리갈리 게임 서버입니다.

## 시스템 구조

이 서버는 WebSocket 연결, Ping/Pong 기능, 방 관리, 게임 시작 기능을 제공합니다.

### 기능
- WebSocket 연결 관리
- Ping/Pong 통신
- 클라이언트 연결 상태 관리
- 단일 방 시스템 (최대 4명)
- 방 입장/나가기
- 게임 시작 (최대 인원 도달 시 자동 시작)

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
  - `1001`: EnterRoom (방 입장 응답)
  - `1002`: LeaveRoom (방 나가기 응답)
  - `1010`: StartGame (게임 시작)

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
  - `1001`: EnterRoom (방 입장 요청)
  - `1002`: LeaveRoom (방 나가기 요청)

- **data**: 요청 종류에 따라 달라지는 데이터 내용

#### 요청 예시

```json
// Ping 요청
{
  "signal": 1,
  "data": {}
}

// 방 입장 요청
{
  "signal": 1001,
  "data": {}
}

// 방 나가기 요청
{
  "signal": 1002,
  "data": {}
}
```

### 게임 시작 패킷 (ResponseStartGame)

게임이 시작될 때 모든 플레이어에게 전송되는 패킷입니다.

```json
{
  "signal": 1010,
  "data": {
    "playerCount": 4,
    "playerNames": ["Player1234", "Player5678", "Player9012", "Player3456"],
    "myIndex": 0,
    "startingCards": 5
  },
  "code": 200
}
```

#### GameStartData 필드 설명

- **playerCount**: 총 플레이어 수
- **playerNames**: 모든 플레이어의 이름 배열
- **myIndex**: 받는 클라이언트의 플레이어 인덱스 (0부터 시작)
- **startingCards**: 게임 시작 시 각 플레이어가 받는 카드 수

### 사용 예시

```go
// 성공 패킷 생성
packet := NewSuccessResponse(SignalPong, map[string]interface{}{
    "timestamp": time.Now().Unix(),
})

// 에러 패킷 생성 (요청 signal과 동일한 signal 사용, 빈 data)
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
- 에러 응답의 data는 빈 객체이고, code는 400입니다

### 방 관리 시스템

#### 방 입장 (RequestEnterRoom)
- 클라이언트가 방에 입장을 요청합니다
- 이미 방에 있는 경우 에러를 반환합니다
- 방이 꽉 찬 경우 에러를 반환합니다
- 게임이 이미 시작된 경우 에러를 반환합니다
- 성공 시 클라이언트의 방 참여 상태가 업데이트됩니다

#### 방 나가기 (RequestLeaveRoom)
- 클라이언트가 방에서 나가기를 요청합니다
- 방에 참여하지 않은 경우 에러를 반환합니다
- 게임이 이미 시작된 경우 에러를 반환합니다
- 성공 시 클라이언트의 방 참여 상태가 초기화됩니다

#### 게임 시작 (ResponseStartGame)
- 방에 최대 인원(4명)이 들어왔을 때 자동으로 게임이 시작됩니다
- 모든 플레이어에게 게임 시작 패킷이 전송됩니다
- 각 플레이어는 자신의 인덱스와 다른 플레이어들의 정보를 받습니다

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
  "data": {},
  "code": 400
}
```

에러 메시지는 서버 로그에만 기록되고, 클라이언트에게는 빈 data와 code: 400만 전송됩니다.