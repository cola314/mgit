// Package server 는 뷰어 UI 와 그 JSON API 를 루프백에서 서빙한다.
package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cola314/mgit/internal/gitx"
	"github.com/cola314/mgit/internal/graph"
	"github.com/cola314/mgit/internal/notes"
	"github.com/cola314/mgit/internal/single"
)

// DefaultLogLimit 은 한 번에 읽는 커밋 수다.
const DefaultLogLimit = 300

// Server 는 저장소 하나를 서빙한다.
type Server struct {
	repo *gitx.Repo
	mux  *http.ServeMux

	mu     sync.Mutex // 메모 쓰기와 target 갱신을 직렬화한다
	target string     // 현재 UI 가 보고 있어야 할 커밋
	seq    int        // target 이 바뀔 때마다 증가 — UI 폴링이 변화를 감지하는 값
	// explicit 은 사용자가 커밋을 콕 집어 지정했는지다(-c 또는 goto).
	// 지정했으면 UI 가 그래프가 아니라 상세 뷰로 연다.
	explicit bool
}

// New 는 서버를 만든다. target 은 처음 열 커밋, explicit 은 -c 로 지정됐는지 여부다.
func New(repo *gitx.Repo, target string, explicit bool) *Server {
	s := &Server{repo: repo, mux: http.NewServeMux(), target: target, explicit: explicit}
	s.routes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

// healthy 는 이 인스턴스가 아직 git 을 실행할 수 있는지 본다.
// 뷰어를 띄운 셸이 죽으면 프로세스는 남지만 자식 프로세스를 못 띄우게 된다.
func (s *Server) healthy() error {
	_, err := s.repo.Run("rev-parse", "--git-dir")
	return err
}

func (s *Server) routes() {
	// 메서드를 붙여야 한다. 메서드 없는 패턴은 아래 "GET /" 과 충돌해 패닉이 난다.
	s.mux.HandleFunc("GET "+single.PingPath, single.HandlePing(s.healthy))

	s.mux.HandleFunc("GET /api/repo", s.handleRepo)
	s.mux.HandleFunc("GET /api/log", s.handleLog)
	s.mux.HandleFunc("GET /api/commit/{sha}", s.handleCommit)
	s.mux.HandleFunc("GET /api/diff/{sha}", s.handleDiff)
	s.mux.HandleFunc("GET /api/file/{sha}", s.handleFile)
	s.mux.HandleFunc("GET /api/state", s.handleState)
	s.mux.HandleFunc("POST /api/goto", s.handleGoto)

	s.mux.HandleFunc("GET /api/notes", s.handleNotesList)
	s.mux.HandleFunc("POST /api/notes", s.handleNotesCreate)
	s.mux.HandleFunc("PATCH /api/notes/{id}", s.handleNotesUpdate)
	s.mux.HandleFunc("DELETE /api/notes/{id}", s.handleNotesDelete)
	s.mux.HandleFunc("GET /api/export", s.handleExport)

	s.mux.Handle("GET /", uiHandler())
}

// ── 저장소 ────────────────────────────────────────────────────────

func (s *Server) handleRepo(w http.ResponseWriter, r *http.Request) {
	head, err := s.repo.ResolveCommit("HEAD")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	branch, _ := s.repo.Run("rev-parse", "--abbrev-ref", "HEAD")
	writeJSON(w, http.StatusOK, map[string]any{
		"root":   s.repo.Root,
		"name":   baseName(s.repo.Root),
		"head":   head,
		"branch": branch,
	})
}

func (s *Server) handleLog(w http.ResponseWriter, r *http.Request) {
	limit := DefaultLogLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	// scope=head 면 현재 브랜치(HEAD)의 조상만 본다. 기본은 모든 ref.
	// SourceTree 의 "현재 브랜치만 보기"와 같은 필터다.
	opt := gitx.LogOptions{Limit: limit}
	if r.URL.Query().Get("scope") == "head" {
		opt.Revs = []string{"HEAD"}
	}
	commits, err := s.repo.Log(opt)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	items := make([]graph.Item, len(commits))
	for i, c := range commits {
		items[i] = graph.Item{SHA: c.SHA, Parents: c.Parents}
	}
	layout := graph.Build(items)

	// 커밋별 메모 개수를 같이 실어 보낸다. UI 가 목록에서 바로 표시할 수 있도록.
	counts := map[string]int{}
	if st, err := notes.Open(s.repo.Root); err == nil {
		for _, n := range st.Notes {
			counts[n.Anchor.Commit]++
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"commits":    commits,
		"graph":      layout,
		"noteCounts": counts,
	})
}

func (s *Server) handleCommit(w http.ResponseWriter, r *http.Request) {
	sha, err := s.repo.ResolveCommit(r.PathValue("sha"))
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	stats, err := s.repo.NumStat(sha)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	body, _ := s.repo.Run("log", "-1", "--format=%B", sha)
	writeJSON(w, http.StatusOK, map[string]any{
		"sha":   sha,
		"short": s.repo.Short(sha),
		"body":  strings.TrimSpace(body),
		"stats": stats,
	})
}

func (s *Server) handleDiff(w http.ResponseWriter, r *http.Request) {
	sha, err := s.repo.ResolveCommit(r.PathValue("sha"))
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	ctx := gitx.DefaultDiffContext
	if v := r.URL.Query().Get("ctx"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			ctx = n
		}
	}
	files, err := s.repo.Diff(sha, r.URL.Query().Get("path"), ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if files == nil {
		files = []gitx.FileDiff{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"sha": sha, "files": files})
}

// MaxFileLines 는 파일 전문 조회에서 한 번에 내려보내는 최대 줄 수다.
// 생성된 소스나 덤프 파일 하나로 브라우저를 멈추게 하지 않으려는 상한이다.
const MaxFileLines = 20000

// handleFile 은 특정 커밋 시점의 파일 전문을 준다.
//
// diff 는 헝크 주변만 보여주므로 앞뒤 맥락이 필요할 때가 있다. 워킹트리가 아니라
// 그 커밋 시점을 읽는 것이 중요하다 — 지금 파일은 이미 달라져 있을 수 있다.
func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	sha, err := s.repo.ResolveCommit(r.PathValue("sha"))
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	path := r.URL.Query().Get("path")
	if strings.TrimSpace(path) == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("path 가 필요합니다"))
		return
	}
	rel, err := s.repo.RelPath(path)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	lines, err := s.repo.FileLines(sha, rel)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}

	// 바이너리는 줄 단위로 보여줄 게 없다. NUL 이 있으면 바이너리로 본다.
	for _, l := range lines {
		if strings.ContainsRune(l, 0) {
			writeJSON(w, http.StatusOK, map[string]any{
				"sha": sha, "path": rel, "binary": true, "lines": []string{},
			})
			return
		}
	}

	truncated := false
	if len(lines) > MaxFileLines {
		lines = lines[:MaxFileLines]
		truncated = true
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"sha":       sha,
		"path":      rel,
		"lines":     lines,
		"truncated": truncated,
		"total":     len(lines),
	})
}

// ── 딥링크 ────────────────────────────────────────────────────────

// handleState 는 UI 가 주기적으로 물어보는 현재 목표 커밋이다.
//
// 두 번째 `mgit . -c <sha>` 실행이 여기로 흘러들어와 이미 열려 있는 화면을 이동시킨다.
func (s *Server) handleState(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	target, seq, explicit := s.target, s.seq, s.explicit
	s.mu.Unlock()

	// HEAD 를 같이 실어 보낸다. 커밋·amend·브랜치 전환이 일어나면 UI 가 목록을
	// 다시 읽어야 하는데, 알려주지 않으면 방금 만든 커밋이 화면에 영영 안 뜬다.
	head, _ := s.repo.Run("rev-parse", "HEAD")

	writeJSON(w, http.StatusOK, map[string]any{
		"target":   target,
		"seq":      seq,
		"explicit": explicit,
		"notesRev": s.notesRev(),
		"head":     head,
	})
}

// notesRev 는 메모 파일의 개정 표식이다.
//
// 에이전트가 `mgit note done` 으로 파일을 고쳐도 열려 있는 화면이 따라가야 한다.
// 매 폴링마다 메모 전체를 내려보내는 대신 stat 한 번으로 변경만 감지한다.
func (s *Server) notesRev() string {
	fi, err := os.Stat(filepath.Join(s.repo.Root, notes.StoreDir, notes.StoreFile))
	if err != nil {
		return "0"
	}
	return fmt.Sprintf("%d-%d", fi.ModTime().UnixNano(), fi.Size())
}

func (s *Server) handleGoto(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Commit string `json:"commit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	sha, err := s.repo.ResolveCommit(req.Commit)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	s.mu.Lock()
	s.target = sha
	s.explicit = true // goto 는 언제나 사용자가 콕 집은 것이다
	s.seq++
	seq := s.seq
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"target": sha, "seq": seq})
}

// ── 메모 ──────────────────────────────────────────────────────────

// 메모는 요청마다 디스크에서 다시 읽는다. CLI 나 에이전트가 파일을 직접
// 고쳐도 UI 가 바로 따라오게 하려는 의도적 선택이다.
func (s *Server) handleNotesList(w http.ResponseWriter, r *http.Request) {
	st, err := notes.Open(s.repo.Root)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	list := st.Select(r.URL.Query().Get("status"))
	if list == nil {
		list = []*notes.Note{}
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleNotesCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind   string `json:"kind"`
		Commit string `json:"commit"`
		Path    string `json:"path"`
		Line    int    `json:"line"`
		EndLine int    `json:"endLine"`
		Body    string `json:"body"`
		Parent  string `json:"parent"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(req.Body) == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("메모 내용이 비어 있습니다"))
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	st, err := notes.Open(s.repo.Root)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	// 답글은 부모의 앵커를 그대로 물려받는다. 위치를 다시 풀 필요가 없다.
	if p := strings.TrimSpace(req.Parent); p != "" {
		target := st.Find(p)
		if target == nil {
			writeErr(w, http.StatusNotFound, fmt.Errorf("%s 메모를 찾을 수 없습니다", p))
			return
		}
		root := st.ThreadRoot(target)
		reply := &notes.Note{
			ID:          st.NextID(),
			Parent:      root.ID,
			Kind:        root.Kind,
			Anchor:      root.Anchor,
			Fingerprint: root.Fingerprint,
			Body:        strings.TrimSpace(req.Body),
			Status:      notes.StatusOpen,
			Created:     time.Now(),
		}
		st.Add(reply)
		if err := st.Save(); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusCreated, reply)
		return
	}

	sha, err := s.repo.ResolveCommit(req.Commit)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	n := &notes.Note{
		ID:      st.NextID(),
		Body:    strings.TrimSpace(req.Body),
		Status:  notes.StatusOpen,
		Created: time.Now(),
	}
	if req.Kind == string(notes.KindCommit) || req.Path == "" {
		n.Kind = notes.KindCommit
		n.Anchor = notes.Anchor{Commit: sha}
	} else {
		rel, err := s.repo.RelPath(req.Path)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		lines, err := s.repo.FileLines(sha, rel)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		if req.Line < 1 || req.Line > len(lines) {
			writeErr(w, http.StatusBadRequest,
				fmt.Errorf("%s 는 %d줄뿐입니다 (%d번 줄 지정)", rel, len(lines), req.Line))
			return
		}
		if req.EndLine > len(lines) {
			writeErr(w, http.StatusBadRequest,
				fmt.Errorf("%s 는 %d줄뿐입니다 (%d번 줄까지 지정)", rel, len(lines), req.EndLine))
			return
		}
		n.Kind = notes.KindLine
		n.Anchor = notes.Anchor{Commit: sha, Path: rel, Line: req.Line}
		if req.EndLine > req.Line {
			n.Anchor.EndLine = req.EndLine
		}
		n.Fingerprint = notes.MakeFingerprint(lines, req.Line)
	}

	st.Add(n)
	if err := st.Save(); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, n)
}

func (s *Server) handleNotesUpdate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Status     *string `json:"status"`
		Resolution *string `json:"resolution"`
		Body       *string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	st, err := notes.Open(s.repo.Root)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	n := st.Find(r.PathValue("id"))
	if n == nil {
		writeErr(w, http.StatusNotFound, fmt.Errorf("%s 메모를 찾을 수 없습니다", r.PathValue("id")))
		return
	}

	if req.Status != nil && n.IsReply() {
		writeErr(w, http.StatusBadRequest,
			fmt.Errorf("%s 는 답글이라 상태를 가지지 않습니다. 부모 메모 %s 를 처리하세요", n.ID, n.Parent))
		return
	}
	if req.Status != nil {
		switch *req.Status {
		case notes.StatusOpen:
			n.Status = notes.StatusOpen
			n.Resolution = ""
		case notes.StatusDone:
			n.Status = notes.StatusDone
		default:
			writeErr(w, http.StatusBadRequest, fmt.Errorf("알 수 없는 상태: %s", *req.Status))
			return
		}
	}
	if req.Resolution != nil && n.Status == notes.StatusDone {
		n.Resolution = strings.TrimSpace(*req.Resolution)
	}
	if req.Body != nil && strings.TrimSpace(*req.Body) != "" {
		n.Body = strings.TrimSpace(*req.Body)
	}
	now := time.Now()
	n.Updated = &now

	if err := st.Save(); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, n)
}

func (s *Server) handleNotesDelete(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	st, err := notes.Open(s.repo.Root)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if !st.Remove(r.PathValue("id")) {
		writeErr(w, http.StatusNotFound, fmt.Errorf("%s 메모를 찾을 수 없습니다", r.PathValue("id")))
		return
	}
	if err := st.Save(); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	st, err := notes.Open(s.repo.Root)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	status := r.URL.Query().Get("status")
	if status == "" {
		status = notes.StatusOpen
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	notes.Export(w, st.Select(status), s.repo, notes.DefaultContext)
}

// ── 공통 ──────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func baseName(p string) string {
	p = strings.TrimRight(strings.ReplaceAll(p, "\\", "/"), "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}
