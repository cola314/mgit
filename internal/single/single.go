// Package single 은 저장소당 하나의 뷰어 인스턴스만 뜨도록 조정한다.
//
// `mgit . -c abc123` 을 다시 실행했을 때 창이 하나 더 뜨면 안 되고, 이미 떠 있는
// 화면이 그 커밋으로 이동해야 한다. 그래서 첫 인스턴스가 루프백 리스너를 열고
// 주소를 파일에 적어두면, 이후 실행은 그 주소로 요청만 보내고 바로 종료한다.
//
// 이름 있는 파이프 대신 루프백 TCP 를 쓴다. 어차피 UI 를 같은 포트로 서빙하므로
// 채널이 하나로 줄고, 크로스 플랫폼 분기도 사라진다.
package single

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PingPath 는 살아 있는 인스턴스인지 확인하는 경로다. 서버가 반드시 등록해야 한다.
const PingPath = "/api/ping"

// pingToken 은 그 포트가 정말 mgit 인지 구별하는 값이다.
// 포트 파일이 낡아 엉뚱한 프로세스가 그 포트를 물었을 수 있다.
const pingToken = "mgit-instance-ok"

// probeTimeout 은 기존 인스턴스 확인에 쓰는 짧은 제한 시간이다.
const probeTimeout = 700 * time.Millisecond

// Instance 는 이 프로세스가 획득한 리스너다.
type Instance struct {
	Listener net.Listener
	Addr     string // "127.0.0.1:52341"
	lockPath string
}

// lockData 는 포트 파일 내용이다.
type lockData struct {
	Addr string `json:"addr"`
	Root string `json:"root"`
	PID  int    `json:"pid"`
}

// Acquire 는 root 저장소의 인스턴스 자리를 잡는다.
//
// 이미 떠 있으면 inst 는 nil 이고 existing 에 그 주소가 담긴다.
func Acquire(root string) (inst *Instance, existing string, err error) {
	lockPath, err := lockPathFor(root)
	if err != nil {
		return nil, "", err
	}

	if addr, ok := probe(lockPath); ok {
		return nil, addr, nil
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", fmt.Errorf("루프백 포트를 열 수 없습니다: %w", err)
	}
	addr := ln.Addr().String()

	body, err := json.Marshal(lockData{Addr: addr, Root: root, PID: os.Getpid()})
	if err != nil {
		ln.Close()
		return nil, "", err
	}
	if err := os.WriteFile(lockPath, body, 0o600); err != nil {
		ln.Close()
		return nil, "", err
	}
	return &Instance{Listener: ln, Addr: addr, lockPath: lockPath}, "", nil
}

// Release 는 리스너를 닫고 포트 파일을 지운다.
func (i *Instance) Release() {
	if i == nil {
		return
	}
	if i.Listener != nil {
		i.Listener.Close()
	}
	// 남의 인스턴스 파일을 지우지 않도록 내 주소일 때만 삭제한다.
	if data, err := os.ReadFile(i.lockPath); err == nil {
		var d lockData
		if json.Unmarshal(data, &d) == nil && d.Addr != i.Addr {
			return
		}
	}
	os.Remove(i.lockPath)
}

// URL 은 이 인스턴스의 기본 주소를 만든다.
func (i *Instance) URL() string { return "http://" + i.Addr }

// Notify 는 이미 떠 있는 인스턴스에 JSON 을 POST 한다.
func Notify(addr, path string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: probeTimeout}
	resp, err := client.Post("http://"+addr+path, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("기존 인스턴스가 %s 로 응답했습니다", resp.Status)
	}
	return nil
}

// HandlePing 은 서버가 PingPath 에 등록해야 하는 핸들러다.
func HandlePing(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	io.WriteString(w, pingToken)
}

// probe 는 포트 파일을 읽어 그 주소에 살아 있는 mgit 이 있는지 확인한다.
//
// 죽은 인스턴스가 남긴 낡은 파일이면 지우고 false 를 돌려준다.
func probe(lockPath string) (string, bool) {
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return "", false
	}
	var d lockData
	if err := json.Unmarshal(data, &d); err != nil || d.Addr == "" {
		os.Remove(lockPath)
		return "", false
	}

	client := &http.Client{Timeout: probeTimeout}
	resp, err := client.Get("http://" + d.Addr + PingPath)
	if err != nil {
		os.Remove(lockPath) // 아무도 안 듣는 낡은 파일
		return "", false
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64))
	if strings.TrimSpace(string(body)) != pingToken {
		// 다른 프로그램이 그 포트를 쓰고 있다. 파일을 지우고 새로 잡는다.
		os.Remove(lockPath)
		return "", false
	}
	return d.Addr, true
}

// lockPathFor 는 저장소별 포트 파일 경로를 만든다.
//
// 저장소 안이 아니라 사용자 캐시 디렉토리에 둔다. 작업트리를 더럽히지 않고,
// 저장소를 지웠다 다시 클론해도 찌꺼기가 남지 않는다.
func lockPathFor(root string) (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "mgit")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	// 경로를 그대로 파일명에 못 쓰므로 해시로 줄인다. 대소문자 차이를 흡수하기
	// 위해 Windows 를 고려해 소문자로 맞춘다.
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(root))))
	return filepath.Join(dir, hex.EncodeToString(sum[:8])+".json"), nil
}

// ErrNoInstance 는 살아 있는 인스턴스가 없을 때 Lookup 이 돌려준다.
var ErrNoInstance = errors.New("실행 중인 인스턴스가 없습니다")

// Lookup 은 root 저장소에 떠 있는 인스턴스 주소를 찾는다.
func Lookup(root string) (string, error) {
	lockPath, err := lockPathFor(root)
	if err != nil {
		return "", err
	}
	if addr, ok := probe(lockPath); ok {
		return addr, nil
	}
	return "", ErrNoInstance
}
