package server

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
)

// Snapshot 은 지정한 커밋들을 담은 HTML 한 장을 만든다.
//
// 서버 없이 열리는 읽기 전용 화면이다. UI 가 부르는 GET API 응답을 미리 받아 페이지에
// 박고, fetch 를 그 사본으로 바꿔 끼운다. 화면 코드는 서버 모드와 같은 것을 그대로 쓴다.
func (s *Server) Snapshot(shas []string) ([]byte, error) {
	if len(shas) == 0 {
		return nil, fmt.Errorf("커밋이 하나 이상 필요합니다")
	}
	s.mu.Lock()
	s.target, s.explicit = shas[0], true
	s.mu.Unlock()

	api := map[string]json.RawMessage{}
	files := map[string]map[string]json.RawMessage{}
	get := func(path string) (json.RawMessage, error) {
		rec := httptest.NewRecorder()
		s.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			return nil, fmt.Errorf("%s: %d %s", path, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
		return json.RawMessage(rec.Body.Bytes()), nil
	}
	put := func(path string) error {
		b, err := get(path)
		if err == nil {
			api[path] = b
		}
		return err
	}

	for _, p := range []string{"/api/repo", "/api/log", "/api/log?scope=head", "/api/notes?status=all", "/api/state"} {
		if err := put(p); err != nil {
			return nil, err
		}
	}
	for _, sha := range shas {
		if err := put("/api/commit/" + sha); err != nil {
			return nil, err
		}
		if err := put("/api/diff/" + sha); err != nil {
			return nil, err
		}
		stats, err := s.repo.NumStat(sha)
		if err != nil {
			return nil, err
		}
		files[sha] = map[string]json.RawMessage{}
		for _, f := range stats {
			// 삭제된 파일은 그 커밋에 내용이 없다. 화면에서 열면 "스냅샷에 없음"으로 끝난다.
			if b, err := get("/api/file/" + sha + "?path=" + url.QueryEscape(f.Path)); err == nil {
				files[sha][f.Path] = b
			}
		}
	}

	data, err := json.Marshal(map[string]any{"api": api, "files": files})
	if err != nil {
		return nil, err
	}
	page, err := inlinePage(data)
	if err != nil {
		return nil, err
	}
	// 게시처 목록에서 구분되도록 제목에 저장소와 커밋을 넣는다.
	title := "<title>" + baseName(s.repo.Root) + " " + s.repo.Short(shas[0]) + "</title>"
	return []byte(titleRe.ReplaceAllLiteralString(string(page), title)), nil
}

var titleRe = regexp.MustCompile(`<title>[^<]*</title>`)

// shim 은 fetch 를 박아 둔 사본으로 바꾼다. 쓰기 요청은 읽기 전용이라고 거절한다.
const shim = `<script>
(function () {
  var D = window.__MGIT_SNAPSHOT__;
  function reply(code, body) {
    return Promise.resolve(new Response(JSON.stringify(body), {status: code, headers: {"content-type": "application/json"}}));
  }
  window.fetch = function (path, opts) {
    var method = ((opts && opts.method) || "GET").toUpperCase();
    if (method !== "GET") return reply(405, {error: "스냅샷은 읽기 전용입니다"});
    var u = new URL(path, "http://snapshot");
    var m = u.pathname.match(/^\/api\/file\/([^/]+)$/);
    if (m) {
      var f = D.files[m[1]] && D.files[m[1]][u.searchParams.get("path")];
      return f ? reply(200, f) : reply(404, {error: "스냅샷에 없는 파일입니다"});
    }
    var key = u.pathname + u.search;
    if (key in D.api) return reply(200, D.api[key]);
    return reply(404, {error: "스냅샷에 없는 커밋입니다 — 스냅샷을 만들 때 -c 로 지정한 커밋만 볼 수 있습니다"});
  };
})();
</script>
`

// inlinePage 는 임베드된 index.html 에 스크립트를 모두 인라인해 파일 하나로 만든다.
func inlinePage(data []byte) ([]byte, error) {
	read := func(name string) (string, error) {
		b, err := fs.ReadFile(uiFS, "web/"+name)
		return string(b), err
	}
	page, err := read("index.html")
	if err != nil {
		return nil, err
	}
	for _, name := range []string{"highlight.js", "app.js"} {
		src, err := read(name)
		if err != nil {
			return nil, err
		}
		tag := `<script src="` + name + `"></script>`
		if !strings.Contains(page, tag) {
			return nil, fmt.Errorf("index.html 에 %s 태그가 없습니다", name)
		}
		body := strings.ReplaceAll(src, "</script", `<\/script`)
		if name == "highlight.js" {
			// json.Marshal 은 < > & 를 \u 로 바꾸므로 데이터 안에서 태그가 닫힐 일은 없다.
			body = "window.__MGIT_SNAPSHOT__ = " + string(data) + ";\n</script>\n" + shim + "<script>\n" + body
		}
		page = strings.Replace(page, tag, "<script>\n"+body+"\n</script>", 1)
	}
	return []byte(page), nil
}
