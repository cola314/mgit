# mgit

로컬 git 뷰어 + 에이전트에게 먹이는 코드 메모.

읽기 전용이다. 커밋 그래프와 diff 를 보고, 코드 줄에 메모를 남기면, 그 메모가
AI 에이전트가 바로 읽을 수 있는 형태로 나온다. 커밋·머지·푸시 같은 쓰기 작업은
하지 않는다 — 그건 이미 잘 하는 도구가 많다.

```
사람: 뷰어에서 diff 보다가 줄에 메모 투척        →  status: open
에이전트: mgit note export 로 읽고 고친 뒤       →  mgit note done n1 -m "..."
사람: 열어둔 화면에서 done 으로 바뀐 걸 확인
```

## 설치

```
go build -o mgit.exe ./cmd/mgit
```

의존성은 Go 표준 라이브러리와 `git` 바이너리뿐이다.

## 쓰기

```bash
mgit                              # 현재 저장소 뷰어 열기
mgit path/to/repo                 # 특정 저장소
mgit . -c caf6ab321               # 그 커밋의 상세 화면으로 바로
mgit . -c HEAD~3                  # git 리비전 문법 그대로

mgit note add Foo.java:120 -m "여기 null 체크 빠진 듯"
mgit note add -c HEAD -m "이 커밋 통째로 재검토"     # 커밋 단위 메모
mgit note list [--status open|done|all] [--json]
mgit note show n1
mgit note done n1 -m "처리 내용"
mgit note reopen n1
mgit note rm n1
mgit note export [--status open|all]                # 프롬프트에 붙일 마크다운
```

## 설계에서 갈린 지점

**git 은 CLI 를 셸아웃한다.** go-git 이나 libgit2 대신 `git` 바이너리를 그대로
부른다. credential helper, SSH config, LFS, submodule, sparse-checkout 을 전부 git 이
대신 처리해 준다. 리비전 문법(`HEAD~3`, 태그, 짧은 SHA, `@{u}`)도 `rev-parse` 에
위임해 공짜로 얻는다.

**UI 는 루프백 HTTP + 브라우저다.** Wails 같은 네이티브 셸을 쓰지 않는다.
표준 라이브러리만으로 끝나서 빌드 체인이 깨질 여지가 없고, API 전체를 `httptest`
로 자동 검증할 수 있다. 한글 IME 도 진짜 브라우저라 그냥 동작한다.
단일 인스턴스 IPC 도 같은 포트로 해결된다. 나중에 백엔드를 안 건드리고
네이티브 껍데기만 씌울 수 있다.

**메모는 평문 JSONL 이다.** git notes 를 쓰지 않는다. `refs/notes` 는 객체에만
붙어서 줄 단위 앵커링이 억지스럽고, fetch/merge 충돌 해결이 고통스럽다.
무엇보다 에이전트가 읽기 불편하다. `<저장소>/.mgit/notes.jsonl` 한 줄에 메모 하나면
CLI 없이 파일만 읽어도 된다. `.gitignore` 대신 `.git/info/exclude` 에 등록해
공유 파일을 건드리지 않는다.

**앵커는 고정하고 이동은 따로 추적한다.** 메모는 `(커밋, 경로, 줄)` 에 못박혀서
이후 코드가 아무리 바뀌어도 원래 문맥을 항상 복원할 수 있다. 동시에 앵커 줄
위아래를 `fingerprint` 로 떠 두고, 현재 HEAD 에서 그 코드가 어디로 갔는지
찾아 `export` 에 함께 표시한다. 에이전트가 옛 줄 번호로 헤매지 않도록.
`git blame` 추적보다 훨씬 싸면서 실제 케이스 대부분을 잡는다.

**머지 diff 는 첫 부모 기준이다.** git 기본값인 결합 diff(combined diff)는 줄 번호가
모호해 메모 앵커로 쓸 수 없다.

## 구조

```
cmd/mgit/           CLI 진입점 (note 서브커맨드 + 뷰어 실행)
internal/gitx/      git CLI 래퍼 — log/diff 파싱, 리비전 해석
internal/graph/     커밋 DAG → 세로 레인 배치 (git·UI 무관 순수 계산)
internal/notes/     메모 자료구조, JSONL 저장소, 마크다운 export
internal/single/    저장소당 인스턴스 하나 보장 + 딥링크 전달
internal/server/    JSON API + 임베드된 UI
```

### 레인 배치 규칙

`internal/graph` 는 위상 정렬된 커밋 목록을 받아 세 규칙으로 레인을 정한다.

- 나를 기다리는 레인이 있으면 그 자리에 앉는다. 없으면 빈 레인을 찾는다.
- 첫 부모는 내 레인을 물려받는다 → 주 계보가 곧게 내려간다.
- 나머지 부모는 새 레인을 예약한다 → 머지가 옆으로 갈라져 보인다.

여기에 하나가 더 붙는다. **첫 부모는 더 왼쪽 레인이 우선권을 갖는다.** 사이드
브랜치가 공통 부모를 먼저 예약해 버리면 트렁크가 오른쪽으로 밀려 그래프가
계단처럼 어긋나기 때문이다.

### 단일 인스턴스

첫 실행이 루프백 리스너를 열고 주소를 사용자 캐시 디렉토리에 적어둔다.
이후 실행은 그 주소로 `POST /api/goto` 만 보내고 바로 종료하므로 창이 늘어나지 않는다.
죽은 프로세스가 남긴 낡은 파일, 깨진 파일, 그 포트를 다른 프로그램이 물고 있는
경우는 모두 회수하고 새로 잡는다.

## 테스트

```
go test ./...
```

`internal/gitx` 와 `internal/server` 는 임시 디렉토리에 실제 저장소를 만들어
돌리므로 `git` 이 PATH 에 있어야 하고 다른 패키지보다 느리다.

## 아직 없는 것

- 검색 (커밋 메시지·작성자·경로)
- 파일 이력 / blame 뷰
- `mgit://` URL 스킴 핸들러
- 메모 이동 추적의 `git blame` 폴백 (지금은 지문 매칭만)
