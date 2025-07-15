# halligalli-server

할리갈리 게임 서버입니다.

## 시스템 구조

이 서버는 WebSocket 연결, Ping/Pong 기능, 방 관리, 게임 시작 기능을 제공합니다.

## DB 사용 설정

로컬 테스트를 위해 DB 사용 여부를 제어할 수 있습니다.

### 환경변수 설정

- `USE_DATABASE=true`: DB 사용 (기본값)
- `USE_DATABASE=false`: DB 사용 안함 (로컬 테스트 모드)

### 사용 예시

```bash
# DB 사용 (기본)
go run main.go

# DB 사용 안함 (로컬 테스트)
USE_DATABASE=false go run main.go
```

### 로컬 테스트 모드 특징

- DB 연결 없이 서버 실행 가능
- DB 연결 실패 시에도 서버가 계속 실행됨
- 계정 생성 시 항상 성공으로 처리
- 로그인 시 입력한 ID로 "LocalUser" + ID 형태의 닉네임 자동 생성
- 실제 DB 저장/조회 작업 없이 게임 기능만 테스트 가능

## 게임 설정

게임 관련 설정값들은 `config/game_config.go` 파일에서 관리됩니다.

### 주요 설정값

- **MaxPlayers**: 방에 들어갈 수 있는 최대 플레이어 수 (기본값: 4)
- **BellRingingFruitCount**: 종을 올바르게 치기 위한 과일 개수 (기본값: 5)
- **CardOpenInterval**: 카드 공개 간격 (기본값: 3초)
- **StartingCards**: 게임 시작 시 각 플레이어가 받는 카드 수 (기본값: 5)

설정값을 변경하려면 `config/game_config.go` 파일의 상수값을 수정하면 됩니다.

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
  - `1011`: ReadyGame (게임 준비 완료)
  - `2000`: OpenCard (카드 공개)
  - `2002`: RingBellCorrect (벨 누르기 성공)
  - `2003`: RingBellWrong (벨 누르기 실패)

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
  - `1011`: ReadyGame (게임 준비 완료 요청)
  - `2001`: RingBell (벨 누르기 요청)

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

// 게임 준비 완료 요청
{
  "signal": 1011,
  "data": {}
}

// 벨 누르기 요청
{
  "signal": 2001,
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

### 게임 준비 완료 패킷 (ResponseReadyGame)

모든 플레이어가 준비 완료했을 때 전송되는 패킷입니다.

```json
{
  "signal": 1011,
  "data": {},
  "code": 200
}
```

#### ReadyGame 패킷 설명

- **signal**: 1011 (게임 준비 완료)
- **data**: 빈 객체 (추가 데이터 없음)
- **code**: 200 (성공)

### 카드 공개 패킷 (ResponseOpenCard)

게임이 시작된 후 3초마다 전송되는 카드 공개 패킷입니다.

```json
{
  "signal": 2000,
  "data": {
    "fruitIndex": 1,
    "fruitCount": 3,
    "playerIndex": 2
  },
  "code": 200
}
```

#### OpenCardData 필드 설명

- **fruitIndex**: 과일 종류 (0-2)
  - `0`: 첫 번째 과일
  - `1`: 두 번째 과일  
  - `2`: 세 번째 과일
- **fruitCount**: 과일 개수 (1-5)
- **playerIndex**: 카드를 낸 플레이어 인덱스 (0부터 시작)

#### 카드 공개 시스템

- 게임이 시작되면 3초마다 자동으로 카드가 공개됩니다
- 플레이어들이 순환하면서 카드를 냅니다: `(playerIndex + 1) % totalPlayerCount`
- 과일 종류와 개수는 매번 랜덤하게 결정됩니다

### 벨 누르기 패킷들

#### 벨 누르기 성공 패킷 (ResponseRingBellCorrect)

같은 종류의 과일이 정확히 5개가 공개되어 있을 때 벨을 누르면 전송되는 패킷입니다.

```json
{
  "signal": 2002,
  "data": {
    "playerIndex": 1
  },
  "code": 200
}
```

#### 벨 누르기 실패 패킷 (ResponseRingBellWrong)

같은 종류의 과일이 정확히 5개가 공개되어 있지 않을 때 벨을 누르면 전송되는 패킷입니다.

```json
{
  "signal": 2003,
  "data": {
    "playerIndex": 2
  },
  "code": 200
}
```

#### RingBellData 필드 설명

- **playerIndex**: 벨을 누른 플레이어의 인덱스 (0부터 시작)

#### 벨 누르기 시스템

- 클라이언트가 `RequestRingBell`을 서버에 전송합니다
- 서버는 `IsBellRingingTime()` 함수로 종을 칠 수 있는 타이밍인지 확인합니다
- 같은 종류의 과일이 정확히 5개가 공개되어 있으면 `ResponseRingBellCorrect` 전송
- 그렇지 않으면 `ResponseRingBellWrong` 전송
- 모든 게임 참여 플레이어에게 결과가 전송됩니다

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

#### 게임 준비 완료 (RequestReadyGame / ResponseReadyGame)
- 클라이언트가 `ResponseStartGame`을 받은 후 씬 이동 등의 로직을 완료하면 `RequestReadyGame`을 서버에 전송합니다
- 서버는 모든 플레이어가 준비 완료했을 때 `ResponseReadyGame`을 모든 클라이언트에게 전송합니다
- 실제 게임은 `ResponseReadyGame`을 받은 후에 시작됩니다

#### 카드 공개 (ResponseOpenCard)
- 게임이 시작되면 3초마다 자동으로 카드가 공개됩니다
- 플레이어들이 순환하면서 카드를 냅니다: `(playerIndex + 1) % totalPlayerCount`
- 과일 종류(0-2)와 개수(1-5)는 매번 랜덤하게 결정됩니다
- 모든 클라이언트에게 동일한 카드 공개 정보가 전송됩니다

#### 벨 누르기 (RequestRingBell / ResponseRingBellCorrect / ResponseRingBellWrong)
- 클라이언트가 벨을 누르면 `RequestRingBell`을 서버에 전송합니다
- 서버는 현재 공개된 카드들을 확인하여 같은 종류의 과일이 정확히 5개인지 판단합니다
- 정확히 5개이면 `ResponseRingBellCorrect`, 그렇지 않으면 `ResponseRingBellWrong`을 모든 플레이어에게 전송합니다
- 모든 게임 참여 플레이어에게 결과가 전송됩니다

#### 플레이어 연결 해제 처리
- **게임 시작 전 연결 해제**: `RequestLeaveRoom`과 동일하게 처리 (플레이어를 방에서 제거)
- **게임 진행 중 연결 해제**: 플레이어를 방에서 제거하지 않고, 해당 플레이어에게만 패킷 전송을 중단
- 연결이 끊어진 플레이어는 `OpenCard` 등의 패킷을 받지 않습니다
- **모든 플레이어 연결 해제**: 모든 플레이어가 연결을 끊으면 즉시 게임이 종료되고 방이 초기화됩니다

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