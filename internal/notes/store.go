package notes

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// StoreDir 은 레포 안에서 메모가 사는 디렉토리 이름이다.
const StoreDir = ".mgit"

// StoreFile 은 메모 파일 이름이다.
const StoreFile = "notes.jsonl"

// Store 는 한 레포의 메모 전체를 메모리에 들고 있다가 통째로 다시 쓴다.
//
// 메모 개수는 많아야 수백 단위라 부분 갱신을 최적화할 이유가 없다.
// 통째로 쓰면 삭제·상태변경이 append 로그를 재생할 필요 없이 단순해진다.
type Store struct {
	Root  string // 워크트리 최상위
	Path  string // notes.jsonl 절대경로
	Notes []*Note
}

// Open 은 레포의 메모 파일을 읽는다. 파일이 없으면 빈 Store 를 돌려준다.
func Open(root string) (*Store, error) {
	s := &Store{
		Root: root,
		Path: filepath.Join(root, StoreDir, StoreFile),
	}
	f, err := os.Open(s.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	// 메모 본문이 길 수 있으니 기본 64KB 한도를 넉넉히 올린다.
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for ln := 1; sc.Scan(); ln++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var n Note
		if err := json.Unmarshal([]byte(line), &n); err != nil {
			return nil, fmt.Errorf("%s %d번째 줄을 읽을 수 없습니다: %w", s.Path, ln, err)
		}
		s.Notes = append(s.Notes, &n)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return s, nil
}

// Save 는 메모 전체를 파일에 다시 쓴다. 임시 파일에 쓰고 rename 해 중간에 깨지지 않게 한다.
func (s *Store) Save() error {
	dir := filepath.Dir(s.Path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := s.ensureExcluded(); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".notes-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // rename 성공 후에는 이미 사라져 no-op

	w := bufio.NewWriter(tmp)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false) // 한글·꺾쇠가 \uXXXX 로 깨지지 않게
	for _, n := range s.Notes {
		if err := enc.Encode(n); err != nil {
			tmp.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// Windows 는 대상이 있으면 rename 이 실패하므로 먼저 치운다.
	_ = os.Remove(s.Path)
	return os.Rename(tmpName, s.Path)
}

// ensureExcluded 는 .mgit/ 을 .git/info/exclude 에 등록한다.
//
// 공유되는 .gitignore 를 건드리지 않으려는 의도적 선택이다. 메모는 로컬 전용이므로
// 남의 작업트리에 규칙을 강요할 이유가 없다.
func (s *Store) ensureExcluded() error {
	excl := filepath.Join(s.Root, ".git", "info", "exclude")
	// 워크트리·서브모듈이면 .git 이 파일일 수 있다. 그 경우는 조용히 건너뛴다.
	if fi, err := os.Stat(filepath.Join(s.Root, ".git")); err != nil || !fi.IsDir() {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(excl), 0o755); err != nil {
		return err
	}
	body, err := os.ReadFile(excl)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, line := range strings.Split(string(body), "\n") {
		if strings.TrimSpace(line) == StoreDir+"/" {
			return nil
		}
	}
	f, err := os.OpenFile(excl, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	prefix := ""
	if len(body) > 0 && !strings.HasSuffix(string(body), "\n") {
		prefix = "\n"
	}
	_, err = f.WriteString(prefix + "\n# mgit 로컬 메모 (공유 대상 아님)\n" + StoreDir + "/\n")
	return err
}

// NextID 는 쓰이지 않은 다음 메모 ID(n1, n2, …)를 만든다.
func (s *Store) NextID() string {
	max := 0
	for _, n := range s.Notes {
		if !strings.HasPrefix(n.ID, "n") {
			continue
		}
		if v, err := strconv.Atoi(n.ID[1:]); err == nil && v > max {
			max = v
		}
	}
	return "n" + strconv.Itoa(max+1)
}

// Find 는 ID 로 메모를 찾는다. 없으면 nil.
func (s *Store) Find(id string) *Note {
	id = strings.TrimSpace(id)
	for _, n := range s.Notes {
		if n.ID == id {
			return n
		}
	}
	return nil
}

// Add 는 메모를 추가한다.
func (s *Store) Add(n *Note) { s.Notes = append(s.Notes, n) }

// Remove 는 ID 로 메모를 지운다. 지웠으면 true.
func (s *Store) Remove(id string) bool {
	for i, n := range s.Notes {
		if n.ID == id {
			s.Notes = append(s.Notes[:i], s.Notes[i+1:]...)
			return true
		}
	}
	return false
}

// Select 는 상태로 거른 메모를 돌려준다. status 가 "" 나 "all" 이면 전부.
// 결과는 open 먼저, 그 안에서는 생성 순으로 정렬된다.
func (s *Store) Select(status string) []*Note {
	out := make([]*Note, 0, len(s.Notes))
	for _, n := range s.Notes {
		if status == "" || status == "all" || n.Status == status {
			out = append(out, n)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].Status == StatusOpen) != (out[j].Status == StatusOpen) {
			return out[i].Status == StatusOpen
		}
		return out[i].Created.Before(out[j].Created)
	})
	return out
}
