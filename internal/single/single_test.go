package single

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// serve 는 인스턴스 리스너 위에 ping + 수신 핸들러를 띄운다.
func serve(t *testing.T, inst *Instance) *received {
	t.Helper()
	got := &received{}
	mux := http.NewServeMux()
	mux.HandleFunc(PingPath, HandlePing(nil))
	mux.HandleFunc("/api/goto", func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]string
		json.NewDecoder(r.Body).Decode(&payload)
		got.set(payload)
		w.WriteHeader(http.StatusNoContent)
	})
	srv := &http.Server{Handler: mux}
	go srv.Serve(inst.Listener)
	t.Cleanup(func() { srv.Close(); inst.Release() })
	return got
}

type received struct {
	mu sync.Mutex
	v  map[string]string
}

func (r *received) set(v map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.v = v
}
func (r *received) get() map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.v
}

// 캐시 디렉토리를 테스트 전용으로 돌려 사용자 환경을 건드리지 않는다.
func isolateCache(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	// os.UserCacheDir 은 Windows 에서 LOCALAPPDATA, 그 외에서 XDG_CACHE_HOME 을 본다.
	t.Setenv("LOCALAPPDATA", dir)
	t.Setenv("XDG_CACHE_HOME", dir)
	t.Setenv("HOME", dir)
}

func TestAcquireFirstInstance(t *testing.T) {
	isolateCache(t)
	root := t.TempDir()

	inst, existing, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	if inst == nil {
		t.Fatal("첫 인스턴스인데 리스너를 못 받았다")
	}
	if existing != "" {
		t.Errorf("existing = %q, want 빈 문자열", existing)
	}
	defer inst.Release()

	if inst.Addr == "" {
		t.Error("주소가 비어 있다")
	}
	if got := inst.URL(); got != "http://"+inst.Addr {
		t.Errorf("URL() = %q", got)
	}
}

// 핵심 시나리오: 두 번째 실행은 창을 새로 띄우지 않고 기존 인스턴스를 찾아야 한다.
func TestSecondAcquireFindsExisting(t *testing.T) {
	isolateCache(t)
	root := t.TempDir()

	first, _, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	serve(t, first)

	second, existing, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	if second != nil {
		second.Release()
		t.Fatal("두 번째 실행이 리스너를 새로 잡았다 — 창이 두 개 뜬다")
	}
	if existing != first.Addr {
		t.Errorf("existing = %q, want %q", existing, first.Addr)
	}
}

// 두 번째 실행이 넘긴 인자가 실제로 첫 인스턴스에 도착해야 한다.
func TestNotifyReachesRunningInstance(t *testing.T) {
	isolateCache(t)
	root := t.TempDir()

	first, _, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	got := serve(t, first)

	_, existing, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	if existing == "" {
		t.Fatal("기존 인스턴스를 못 찾았다")
	}

	if err := Notify(existing, "/api/goto", map[string]string{"commit": "abc123"}); err != nil {
		t.Fatalf("Notify 실패: %v", err)
	}
	if v := got.get(); v == nil || v["commit"] != "abc123" {
		t.Errorf("수신 내용 = %+v, want commit=abc123", v)
	}
}

// 저장소가 다르면 인스턴스도 따로 떠야 한다.
func TestDifferentReposGetSeparateInstances(t *testing.T) {
	isolateCache(t)
	rootA, rootB := t.TempDir(), t.TempDir()

	a, _, err := Acquire(rootA)
	if err != nil {
		t.Fatal(err)
	}
	serve(t, a)

	b, existing, err := Acquire(rootB)
	if err != nil {
		t.Fatal(err)
	}
	if b == nil {
		t.Fatalf("다른 저장소인데 기존 인스턴스(%s)로 붙었다", existing)
	}
	defer b.Release()
	if a.Addr == b.Addr {
		t.Error("두 저장소가 같은 주소를 쓴다")
	}
}

// 프로세스가 죽어 리스너는 없는데 포트 파일만 남은 경우.
// 여기서 막히면 비정상 종료 후 영영 실행이 안 된다.
func TestStaleLockFileIsReclaimed(t *testing.T) {
	isolateCache(t)
	root := t.TempDir()

	first, _, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	staleAddr := first.Addr
	lockPath := first.lockPath
	// 서버를 띄우지 않고 리스너만 닫는다 = 죽은 프로세스가 파일을 남긴 상태.
	first.Listener.Close()
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("포트 파일이 없다: %v", err)
	}

	second, existing, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	if second == nil {
		t.Fatalf("낡은 포트 파일(%s) 때문에 새 인스턴스를 못 잡았다 (existing=%s)", staleAddr, existing)
	}
	defer second.Release()
	if existing != "" {
		t.Errorf("existing = %q, want 빈 문자열", existing)
	}
}

// 포트 파일이 깨졌어도 복구돼야 한다.
func TestCorruptLockFileIsReclaimed(t *testing.T) {
	isolateCache(t)
	root := t.TempDir()

	lockPath, err := lockPathFor(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, []byte("{쓰레기"), 0o600); err != nil {
		t.Fatal(err)
	}

	inst, existing, err := Acquire(root)
	if err != nil {
		t.Fatalf("깨진 포트 파일에서 Acquire 실패: %v", err)
	}
	if inst == nil {
		t.Fatalf("깨진 포트 파일 때문에 인스턴스를 못 잡았다 (existing=%s)", existing)
	}
	inst.Release()
}

// 그 포트를 mgit 이 아닌 다른 프로그램이 쓰고 있으면 붙으면 안 된다.
func TestForeignServerOnPortIsRejected(t *testing.T) {
	isolateCache(t)
	root := t.TempDir()

	// mgit 이 아닌 서버를 띄우고 그 주소를 포트 파일에 심는다.
	foreign := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("나는 다른 서버다"))
	})}
	inst, _, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	go foreign.Serve(inst.Listener)
	defer foreign.Close()

	second, existing, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	if second == nil {
		t.Fatalf("mgit 이 아닌 서버(%s)에 붙었다", existing)
	}
	second.Release()
}

// 프로세스는 살아 있지만 일을 못 하는 인스턴스(셸이 죽어 git 을 못 띄우는 상태)는
// "실행 중"으로 보면 안 된다. 그렇지 않으면 새 인스턴스가 영영 못 뜬다.
func TestUnhealthyInstanceIsReclaimed(t *testing.T) {
	isolateCache(t)
	root := t.TempDir()

	first, _, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc(PingPath, HandlePing(func() error { return errors.New("git 을 실행할 수 없습니다") }))
	srv := &http.Server{Handler: mux}
	go srv.Serve(first.Listener)
	defer srv.Close()

	second, existing, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	if second == nil {
		t.Fatalf("좀비 인스턴스(%s)에 붙었다 — 새 인스턴스를 띄우지 못한다", existing)
	}
	second.Release()
}

func TestLookup(t *testing.T) {
	isolateCache(t)
	root := t.TempDir()

	if _, err := Lookup(root); err != ErrNoInstance {
		t.Errorf("인스턴스 없을 때 Lookup 오류 = %v, want ErrNoInstance", err)
	}

	inst, _, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	serve(t, inst)

	addr, err := Lookup(root)
	if err != nil {
		t.Fatalf("Lookup 실패: %v", err)
	}
	if addr != inst.Addr {
		t.Errorf("Lookup = %q, want %q", addr, inst.Addr)
	}
}

// 포트 파일은 작업트리가 아니라 사용자 캐시에 있어야 한다.
func TestLockFileLivesOutsideRepo(t *testing.T) {
	isolateCache(t)
	root := t.TempDir()

	lockPath, err := lockPathFor(root)
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(root, lockPath)
	if err == nil && !filepath.IsAbs(rel) && len(rel) > 1 && rel[0] != '.' {
		t.Errorf("포트 파일이 저장소 안에 있다: %s", lockPath)
	}
	if filepath.Base(filepath.Dir(lockPath)) != "mgit" {
		t.Errorf("포트 파일 디렉토리 = %s, want .../mgit/", filepath.Dir(lockPath))
	}
}
