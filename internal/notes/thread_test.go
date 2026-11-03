package notes

import (
	"strings"
	"testing"
	"time"
)

// mkStore 는 테스트용 인메모리 스토어를 만든다. Save 를 부르지 않으면 디스크를 안 건드린다.
func mkStore(ns ...*Note) *Store {
	return &Store{Root: "/tmp/repo", Path: "/tmp/repo/.mgit/notes.jsonl", Notes: ns}
}

func mkNote(id, parent, status string, min int) *Note {
	return &Note{
		ID:      id,
		Parent:  parent,
		Kind:    KindLine,
		Anchor:  Anchor{Commit: "abc123", Path: "src/Foo.java", Line: 10},
		Body:    "body of " + id,
		Status:  status,
		Created: time.Date(2026, 1, 1, 0, min, 0, 0, time.UTC),
	}
}

func ids(list []*Note) string {
	out := make([]string, 0, len(list))
	for _, n := range list {
		out = append(out, n.ID)
	}
	return strings.Join(out, ",")
}

func TestRootsExcludesReplies(t *testing.T) {
	st := mkStore(
		mkNote("n1", "", StatusOpen, 1),
		mkNote("n2", "n1", StatusOpen, 2),
		mkNote("n3", "", StatusDone, 3),
	)
	if got := ids(st.Roots("all")); got != "n1,n3" {
		t.Fatalf("Roots(all) = %q, want n1,n3", got)
	}
	if got := ids(st.Roots(StatusOpen)); got != "n1" {
		t.Fatalf("Roots(open) = %q, want n1", got)
	}
	// 답글은 상태 필터의 대상이 아니므로 done 필터에도 안 끼어야 한다.
	if got := ids(st.Roots(StatusDone)); got != "n3" {
		t.Fatalf("Roots(done) = %q, want n3", got)
	}
}

func TestSelectThreadsOrder(t *testing.T) {
	st := mkStore(
		mkNote("n1", "", StatusOpen, 1),
		mkNote("n4", "n1", StatusOpen, 4), // 나중에 달린 답글
		mkNote("n2", "n1", StatusOpen, 2), // 먼저 달린 답글
		mkNote("n3", "", StatusOpen, 3),
	)
	// 루트는 생성 순, 답글은 각 루트 바로 뒤에 생성 순으로 붙어야 한다.
	if got := ids(st.SelectThreads("all")); got != "n1,n2,n4,n3" {
		t.Fatalf("SelectThreads = %q, want n1,n2,n4,n3", got)
	}
}

func TestRemoveRootCascadesToReplies(t *testing.T) {
	st := mkStore(
		mkNote("n1", "", StatusOpen, 1),
		mkNote("n2", "n1", StatusOpen, 2),
		mkNote("n3", "", StatusOpen, 3),
	)
	if !st.Remove("n1") {
		t.Fatal("Remove(n1) = false, want true")
	}
	if got := ids(st.Notes); got != "n3" {
		t.Fatalf("삭제 후 남은 메모 = %q, want n3 (답글 n2 도 함께 지워져야 함)", got)
	}
}

func TestRemoveReplyKeepsRoot(t *testing.T) {
	st := mkStore(
		mkNote("n1", "", StatusOpen, 1),
		mkNote("n2", "n1", StatusOpen, 2),
	)
	if !st.Remove("n2") {
		t.Fatal("Remove(n2) = false, want true")
	}
	if got := ids(st.Notes); got != "n1" {
		t.Fatalf("삭제 후 남은 메모 = %q, want n1", got)
	}
}

func TestThreadRootFlattensNestedReply(t *testing.T) {
	st := mkStore(
		mkNote("n1", "", StatusOpen, 1),
		mkNote("n2", "n1", StatusOpen, 2),
		mkNote("n3", "n2", StatusOpen, 3), // 답글의 답글
	)
	if got := st.ThreadRoot(st.Find("n3")); got.ID != "n1" {
		t.Fatalf("ThreadRoot(n3) = %s, want n1", got.ID)
	}
	// 루트를 넣으면 자기 자신이 나와야 한다.
	if got := st.ThreadRoot(st.Find("n1")); got.ID != "n1" {
		t.Fatalf("ThreadRoot(n1) = %s, want n1", got.ID)
	}
}

// 부모가 사라진 답글이 남아 있어도(옛 파일 등) 무한 루프에 빠지지 않아야 한다.
func TestThreadRootOnBrokenParentChain(t *testing.T) {
	orphan := mkNote("n9", "gone", StatusOpen, 1)
	st := mkStore(orphan)
	if got := st.ThreadRoot(orphan); got.ID != "n9" {
		t.Fatalf("ThreadRoot(고아 답글) = %s, want n9", got.ID)
	}
}

func TestNextIDCountsReplies(t *testing.T) {
	st := mkStore(
		mkNote("n1", "", StatusOpen, 1),
		mkNote("n2", "n1", StatusOpen, 2),
	)
	if got := st.NextID(); got != "n3" {
		t.Fatalf("NextID = %s, want n3 (답글도 ID 를 차지한다)", got)
	}
}

func TestExportRendersReplyWithoutRepeatingHeader(t *testing.T) {
	root := mkNote("n1", "", StatusOpen, 1)
	reply := mkNote("n2", "n1", StatusOpen, 2)
	var sb strings.Builder
	// src 가 nil 이면 코드 문맥 없이 본문만 나온다.
	if err := Export(&sb, []*Note{root, reply}, nil, DefaultContext); err != nil {
		t.Fatal(err)
	}
	out := sb.String()
	if !strings.Contains(out, "## [n1]") {
		t.Fatalf("루트 헤더가 없습니다:\n%s", out)
	}
	if strings.Contains(out, "## [n2]") {
		t.Fatalf("답글이 루트처럼 헤더를 달았습니다:\n%s", out)
	}
	if !strings.Contains(out, "↳ [n2] 답글 (→ n1)") {
		t.Fatalf("답글 표기가 없습니다:\n%s", out)
	}
}
