package notes

import (
	"strings"
	"testing"
)

func rangeNote(start, end int) *Note {
	src := lines(sample)
	n := &Note{
		Kind:        KindLine,
		Anchor:      Anchor{Commit: "abc123", Path: "src/Foo.java", Line: start},
		Fingerprint: MakeFingerprint(src, start),
		Body:        "범위 메모",
		Status:      StatusOpen,
	}
	if end > start {
		n.Anchor.EndLine = end
	}
	return n
}

func TestIsRangeAndLastLine(t *testing.T) {
	single := rangeNote(6, 6)
	if single.IsRange() {
		t.Error("단일 줄인데 IsRange() = true")
	}
	if got := single.LastLine(); got != 6 {
		t.Errorf("LastLine() = %d, want 6", got)
	}

	multi := rangeNote(6, 8)
	if !multi.IsRange() {
		t.Error("범위인데 IsRange() = false")
	}
	if got := multi.LastLine(); got != 8 {
		t.Errorf("LastLine() = %d, want 8", got)
	}
}

func TestTargetShowsRange(t *testing.T) {
	if got := rangeNote(6, 8).Target(); got != "src/Foo.java:6-8" {
		t.Errorf("Target() = %q, want src/Foo.java:6-8", got)
	}
	if got := rangeNote(6, 6).Target(); got != "src/Foo.java:6" {
		t.Errorf("Target() = %q, want src/Foo.java:6", got)
	}
}

// 코드가 위로 밀려도 범위 길이는 유지돼야 한다.
func TestLocateRangeKeepsSpanWhenMoved(t *testing.T) {
	n := rangeNote(6, 8) // func Handle ~ return
	moved := lines(`
// 새로 추가된 주석
// 한 줄 더

package foo

import "time"

func Handle(id int) string {
	u := repo.Find(id)
	return u.Name()
}
`)
	start, end, d := n.LocateRange(moved)
	if d != DriftMoved {
		t.Fatalf("drift = %v, want moved", d)
	}
	if end-start != 2 {
		t.Errorf("범위 길이가 바뀌었습니다: %d-%d (want 길이 3줄)", start, end)
	}
	// sample 의 6번 줄은 u := repo.Find(id) 다. 주석 3줄이 앞에 붙었으니 9번으로 밀린다.
	if !strings.Contains(moved[start-1], "u := repo.Find") {
		t.Errorf("시작 줄이 어긋났습니다: %q", moved[start-1])
	}
	if !strings.Contains(moved[end-1], "}") {
		t.Errorf("끝 줄이 어긋났습니다: %q", moved[end-1])
	}
}

// 파일 끝을 넘어가면 파일 길이에서 잘려야 한다.
func TestLocateRangeClampsToFileEnd(t *testing.T) {
	n := rangeNote(6, 900)
	cur := lines(sample)
	start, end, _ := n.LocateRange(cur)
	if end > len(cur) {
		t.Errorf("end = %d 인데 파일은 %d줄뿐입니다", end, len(cur))
	}
	if end < start {
		t.Errorf("end(%d) < start(%d)", end, start)
	}
}

// 옛 메모(EndLine 없음)는 단일 줄로 읽혀야 한다.
func TestLocateRangeOnLegacySingleLineNote(t *testing.T) {
	n := rangeNote(6, 6)
	cur := lines(sample)
	start, end, _ := n.LocateRange(cur)
	if start != end {
		t.Errorf("단일 줄 메모인데 범위로 나왔습니다: %d-%d", start, end)
	}
}

type stubSource struct{ src []string }

func (s stubSource) FileLines(commit, path string) ([]string, error) { return s.src, nil }
func (s stubSource) Short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func TestExportMarksRangeBounds(t *testing.T) {
	n := rangeNote(6, 8)
	var sb strings.Builder
	if err := Export(&sb, []*Note{n}, stubSource{lines(sample)}, 2); err != nil {
		t.Fatal(err)
	}
	out := sb.String()
	if !strings.Contains(out, "← 메모 범위 시작") {
		t.Errorf("범위 시작 표시가 없습니다:\n%s", out)
	}
	if !strings.Contains(out, "← 메모 범위 끝") {
		t.Errorf("범위 끝 표시가 없습니다:\n%s", out)
	}
	// 범위 안의 모든 줄이 나와야 한다.
	for _, want := range []string{"func Handle", "u := repo.Find", "return u.Name()"} {
		if !strings.Contains(out, want) {
			t.Errorf("범위 안 %q 가 출력에 없습니다:\n%s", want, out)
		}
	}
}

func TestExportSingleLineKeepsOldMarker(t *testing.T) {
	n := rangeNote(6, 6)
	var sb strings.Builder
	if err := Export(&sb, []*Note{n}, stubSource{lines(sample)}, 2); err != nil {
		t.Fatal(err)
	}
	out := sb.String()
	if !strings.Contains(out, "← 메모 위치") {
		t.Errorf("단일 줄 표시가 바뀌었습니다:\n%s", out)
	}
	if strings.Contains(out, "메모 범위") {
		t.Errorf("단일 줄인데 범위 표시가 붙었습니다:\n%s", out)
	}
}
