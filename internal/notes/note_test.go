package notes

import (
	"strings"
	"testing"
)

func lines(s string) []string { return strings.Split(strings.TrimPrefix(s, "\n"), "\n") }

const sample = `
package foo

import "time"

func Handle(id int) string {
	u := repo.Find(id)
	return u.Name()
}
`

func TestParseTarget(t *testing.T) {
	tests := []struct {
		in      string
		path    string
		line    int
		end     int
		wantErr bool
	}{
		{in: "src/Foo.java:120", path: "src/Foo.java", line: 120, end: 120},
		{in: "Foo.java:1", path: "Foo.java", line: 1, end: 1},
		// Windows 드라이브 문자를 줄 번호로 오해하면 안 된다.
		{in: `C:\work\repo\Foo.java:42`, path: `C:\work\repo\Foo.java`, line: 42, end: 42},
		// 범위 지정.
		{in: "src/Foo.java:120-135", path: "src/Foo.java", line: 120, end: 135},
		{in: "src/Foo.java:7-7", path: "src/Foo.java", line: 7, end: 7},
		{in: `C:\work\repo\Foo.java:10-20`, path: `C:\work\repo\Foo.java`, line: 10, end: 20},
		{in: "src/Foo.java", wantErr: true},
		{in: "src/Foo.java:0", wantErr: true},
		{in: "src/Foo.java:abc", wantErr: true},
		{in: ":12", wantErr: true},
		// 끝이 시작보다 앞이면 오류.
		{in: "src/Foo.java:30-10", wantErr: true},
		{in: "src/Foo.java:10-abc", wantErr: true},
		{in: "src/Foo.java:10-0", wantErr: true},
	}
	for _, tt := range tests {
		path, line, end, err := ParseTarget(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("ParseTarget(%q): 오류를 기대했지만 %s:%d-%d 를 얻음", tt.in, path, line, end)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseTarget(%q): 예상치 못한 오류 %v", tt.in, err)
			continue
		}
		if path != tt.path || line != tt.line || end != tt.end {
			t.Errorf("ParseTarget(%q) = %s:%d-%d, want %s:%d-%d", tt.in, path, line, end, tt.path, tt.line, tt.end)
		}
	}
}

func TestMakeFingerprint(t *testing.T) {
	src := lines(sample)

	// 가운데 줄: 앞뒤 한 줄씩 총 3줄.
	fp := MakeFingerprint(src, 6)
	if len(fp) != 3 {
		t.Fatalf("가운데 줄 지문 길이 = %d, want 3 (%q)", len(fp), fp)
	}
	if strings.TrimSpace(fp[1]) != "u := repo.Find(id)" {
		t.Errorf("지문 중심 = %q, want %q", fp[1], "u := repo.Find(id)")
	}

	// 첫 줄: 앞 줄이 없으므로 2줄이고 중심이 인덱스 0 이다.
	fp = MakeFingerprint(src, 1)
	if len(fp) != 2 {
		t.Fatalf("첫 줄 지문 길이 = %d, want 2 (%q)", len(fp), fp)
	}
	if fp[0] != "package foo" {
		t.Errorf("첫 줄 지문[0] = %q, want %q", fp[0], "package foo")
	}

	if got := MakeFingerprint(src, 999); got != nil {
		t.Errorf("범위 밖 줄 지문 = %q, want nil", got)
	}
}

func TestLocateExact(t *testing.T) {
	src := lines(sample)
	n := &Note{Kind: KindLine, Anchor: Anchor{Line: 6}, Fingerprint: MakeFingerprint(src, 6)}

	at, d := n.Locate(src)
	if d != DriftExact || at != 6 {
		t.Errorf("변경 없는 파일 Locate = %d/%s, want 6/exact", at, d)
	}
}

func TestLocateMovedByInsertion(t *testing.T) {
	src := lines(sample)
	n := &Note{Kind: KindLine, Anchor: Anchor{Line: 6}, Fingerprint: MakeFingerprint(src, 6)}

	// 위쪽에 import 두 줄이 끼어들어 코드가 아래로 밀린 상황.
	moved := append([]string{}, src[:3]...)
	moved = append(moved, `import "fmt"`, `import "os"`)
	moved = append(moved, src[3:]...)

	at, d := n.Locate(moved)
	if d != DriftMoved {
		t.Fatalf("삽입 후 Locate 판정 = %s, want moved", d)
	}
	if at != 8 {
		t.Errorf("삽입 후 줄 번호 = %d, want 8", at)
	}
	if strings.TrimSpace(moved[at-1]) != "u := repo.Find(id)" {
		t.Errorf("이동 위치가 엉뚱함: %q", moved[at-1])
	}
}

func TestLocateSurvivesReindent(t *testing.T) {
	src := lines(sample)
	n := &Note{Kind: KindLine, Anchor: Anchor{Line: 6}, Fingerprint: MakeFingerprint(src, 6)}

	// 들여쓰기만 탭에서 스페이스로 바뀐 경우는 그대로 찾아야 한다.
	reindented := append([]string{}, src...)
	for i, s := range reindented {
		reindented[i] = strings.ReplaceAll(s, "\t", "    ")
	}
	at, d := n.Locate(reindented)
	if d != DriftExact || at != 6 {
		t.Errorf("들여쓰기 변경 후 Locate = %d/%s, want 6/exact", at, d)
	}
}

func TestLocateLost(t *testing.T) {
	src := lines(sample)
	n := &Note{Kind: KindLine, Anchor: Anchor{Line: 6}, Fingerprint: MakeFingerprint(src, 6)}

	gone := lines("\npackage foo\n\nfunc Other() {}\n")
	at, d := n.Locate(gone)
	if d != DriftLost {
		t.Errorf("사라진 코드 Locate 판정 = %s (줄 %d), want lost", d, at)
	}
}

func TestLocateFirstLineAnchor(t *testing.T) {
	src := lines(sample)
	n := &Note{Kind: KindLine, Anchor: Anchor{Line: 1}, Fingerprint: MakeFingerprint(src, 1)}

	// 첫 줄 앵커는 지문 중심 인덱스가 0 이라 별도 경로를 탄다.
	at, d := n.Locate(src)
	if d != DriftExact || at != 1 {
		t.Errorf("첫 줄 앵커 Locate = %d/%s, want 1/exact", at, d)
	}

	moved := append([]string{"// 저작권 헤더", ""}, src...)
	at, d = n.Locate(moved)
	if d != DriftMoved || at != 3 {
		t.Errorf("첫 줄 앵커 이동 후 = %d/%s, want 3/moved", at, d)
	}
}

func TestTargetAndBase(t *testing.T) {
	line := &Note{Kind: KindLine, Anchor: Anchor{Path: "src/main/java/Foo.java", Line: 18}}
	if got, want := line.Target(), "src/main/java/Foo.java:18"; got != want {
		t.Errorf("Target() = %q, want %q", got, want)
	}
	if got, want := line.Base(), "Foo.java"; got != want {
		t.Errorf("Base() = %q, want %q", got, want)
	}

	commit := &Note{Kind: KindCommit, Anchor: Anchor{Commit: "abc"}}
	if got, want := commit.Target(), "커밋 전체"; got != want {
		t.Errorf("커밋 메모 Target() = %q, want %q", got, want)
	}
}
