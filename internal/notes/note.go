// Package notes 는 로컬 전용 코드 메모의 자료구조와 저장소를 담당한다.
//
// 메모는 레포 안 .mgit/notes.jsonl 에 한 줄 하나씩 쌓인다. 에이전트가 CLI 없이
// 파일만 읽어도 되도록 일부러 평문 JSONL 을 단일 진실 공급원으로 쓴다.
package notes

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Kind 는 메모가 무엇에 붙었는지를 나타낸다.
type Kind string

const (
	KindLine   Kind = "line"   // 특정 커밋의 특정 파일 특정 줄
	KindCommit Kind = "commit" // 커밋 전체
)

// 메모 처리 상태.
const (
	StatusOpen = "open"
	StatusDone = "done"
)

// Anchor 는 메모가 가리키는 불변 위치다.
//
// 커밋 SHA 를 함께 박아두기 때문에 이후 코드가 아무리 바뀌어도 원래 문맥은
// 항상 복원할 수 있다. 현재 워킹트리의 어디에 해당하는지는 Fingerprint 로 따로 추적한다.
type Anchor struct {
	Commit string `json:"commit"`
	Path   string `json:"path,omitempty"`
	Line   int    `json:"line,omitempty"`
	// EndLine 은 여러 줄에 걸친 메모의 끝 줄이다. 0 이거나 Line 이하면 단일 줄 메모다.
	//
	// 필드가 없는 옛 메모는 자연히 단일 줄로 읽힌다. 지문은 시작 줄에만 걸고 끝은
	// 델타로 보존한다 — 양 끝에 지문을 걸면 둘이 서로 다른 방향으로 밀렸을 때
	// 범위가 뒤집히는 경우를 전부 처리해야 하는데, 얻는 정확도에 비해 비싸다.
	EndLine int `json:"endLine,omitempty"`
}

// Note 는 메모 하나다.
type Note struct {
	ID     string `json:"id"`
	Kind   Kind   `json:"kind"`
	Anchor Anchor `json:"anchor"`
	// Fingerprint 는 앵커 줄과 위아래 한 줄씩의 원문이다. 코드가 이동했을 때
	// 현재 파일에서 같은 위치를 다시 찾는 데 쓴다.
	Fingerprint []string `json:"fingerprint,omitempty"`
	// Parent 는 이 메모가 답글일 때 부모 메모의 ID 다. 비어 있으면 루트 메모.
	//
	// 필드가 없는 옛 메모는 자연히 루트로 읽히므로 기존 notes.jsonl 은 그대로 쓸 수 있다.
	// 답글은 부모의 Kind·Anchor·Fingerprint 를 복사해 갖는다. 그래야 줄 단위 조회나
	// 코드 문맥 추출 같은 기존 경로가 답글에도 손대지 않고 그대로 동작한다.
	Parent     string     `json:"parent,omitempty"`
	Body       string     `json:"body"`
	Status     string     `json:"status"`
	Resolution string     `json:"resolution,omitempty"`
	Created    time.Time  `json:"created"`
	Updated    *time.Time `json:"updated,omitempty"`
}

// IsReply 는 이 메모가 다른 메모에 달린 답글인지 알려준다.
func (n *Note) IsReply() bool { return n.Parent != "" }

// Target 은 사람이 읽는 위치 표기다. (예: Foo.java:18, Foo.java:18-32)
func (n *Note) Target() string {
	if n.Kind == KindCommit {
		return "커밋 전체"
	}
	if n.IsRange() {
		return fmt.Sprintf("%s:%d-%d", n.Anchor.Path, n.Anchor.Line, n.Anchor.EndLine)
	}
	return fmt.Sprintf("%s:%d", n.Anchor.Path, n.Anchor.Line)
}

// IsRange 는 메모가 여러 줄에 걸쳐 있는지 알려준다.
func (n *Note) IsRange() bool { return n.Anchor.EndLine > n.Anchor.Line }

// LastLine 은 메모가 덮는 마지막 줄이다. 단일 줄이면 시작 줄과 같다.
func (n *Note) LastLine() int {
	if n.IsRange() {
		return n.Anchor.EndLine
	}
	return n.Anchor.Line
}

// LocateRange 는 현재 파일에서 이 메모가 덮는 범위를 찾는다.
//
// 시작 줄은 Locate 로 추적하고, 끝은 원래 범위 길이를 그대로 얹어 구한다.
func (n *Note) LocateRange(cur []string) (start, end int, d Drift) {
	start, d = n.Locate(cur)
	end = start + (n.LastLine() - n.Anchor.Line)
	if len(cur) > 0 && end > len(cur) {
		end = len(cur)
	}
	if end < start {
		end = start
	}
	return start, end, d
}

// Base 는 경로의 파일명만 돌려준다.
func (n *Note) Base() string {
	if n.Anchor.Path == "" {
		return ""
	}
	if i := strings.LastIndex(n.Anchor.Path, "/"); i >= 0 {
		return n.Anchor.Path[i+1:]
	}
	return n.Anchor.Path
}

// Drift 는 메모가 현재 파일에서 어디에 놓이는지에 대한 판정이다.
type Drift int

const (
	DriftExact Drift = iota // 앵커 줄 그대로
	DriftMoved              // 다른 줄로 이동함
	DriftLost               // 현재 파일에서 못 찾음
)

func (d Drift) String() string {
	switch d {
	case DriftExact:
		return "exact"
	case DriftMoved:
		return "moved"
	default:
		return "lost"
	}
}

// Locate 는 현재 파일 내용에서 이 메모가 가리키는 줄 번호를 찾는다.
//
// 먼저 원래 줄을 그대로 확인하고(대부분의 경우), 어긋났으면 지문 3줄이 연속으로
// 나타나는 곳을 찾고, 그래도 없으면 중심 줄만 유일하게 나타나는 곳을 찾는다.
// git blame 추적보다 훨씬 싸면서 실제 케이스 대부분을 잡는다.
func (n *Note) Locate(cur []string) (line int, d Drift) {
	if n.Kind != KindLine || len(cur) == 0 {
		return n.Anchor.Line, DriftLost
	}
	// 지문 안에서 앵커 줄이 놓인 위치. MakeFingerprint 가 앞 한 줄을 같이 뜨므로
	// 보통 1이지만, 파일 첫 줄에 달린 메모는 앞 줄이 없어 0이다.
	centerIdx := 1
	if n.Anchor.Line <= 1 {
		centerIdx = 0
	}
	center := ""
	if centerIdx < len(n.Fingerprint) {
		center = norm(n.Fingerprint[centerIdx])
	}
	if center == "" {
		// 지문이 없는 옛 메모는 앵커를 그대로 믿는다.
		if n.Anchor.Line >= 1 && n.Anchor.Line <= len(cur) {
			return n.Anchor.Line, DriftExact
		}
		return n.Anchor.Line, DriftLost
	}

	if i := n.Anchor.Line - 1; i >= 0 && i < len(cur) && norm(cur[i]) == center {
		return n.Anchor.Line, DriftExact
	}

	// 지문 전체가 연속으로 맞는 자리를 찾는다.
	fp := make([]string, 0, len(n.Fingerprint))
	for _, s := range n.Fingerprint {
		fp = append(fp, norm(s))
	}
	if at := findWindow(cur, fp); at > 0 {
		return at + centerIdx, DriftMoved
	}

	// 마지막 수단: 중심 줄이 딱 한 번만 나타나면 그 자리로 본다.
	found, count := 0, 0
	for i, s := range cur {
		if norm(s) == center {
			found, count = i+1, count+1
			if count > 1 {
				break
			}
		}
	}
	if count == 1 {
		return found, DriftMoved
	}
	return n.Anchor.Line, DriftLost
}

// findWindow 는 hay 안에서 needle 이 연속으로 나타나는 첫 시작 줄(1-base)을 찾는다.
// 못 찾으면 0.
func findWindow(hay []string, needle []string) int {
	if len(needle) == 0 || len(needle) > len(hay) {
		return 0
	}
	for i := 0; i+len(needle) <= len(hay); i++ {
		ok := true
		for j, want := range needle {
			if norm(hay[i+j]) != want {
				ok = false
				break
			}
		}
		if ok {
			return i + 1
		}
	}
	return 0
}

// norm 은 들여쓰기 변경에 흔들리지 않도록 좌우 공백을 없앤다.
func norm(s string) string { return strings.TrimSpace(s) }

// ParseTarget 은 "경로:줄번호" 또는 "경로:시작-끝" 형태의 인자를 쪼갠다.
//
// Windows 드라이브 문자(C:\...)를 줄 번호로 오해하지 않도록 마지막 콜론만 본다.
// 단일 줄이면 end 는 start 와 같게 돌려준다.
func ParseTarget(arg string) (path string, line, end int, err error) {
	i := strings.LastIndex(arg, ":")
	if i < 0 {
		return "", 0, 0, fmt.Errorf("위치는 경로:줄번호 형식이어야 합니다 (예: src/Foo.java:120 또는 src/Foo.java:120-135)")
	}
	path, numStr := arg[:i], arg[i+1:]
	if strings.TrimSpace(path) == "" {
		return "", 0, 0, fmt.Errorf("경로가 비어 있습니다")
	}

	startStr, endStr := numStr, ""
	if j := strings.Index(numStr, "-"); j >= 0 {
		startStr, endStr = numStr[:j], numStr[j+1:]
	}

	line, err = strconv.Atoi(startStr)
	if err != nil || line < 1 {
		return "", 0, 0, fmt.Errorf("줄 번호가 올바르지 않습니다: %q", startStr)
	}
	if endStr == "" {
		return path, line, line, nil
	}
	end, err = strconv.Atoi(endStr)
	if err != nil || end < 1 {
		return "", 0, 0, fmt.Errorf("끝 줄 번호가 올바르지 않습니다: %q", endStr)
	}
	if end < line {
		return "", 0, 0, fmt.Errorf("끝 줄(%d)이 시작 줄(%d)보다 앞입니다", end, line)
	}
	return path, line, end, nil
}

// MakeFingerprint 는 앵커 줄을 중심으로 위아래 한 줄씩을 지문으로 뜬다.
func MakeFingerprint(lines []string, line int) []string {
	if line < 1 || line > len(lines) {
		return nil
	}
	lo := line - 2
	if lo < 0 {
		lo = 0
	}
	hi := line + 1
	if hi > len(lines) {
		hi = len(lines)
	}
	out := make([]string, 0, hi-lo)
	for _, s := range lines[lo:hi] {
		out = append(out, strings.TrimRight(s, " \t"))
	}
	return out
}
