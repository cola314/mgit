package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cola314/mgit/internal/gitx"
)

// newTestServer 는 머지가 하나 있는 임시 저장소 위에 서버를 띄운다.
func newTestServer(t *testing.T) (*httptest.Server, *gitx.Repo) {
	t.Helper()
	dir := t.TempDir()

	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=tester", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=tester", "GIT_COMMITTER_EMAIL=t@example.com",
			"GIT_AUTHOR_DATE=2026-01-02T03:04:05+09:00",
			"GIT_COMMITTER_DATE=2026-01-02T03:04:05+09:00",
			"GIT_CONFIG_GLOBAL="+filepath.Join(dir, "no-gitconfig"),
			"GIT_CONFIG_SYSTEM="+filepath.Join(dir, "no-gitconfig"),
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	git("init", "-q", "-b", "main", ".")
	write("Foo.go", "package foo\n\nfunc A() int {\n\treturn 1\n}\n")
	git("add", "-A")
	git("commit", "-qm", "feat: A 추가")
	git("checkout", "-q", "-b", "feature")
	write("Foo.go", "package foo\n\nfunc A() int {\n\treturn 2\n}\n")
	git("commit", "-qam", "fix: 반환값 수정")
	git("checkout", "-q", "main")
	git("merge", "-q", "--no-ff", "-m", "Merge branch 'feature'", "feature")

	repo, err := gitx.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	head, err := repo.ResolveCommit("HEAD")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(New(repo, head, false))
	t.Cleanup(srv.Close)
	return srv, repo
}

// -c 로 연 경우와 그냥 연 경우를 UI 가 구분할 수 있어야 한다.
// 구분이 없으면 딥링크로 커밋을 집어도 그래프 뷰가 떠서 한 번 더 클릭해야 한다.
func TestStateReportsExplicitTarget(t *testing.T) {
	srv, repo := newTestServer(t)
	var st struct {
		Explicit bool `json:"explicit"`
	}
	get(t, srv, "/api/state", &st)
	if st.Explicit {
		t.Error("-c 없이 열었는데 explicit=true")
	}

	// goto 는 언제나 명시적이다.
	send(t, srv, "POST", "/api/goto", map[string]string{"commit": "HEAD"}, nil)
	var after struct {
		Explicit bool `json:"explicit"`
	}
	get(t, srv, "/api/state", &after)
	if !after.Explicit {
		t.Error("goto 이후 explicit=false — UI 가 상세로 이동하지 않는다")
	}

	// -c 로 연 서버는 처음부터 explicit 이다.
	head, _ := repo.ResolveCommit("HEAD")
	srv2 := httptest.NewServer(New(repo, head, true))
	t.Cleanup(srv2.Close)
	var st2 struct {
		Explicit bool `json:"explicit"`
	}
	get(t, srv2, "/api/state", &st2)
	if !st2.Explicit {
		t.Error("-c 로 열었는데 explicit=false")
	}
}

func get(t *testing.T, srv *httptest.Server, path string, out any) *http.Response {
	t.Helper()
	resp, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("GET %s 응답 파싱 실패: %v", path, err)
		}
	}
	return resp
}

func send(t *testing.T, srv *httptest.Server, method, path string, body any, out any) *http.Response {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, srv.URL+path, rdr)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	if out != nil {
		json.NewDecoder(resp.Body).Decode(out)
	}
	return resp
}

func TestPingIdentifiesInstance(t *testing.T) {
	srv, _ := newTestServer(t)
	resp, err := http.Get(srv.URL + "/api/ping")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	if !strings.Contains(buf.String(), "mgit") {
		t.Errorf("ping 응답 = %q, mgit 토큰을 기대", buf.String())
	}
}

func TestServesUI(t *testing.T) {
	srv, _ := newTestServer(t)
	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("GET / = %d, want 200", resp.StatusCode)
	}
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	if !strings.Contains(buf.String(), "<title>mgit</title>") {
		t.Error("index.html 이 서빙되지 않았다")
	}
}

func TestRepoEndpoint(t *testing.T) {
	srv, repo := newTestServer(t)
	var got struct {
		Root, Name, Head, Branch string
	}
	get(t, srv, "/api/repo", &got)

	if got.Root != repo.Root {
		t.Errorf("root = %q, want %q", got.Root, repo.Root)
	}
	if got.Branch != "main" {
		t.Errorf("branch = %q, want main", got.Branch)
	}
	if len(got.Head) != 40 {
		t.Errorf("head = %q", got.Head)
	}
}

// 그래프 배치는 커밋 목록과 정확히 짝이 맞아야 한다. 어긋나면 UI 가 엉뚱한
// 줄에 점을 찍는다.
func TestLogEndpointGraphMatchesCommits(t *testing.T) {
	srv, _ := newTestServer(t)
	var got struct {
		Commits []gitx.Commit `json:"commits"`
		Graph   struct {
			Nodes []struct {
				SHA  string `json:"sha"`
				Row  int    `json:"row"`
				Lane int    `json:"lane"`
			} `json:"nodes"`
			Edges []struct {
				FromRow int `json:"fromRow"`
				ToRow   int `json:"toRow"`
			} `json:"edges"`
			Width int `json:"width"`
		} `json:"graph"`
	}
	get(t, srv, "/api/log", &got)

	if len(got.Commits) != 3 {
		t.Fatalf("커밋 수 = %d, want 3", len(got.Commits))
	}
	if len(got.Graph.Nodes) != len(got.Commits) {
		t.Fatalf("노드 수 = %d, 커밋 수 = %d — 어긋났다", len(got.Graph.Nodes), len(got.Commits))
	}
	for i, n := range got.Graph.Nodes {
		if n.Row != i {
			t.Errorf("노드 %d row = %d, want %d", i, n.Row, i)
		}
		if n.SHA != got.Commits[i].SHA {
			t.Errorf("노드 %d sha = %s, 커밋은 %s", i, n.SHA, got.Commits[i].SHA)
		}
	}
	if got.Graph.Width < 2 {
		t.Errorf("레인 수 = %d, 머지가 있으므로 2 이상을 기대", got.Graph.Width)
	}
}

func TestCommitEndpoint(t *testing.T) {
	srv, repo := newTestServer(t)
	head, _ := repo.ResolveCommit("HEAD")

	var got struct {
		SHA   string `json:"sha"`
		Short string `json:"short"`
		Body  string `json:"body"`
		Stats []struct {
			Path string `json:"path"`
			Add  int    `json:"add"`
			Del  int    `json:"del"`
		} `json:"stats"`
	}
	get(t, srv, "/api/commit/"+head, &got)

	if got.SHA != head {
		t.Errorf("sha = %q, want %q", got.SHA, head)
	}
	if !strings.Contains(got.Body, "Merge branch") {
		t.Errorf("body = %q", got.Body)
	}
	if len(got.Stats) != 1 || got.Stats[0].Path != "Foo.go" {
		t.Errorf("stats = %+v, want Foo.go 하나", got.Stats)
	}

	// 리비전 문법도 그대로 받아야 한다.
	get(t, srv, "/api/commit/HEAD", &got)
	if got.SHA != head {
		t.Errorf("HEAD 해석 = %q, want %q", got.SHA, head)
	}
	if resp := get(t, srv, "/api/commit/nope", nil); resp.StatusCode != 404 {
		t.Errorf("없는 리비전 = %d, want 404", resp.StatusCode)
	}
}

func TestDiffEndpoint(t *testing.T) {
	srv, repo := newTestServer(t)
	head, _ := repo.ResolveCommit("HEAD")

	var got struct {
		Files []gitx.FileDiff `json:"files"`
	}
	get(t, srv, "/api/diff/"+head, &got)

	if len(got.Files) != 1 || got.Files[0].Path != "Foo.go" {
		t.Fatalf("files = %+v, want Foo.go 하나", got.Files)
	}
	var numbered bool
	for _, h := range got.Files[0].Hunks {
		for _, l := range h.Lines {
			if l.NewNo > 0 {
				numbered = true
			}
		}
	}
	if !numbered {
		t.Error("줄 번호가 없다 — 메모 앵커를 만들 수 없다")
	}
}

func TestNoteLifecycle(t *testing.T) {
	srv, repo := newTestServer(t)
	head, _ := repo.ResolveCommit("HEAD")

	// 1. 생성
	var created struct {
		ID     string `json:"id"`
		Kind   string `json:"kind"`
		Status string `json:"status"`
		Anchor struct {
			Commit string `json:"commit"`
			Path   string `json:"path"`
			Line   int    `json:"line"`
		} `json:"anchor"`
		Fingerprint []string `json:"fingerprint"`
	}
	resp := send(t, srv, "POST", "/api/notes", map[string]any{
		"kind": "line", "commit": head, "path": "Foo.go", "line": 4,
		"body": "이 반환값 경계 확인 필요",
	}, &created)
	if resp.StatusCode != 201 {
		t.Fatalf("생성 = %d, want 201", resp.StatusCode)
	}
	if created.ID != "n1" || created.Status != "open" {
		t.Errorf("생성 결과 = %+v", created)
	}
	if created.Anchor.Line != 4 || created.Anchor.Path != "Foo.go" {
		t.Errorf("앵커 = %+v", created.Anchor)
	}
	if len(created.Fingerprint) == 0 {
		t.Error("지문이 비었다 — 코드 이동 추적이 불가능해진다")
	}

	// 파일로 실제 저장돼야 한다. 에이전트가 직접 읽는 경로다.
	raw, err := os.ReadFile(filepath.Join(repo.Root, ".mgit", "notes.jsonl"))
	if err != nil {
		t.Fatalf("notes.jsonl 을 읽을 수 없다: %v", err)
	}
	if !strings.Contains(string(raw), "경계 확인") {
		t.Errorf("notes.jsonl 에 내용이 없다: %s", raw)
	}

	// 2. 목록
	var list []map[string]any
	get(t, srv, "/api/notes?status=all", &list)
	if len(list) != 1 {
		t.Fatalf("목록 = %d건, want 1", len(list))
	}

	// 3. 완료 처리
	var updated map[string]any
	resp = send(t, srv, "PATCH", "/api/notes/n1",
		map[string]any{"status": "done", "resolution": "확인 완료"}, &updated)
	if resp.StatusCode != 200 {
		t.Fatalf("수정 = %d, want 200", resp.StatusCode)
	}
	if updated["status"] != "done" || updated["resolution"] != "확인 완료" {
		t.Errorf("수정 결과 = %+v", updated)
	}

	// 4. 다시 열면 처리 내용이 지워져야 한다.
	// map 을 재사용하면 안 된다. encoding/json 은 기존 map 에 병합하므로
	// 응답에서 빠진 키(omitempty 로 사라진 resolution)가 옛 값으로 남는다.
	var reopened map[string]any
	send(t, srv, "PATCH", "/api/notes/n1", map[string]any{"status": "open"}, &reopened)
	if reopened["status"] != "open" || reopened["resolution"] != nil {
		t.Errorf("재오픈 결과 = %+v, resolution 이 남아 있다", reopened)
	}

	// 5. 삭제
	resp = send(t, srv, "DELETE", "/api/notes/n1", nil, nil)
	if resp.StatusCode != 204 {
		t.Fatalf("삭제 = %d, want 204", resp.StatusCode)
	}
	get(t, srv, "/api/notes?status=all", &list)
	if len(list) != 0 {
		t.Errorf("삭제 후 목록 = %d건, want 0", len(list))
	}
}

func TestNoteCreateValidation(t *testing.T) {
	srv, repo := newTestServer(t)
	head, _ := repo.ResolveCommit("HEAD")

	tests := []struct {
		name string
		body map[string]any
		want int
	}{
		{"빈 내용", map[string]any{"kind": "line", "commit": head, "path": "Foo.go", "line": 1, "body": "  "}, 400},
		{"범위 밖 줄", map[string]any{"kind": "line", "commit": head, "path": "Foo.go", "line": 9999, "body": "x"}, 400},
		{"없는 파일", map[string]any{"kind": "line", "commit": head, "path": "NoSuch.go", "line": 1, "body": "x"}, 400},
		{"없는 커밋", map[string]any{"kind": "line", "commit": "deadbeef", "path": "Foo.go", "line": 1, "body": "x"}, 404},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := send(t, srv, "POST", "/api/notes", tt.body, nil)
			if resp.StatusCode != tt.want {
				t.Errorf("상태 = %d, want %d", resp.StatusCode, tt.want)
			}
		})
	}

	if resp := send(t, srv, "PATCH", "/api/notes/nope", map[string]any{"status": "done"}, nil); resp.StatusCode != 404 {
		t.Errorf("없는 메모 수정 = %d, want 404", resp.StatusCode)
	}
	if resp := send(t, srv, "DELETE", "/api/notes/nope", nil, nil); resp.StatusCode != 404 {
		t.Errorf("없는 메모 삭제 = %d, want 404", resp.StatusCode)
	}
}

// 커밋 단위 메모는 path 없이 만들어진다.
func TestCommitScopedNote(t *testing.T) {
	srv, repo := newTestServer(t)
	head, _ := repo.ResolveCommit("HEAD")

	var created map[string]any
	resp := send(t, srv, "POST", "/api/notes",
		map[string]any{"kind": "commit", "commit": head, "body": "이 머지 재검토"}, &created)
	if resp.StatusCode != 201 {
		t.Fatalf("생성 = %d, want 201", resp.StatusCode)
	}
	if created["kind"] != "commit" {
		t.Errorf("kind = %v, want commit", created["kind"])
	}
	anchor := created["anchor"].(map[string]any)
	if _, hasPath := anchor["path"]; hasPath {
		t.Errorf("커밋 메모에 path 가 있다: %+v", anchor)
	}
}

// export 는 메모 내용과 코드 문맥을 함께 담아야 한다. 에이전트 프롬프트의 품질이 여기 달렸다.
func TestExportIncludesCodeContext(t *testing.T) {
	srv, repo := newTestServer(t)
	head, _ := repo.ResolveCommit("HEAD")
	send(t, srv, "POST", "/api/notes", map[string]any{
		"kind": "line", "commit": head, "path": "Foo.go", "line": 4, "body": "경계값 확인",
	}, nil)

	resp, err := http.Get(srv.URL + "/api/export?status=open")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	md := buf.String()

	for _, want := range []string{"[n1]", "Foo.go:4", "경계값 확인", "← 메모 위치", "```go"} {
		if !strings.Contains(md, want) {
			t.Errorf("export 에 %q 가 없다:\n%s", want, md)
		}
	}
}

// 두 번째 실행이 보낸 커밋으로 UI 가 따라갈 수 있어야 한다.
func TestGotoAdvancesState(t *testing.T) {
	srv, repo := newTestServer(t)

	var before struct {
		Target string `json:"target"`
		Seq    int    `json:"seq"`
	}
	get(t, srv, "/api/state", &before)

	root, err := repo.ResolveCommit("HEAD~1")
	if err != nil {
		t.Fatal(err)
	}
	var after struct {
		Target string `json:"target"`
		Seq    int    `json:"seq"`
	}
	resp := send(t, srv, "POST", "/api/goto", map[string]string{"commit": "HEAD~1"}, &after)
	if resp.StatusCode != 200 {
		t.Fatalf("goto = %d, want 200", resp.StatusCode)
	}
	if after.Target != root {
		t.Errorf("target = %q, want %q", after.Target, root)
	}
	if after.Seq <= before.Seq {
		t.Errorf("seq 가 안 늘었다: %d → %d (UI 가 변화를 감지 못한다)", before.Seq, after.Seq)
	}

	var now struct {
		Target string `json:"target"`
		Seq    int    `json:"seq"`
	}
	get(t, srv, "/api/state", &now)
	if now.Target != root || now.Seq != after.Seq {
		t.Errorf("state = %+v, want target %s seq %d", now, root, after.Seq)
	}

	if resp := send(t, srv, "POST", "/api/goto", map[string]string{"commit": "nope"}, nil); resp.StatusCode != 404 {
		t.Errorf("없는 커밋 goto = %d, want 404", resp.StatusCode)
	}
}

// 에이전트가 CLI 로 메모를 고치면 열려 있는 UI 가 알아채야 한다.
// notesRev 가 안 바뀌면 화면은 낡은 상태로 남고 사람·에이전트 루프가 끊긴다.
func TestNotesRevChangesOnExternalEdit(t *testing.T) {
	srv, repo := newTestServer(t)
	head, _ := repo.ResolveCommit("HEAD")

	var st0 struct {
		NotesRev string `json:"notesRev"`
	}
	get(t, srv, "/api/state", &st0)

	send(t, srv, "POST", "/api/notes",
		map[string]any{"kind": "commit", "commit": head, "body": "첫 메모"}, nil)

	var st1 struct {
		NotesRev string `json:"notesRev"`
	}
	get(t, srv, "/api/state", &st1)
	if st1.NotesRev == st0.NotesRev {
		t.Fatalf("메모 생성 후에도 notesRev 가 %q 그대로다", st0.NotesRev)
	}

	// 이제 외부(CLI)에서 파일을 고친다.
	path := filepath.Join(repo.Root, ".mgit", "notes.jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	var st2 struct {
		NotesRev string `json:"notesRev"`
	}
	get(t, srv, "/api/state", &st2)
	if st2.NotesRev == st1.NotesRev {
		t.Errorf("외부 편집 후에도 notesRev 가 %q 그대로다 — UI 가 갱신되지 않는다", st1.NotesRev)
	}
}

// CLI 가 파일을 직접 고쳐도 API 가 바로 반영해야 한다.
// (에이전트는 CLI 로, 사람은 UI 로 같은 메모를 다룬다)
func TestNotesAreRereadFromDisk(t *testing.T) {
	srv, repo := newTestServer(t)
	head, _ := repo.ResolveCommit("HEAD")
	send(t, srv, "POST", "/api/notes",
		map[string]any{"kind": "commit", "commit": head, "body": "첫 메모"}, nil)

	// 외부에서 파일에 한 줄을 덧붙인다 (CLI 가 하는 일과 같다).
	path := filepath.Join(repo.Root, ".mgit", "notes.jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	extra := `{"id":"n99","kind":"commit","anchor":{"commit":"` + head +
		`"},"body":"CLI 가 남긴 메모","status":"open","created":"2026-01-01T00:00:00Z"}` + "\n"
	if err := os.WriteFile(path, append(raw, []byte(extra)...), 0o644); err != nil {
		t.Fatal(err)
	}

	var list []map[string]any
	get(t, srv, "/api/notes?status=all", &list)
	if len(list) != 2 {
		t.Fatalf("목록 = %d건, want 2 (외부 편집이 반영되지 않았다)", len(list))
	}

	// 다음 ID 는 기존 최대값 다음이어야 한다 (n99 → n100).
	var created map[string]any
	send(t, srv, "POST", "/api/notes",
		map[string]any{"kind": "commit", "commit": head, "body": "세 번째"}, &created)
	if created["id"] != "n100" {
		t.Errorf("새 메모 ID = %v, want n100 (외부 메모와 충돌 방지)", created["id"])
	}
}
