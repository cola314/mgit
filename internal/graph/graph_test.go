package graph

import "testing"

// chain 은 a→b→c 형태의 선형 이력을 만든다. 목록은 자식이 먼저(위상 정렬).
func chain(shas ...string) []Item {
	items := make([]Item, 0, len(shas))
	for i, s := range shas {
		it := Item{SHA: s}
		if i+1 < len(shas) {
			it.Parents = []string{shas[i+1]}
		}
		items = append(items, it)
	}
	return items
}

func TestLinearHistoryStaysInOneLane(t *testing.T) {
	l := Build(chain("c", "b", "a"))

	if l.Width != 1 {
		t.Errorf("선형 이력 레인 수 = %d, want 1", l.Width)
	}
	for _, n := range l.Nodes {
		if n.Lane != 0 {
			t.Errorf("%s 레인 = %d, want 0", n.SHA, n.Lane)
		}
	}
	if got := len(l.Nodes); got != 3 {
		t.Errorf("노드 수 = %d, want 3", got)
	}
}

func TestRowsFollowInputOrder(t *testing.T) {
	l := Build(chain("c", "b", "a"))
	for i, n := range l.Nodes {
		if n.Row != i {
			t.Errorf("%s row = %d, want %d", n.SHA, n.Row, i)
		}
	}
}

// 기능 브랜치가 갈라졌다 합쳐지는 가장 흔한 모양.
//
//	m   (merge, parents: t, f)
//	|\
//	t | (주 계보)
//	| f (브랜치 팁)
//	|/
//	b   (분기점)
func TestMergeAllocatesSecondLane(t *testing.T) {
	items := []Item{
		{SHA: "m", Parents: []string{"t", "f"}},
		{SHA: "t", Parents: []string{"b"}},
		{SHA: "f", Parents: []string{"b"}},
		{SHA: "b"},
	}
	l := Build(items)

	if l.Width != 2 {
		t.Fatalf("레인 수 = %d, want 2", l.Width)
	}
	if got := l.LaneOf("m"); got != 0 {
		t.Errorf("머지 커밋 레인 = %d, want 0", got)
	}
	if got := l.LaneOf("t"); got != 0 {
		t.Errorf("첫 부모 레인 = %d, want 0 (주 계보는 곧게 내려가야 한다)", got)
	}
	if got := l.LaneOf("f"); got != 1 {
		t.Errorf("두 번째 부모 레인 = %d, want 1", got)
	}
	if got := l.LaneOf("b"); got != 0 {
		t.Errorf("분기점 레인 = %d, want 0 (브랜치가 다시 합류)", got)
	}
}

// 브랜치가 끝나면(합류선이 부모에 닿은 뒤) 그 레인은 다시 쓰여야 한다.
// 안 그러면 긴 이력에서 레인이 무한정 늘어난다.
func TestLaneIsReusedAfterBranchEnds(t *testing.T) {
	items := []Item{
		{SHA: "m1", Parents: []string{"a2", "f1"}},
		{SHA: "f1", Parents: []string{"a2"}}, // f1 은 a2 에서 합류 완료
		{SHA: "a2", Parents: []string{"a3"}},
		{SHA: "m2", Parents: []string{"a3", "f2"}}, // 합류가 끝난 뒤의 머지
		{SHA: "f2", Parents: []string{"a3"}},
		{SHA: "a3"},
	}
	l := Build(items)

	if l.Width > 3 {
		t.Errorf("레인 수 = %d, 3 이하를 기대 (레인 재사용 실패)", l.Width)
	}
	// f1 의 합류선(레인 1)이 a2 행에서 끝났으므로 m2 는 레인 1을 재사용해야 한다.
	if got := l.LaneOf("m2"); got != 1 {
		t.Errorf("m2 레인 = %d, want 1 (합류 완료된 레인 재사용)", got)
	}
	// 모든 커밋이 배치돼야 한다.
	if len(l.Nodes) != len(items) {
		t.Fatalf("노드 수 = %d, want %d", len(l.Nodes), len(items))
	}
}

// 왼쪽으로 합류하는 긴 선이 아직 레인을 타고 내려가는 동안에는 그 레인을
// 다른 브랜치가 재사용하면 안 된다. 점이 남의 선 위에 찍혀 한 계보처럼 보인다.
// (릴리스 브랜치 머지 직후 무관한 hotfix 커밋이 다른 브랜치의
// 합류선 위에 그려졌던 실제 사례)
//
//	m    (merge, parents: t, f)
//	f    (브랜치 팁 → t 로 합류, 선이 레인 1을 타고 t 까지 내려간다)
//	x1   (무관한 브랜치)
//	x2   (무관한 브랜치, 부모는 범위 밖)
//	t    (본선)
func TestLaneBlockedWhileJoinEdgePassesThrough(t *testing.T) {
	items := []Item{
		{SHA: "m", Parents: []string{"t", "f"}},
		{SHA: "f", Parents: []string{"t"}},
		{SHA: "x1", Parents: []string{"x2"}},
		{SHA: "x2", Parents: []string{"gone"}},
		{SHA: "t"},
	}
	l := Build(items)

	fLane := l.LaneOf("f")
	if got := l.LaneOf("x1"); got == fLane {
		t.Errorf("x1 이 합류선이 지나가는 레인 %d 를 재사용했다", fLane)
	}
	if got := l.LaneOf("x2"); got == fLane {
		t.Errorf("x2 가 합류선이 지나가는 레인 %d 를 재사용했다", fLane)
	}
}

// 화면 바닥까지 흘러나가는 dangling 선의 레인도 재사용 금지.
func TestLaneBlockedByDanglingEdge(t *testing.T) {
	items := []Item{
		{SHA: "m", Parents: []string{"t", "f"}},
		{SHA: "f", Parents: []string{"gone"}}, // 범위 밖 → 레인 1을 타고 바닥까지
		{SHA: "x", Parents: []string{"t"}},
		{SHA: "t"},
	}
	l := Build(items)

	if got, f := l.LaneOf("x"), l.LaneOf("f"); got == f {
		t.Errorf("x 가 dangling 선이 지나가는 레인 %d 를 재사용했다", f)
	}
}

// 조회 범위 밖 부모는 화면 아래로 흘러나가는 선으로 표시된다.
func TestDanglingParentEdge(t *testing.T) {
	items := []Item{
		{SHA: "c", Parents: []string{"unknown"}},
	}
	l := Build(items)

	if len(l.Edges) != 1 {
		t.Fatalf("엣지 수 = %d, want 1", len(l.Edges))
	}
	e := l.Edges[0]
	if !e.Dangling {
		t.Error("범위 밖 부모 엣지가 Dangling 이어야 한다")
	}
	if e.ToRow != len(items) {
		t.Errorf("Dangling ToRow = %d, want %d (마지막 행 다음)", e.ToRow, len(items))
	}
	if e.ToLane != e.FromLane {
		t.Errorf("Dangling 은 같은 레인으로 내려가야 한다: from %d to %d", e.FromLane, e.ToLane)
	}
}

// 여러 브랜치가 같은 커밋에서 갈라져 나온 부채꼴 모양.
// 기능 브랜치를 하나씩 따서 순서대로 머지하는 저장소에서 흔히 나온다.
func TestFanOutFanIn(t *testing.T) {
	items := []Item{
		{SHA: "m3", Parents: []string{"m2", "f3"}},
		{SHA: "f3", Parents: []string{"base"}},
		{SHA: "m2", Parents: []string{"m1", "f2"}},
		{SHA: "f2", Parents: []string{"base"}},
		{SHA: "m1", Parents: []string{"base", "f1"}},
		{SHA: "f1", Parents: []string{"base"}},
		{SHA: "base"},
	}
	l := Build(items)

	// 머지 체인은 모두 0번 레인에 곧게 서야 한다.
	for _, sha := range []string{"m3", "m2", "m1", "base"} {
		if got := l.LaneOf(sha); got != 0 {
			t.Errorf("%s 레인 = %d, want 0", sha, got)
		}
	}
	// 기능 브랜치들은 0번이 아닌 레인에 놓여야 한다.
	for _, sha := range []string{"f1", "f2", "f3"} {
		if got := l.LaneOf(sha); got == 0 {
			t.Errorf("%s 가 주 계보 레인(0)에 놓였다", sha)
		}
	}
	// 부모가 다 범위 안이므로 dangling 이 없어야 한다.
	for _, e := range l.Edges {
		if e.Dangling {
			t.Errorf("예상치 못한 dangling 엣지: %+v", e)
		}
	}
}

// 모든 엣지는 반드시 아래로(자식 row < 부모 row) 향해야 한다.
// 위로 향하는 엣지가 생기면 위상 정렬 전제가 깨진 것이다.
func TestEdgesAlwaysPointDownward(t *testing.T) {
	items := []Item{
		{SHA: "m", Parents: []string{"t", "f"}},
		{SHA: "t", Parents: []string{"b"}},
		{SHA: "f", Parents: []string{"b"}},
		{SHA: "b"},
	}
	l := Build(items)
	for _, e := range l.Edges {
		if e.ToRow <= e.FromRow {
			t.Errorf("엣지가 위로 향한다: row %d → %d", e.FromRow, e.ToRow)
		}
	}
}

func TestEmptyInput(t *testing.T) {
	l := Build(nil)
	if l.Width != 0 || len(l.Nodes) != 0 || len(l.Edges) != 0 {
		t.Errorf("빈 입력 결과 = %+v, want 전부 0", l)
	}
}

// 옥토퍼스 머지(부모 3개 이상)도 레인이 각각 잡혀야 한다.
func TestOctopusMerge(t *testing.T) {
	items := []Item{
		{SHA: "o", Parents: []string{"a", "b", "c"}},
		{SHA: "a", Parents: []string{"r"}},
		{SHA: "b", Parents: []string{"r"}},
		{SHA: "c", Parents: []string{"r"}},
		{SHA: "r"},
	}
	l := Build(items)

	seen := map[int]string{}
	for _, sha := range []string{"a", "b", "c"} {
		lane := l.LaneOf(sha)
		if prev, dup := seen[lane]; dup {
			t.Errorf("%s 와 %s 가 레인 %d 를 공유한다", prev, sha, lane)
		}
		seen[lane] = sha
	}
	if l.Width < 3 {
		t.Errorf("옥토퍼스 레인 수 = %d, want >= 3", l.Width)
	}
}
