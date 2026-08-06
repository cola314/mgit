// Package graph 는 커밋 DAG 를 세로 레인으로 접는다. SourceTree 류 뷰어가
// 왼쪽에 그리는 그 철길이다.
//
// git 이나 UI 어느 쪽에도 의존하지 않는 순수 계산이라 단위 테스트로 전부 덮인다.
package graph

// Item 은 배치에 필요한 커밋 정보의 최소 집합이다.
type Item struct {
	SHA     string
	Parents []string
}

// Node 는 커밋이 놓일 자리다.
type Node struct {
	SHA  string `json:"sha"`
	Row  int    `json:"row"`
	Lane int    `json:"lane"`
}

// Edge 는 자식에서 부모로 내려가는 선이다.
//
// Dangling 은 부모가 조회 범위 밖이라 화면 아래로 흘러나가는 선을 뜻한다.
// 이 경우 ToRow 는 마지막 행 다음을 가리킨다.
type Edge struct {
	FromRow  int  `json:"fromRow"`
	FromLane int  `json:"fromLane"`
	ToRow    int  `json:"toRow"`
	ToLane   int  `json:"toLane"`
	Dangling bool `json:"dangling,omitempty"`
}

// Layout 은 배치 결과 전체다.
type Layout struct {
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
	Width int    `json:"width"` // 사용된 레인 개수
}

// LaneOf 는 커밋의 레인 번호를 찾는다. 없으면 -1.
func (l Layout) LaneOf(sha string) int {
	for _, n := range l.Nodes {
		if n.SHA == sha {
			return n.Lane
		}
	}
	return -1
}

// Build 는 위상 정렬된 커밋 목록을 레인에 배치한다.
//
// items 는 반드시 --topo-order 결과여야 한다. 부모가 자식보다 뒤에 온다는
// 전제가 깨지면 레인이 엉킨다.
//
// 규칙은 세 줄이다.
//   - 나를 기다리는 레인이 있으면 그 자리에 앉는다. 없으면 빈 레인을 찾는다.
//   - 첫 부모는 내 레인을 그대로 물려받는다. 그래서 주 계보가 곧게 내려간다.
//   - 나머지 부모는 새 레인을 예약한다. 그래서 머지가 옆으로 갈라져 보인다.
func Build(items []Item) Layout {
	index := make(map[string]int, len(items))
	for i, it := range items {
		index[it.SHA] = i
	}

	var lanes []string // lanes[i] = 그 레인이 기다리는 커밋 SHA, "" 면 비어 있음
	placed := make(map[string]int, len(items))
	nodes := make([]Node, 0, len(items))

	for row, it := range items {
		lane := indexOf(lanes, it.SHA)
		if lane < 0 {
			lane = indexOf(lanes, "")
			if lane < 0 {
				lane = len(lanes)
				lanes = append(lanes, "")
			}
		}
		lanes[lane] = "" // 이 자리는 지금 소비된다
		placed[it.SHA] = lane
		nodes = append(nodes, Node{SHA: it.SHA, Row: row, Lane: lane})

		for pi, ph := range it.Parents {
			if _, ok := index[ph]; !ok {
				continue // 조회 범위 밖 부모는 예약하지 않는다
			}
			at := indexOf(lanes, ph)

			if pi == 0 {
				// 첫 부모는 주 계보다. 이미 다른 레인이 이 부모를 기다리고 있어도,
				// 내 레인이 더 왼쪽이면 예약을 빼앗아 온다. 그러지 않으면 사이드
				// 브랜치가 먼저 예약해 버린 공통 부모 때문에 트렁크가 오른쪽으로
				// 밀려 그래프가 계단처럼 어긋난다.
				switch {
				case at < 0:
					lanes[lane] = ph
				case lane < at:
					lanes[at] = ""
					lanes[lane] = ph
				}
				// at <= lane 이면 이미 더 왼쪽에 잡혀 있으니 그대로 합류한다.
				continue
			}

			if at >= 0 {
				continue // 나머지 부모는 이미 예약된 레인으로 합류한다
			}
			free := indexOf(lanes, "")
			if free < 0 {
				free = len(lanes)
				lanes = append(lanes, "")
			}
			lanes[free] = ph
		}
	}

	edges := make([]Edge, 0, len(items))
	for row, it := range items {
		from := placed[it.SHA]
		for _, ph := range it.Parents {
			if pi, ok := index[ph]; ok {
				edges = append(edges, Edge{
					FromRow: row, FromLane: from,
					ToRow: pi, ToLane: placed[ph],
				})
				continue
			}
			edges = append(edges, Edge{
				FromRow: row, FromLane: from,
				ToRow: len(items), ToLane: from,
				Dangling: true,
			})
		}
	}

	return Layout{Nodes: nodes, Edges: edges, Width: len(lanes)}
}

func indexOf(ss []string, want string) int {
	for i, s := range ss {
		if s == want {
			return i
		}
	}
	return -1
}
