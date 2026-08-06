package server

import (
	"embed"
	"io/fs"
	"net/http"
)

// UI 는 바이너리에 그대로 박힌다. 배포가 실행파일 하나로 끝나야 하기 때문이다.
//
//go:embed web
var uiFS embed.FS

// uiHandler 는 임베드된 정적 파일을 서빙한다.
func uiHandler() http.Handler {
	sub, err := fs.Sub(uiFS, "web")
	if err != nil {
		// go:embed 가 성공했다면 여기 올 수 없다.
		panic(err)
	}
	files := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 로컬 전용 도구지만 임베드 자산에 캐시가 눌어붙으면 개발이 성가시다.
		w.Header().Set("Cache-Control", "no-store")
		files.ServeHTTP(w, r)
	})
}
