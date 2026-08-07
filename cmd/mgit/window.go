package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// openWindow 는 뷰어를 앱 모드 창으로 띄운다.
//
// Chromium 계열의 --app= 은 주소창·탭·북마크바가 없는 독립 창을 열고 작업표시줄
// 아이콘도 따로 잡아준다. 네이티브 셸(Wails 등)을 끌어들이지 않고도 앱처럼 보이게
// 하는 가장 싼 방법이다. 브라우저를 못 찾으면 기본 브라우저 탭으로 떨어진다.
func openWindow(url, profileDir string) {
	bin := findChromium()
	if bin == "" {
		openBrowser(url)
		return
	}

	// 전용 프로필을 쓴다. 사용자의 평소 브라우저 세션과 창을 섞지 않기 위해서다.
	// 같은 프로필을 공유하면 이미 떠 있는 브라우저 인스턴스에 흡수돼 그냥 탭으로 열린다.
	args := []string{
		"--app=" + url,
		"--user-data-dir=" + profileDir,
		"--no-first-run",
		"--no-default-browser-check",
		"--window-size=1440,900",
	}
	cmd := exec.Command(bin, args...)
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "앱 창을 열지 못했습니다(%v). 브라우저로 엽니다.\n", err)
		openBrowser(url)
		return
	}
	go cmd.Wait() // 좀비 프로세스 방지
}

// findChromium 은 앱 모드를 지원하는 브라우저 실행파일을 찾는다.
func findChromium() string {
	var candidates []string

	switch runtime.GOOS {
	case "windows":
		var roots []string
		for _, env := range []string{"PROGRAMFILES", "PROGRAMFILES(X86)", "LOCALAPPDATA"} {
			if v := os.Getenv(env); v != "" {
				roots = append(roots, v)
			}
		}
		rel := []string{
			`Microsoft\Edge\Application\msedge.exe`,
			`Google\Chrome\Application\chrome.exe`,
		}
		for _, r := range roots {
			for _, p := range rel {
				candidates = append(candidates, filepath.Join(r, p))
			}
		}
	case "darwin":
		candidates = []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		}
	default:
		for _, name := range []string{"google-chrome", "chromium", "chromium-browser", "microsoft-edge"} {
			if p, err := exec.LookPath(name); err == nil {
				return p
			}
		}
	}

	for _, p := range candidates {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	return ""
}

// windowProfileDir 은 앱 창 전용 브라우저 프로필 경로다.
//
// 저장소별로 나눈다. 그래야 여러 저장소를 동시에 열었을 때 각각 독립된 창과
// 작업표시줄 항목을 갖는다.
func windowProfileDir(root string) string {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(root))))
	return filepath.Join(base, "mgit", "window", hex.EncodeToString(sum[:8]))
}
