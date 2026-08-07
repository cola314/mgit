package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"time"

	"github.com/cola314/mgit/internal/gitx"
	"github.com/cola314/mgit/internal/server"
	"github.com/cola314/mgit/internal/single"
)

// runOpen 은 뷰어를 연다.
//
// 저장소마다 인스턴스는 하나다. 이미 떠 있으면 새 창을 만들지 않고 그쪽에
// "이 커밋으로 가라"고 알린 뒤 이 프로세스는 바로 끝난다. 그래서
// `mgit . -c <sha>` 를 셸에서 반복해도 창이 늘어나지 않는다.
func runOpen(args []string) error {
	fs := flag.NewFlagSet("mgit", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	rev := fs.String("c", "", "열면서 이동할 리비전")
	noBrowser := fs.Bool("no-browser", false, "브라우저를 자동으로 열지 않는다")
	printURL := fs.Bool("print-url", false, "주소만 출력하고 종료한다")

	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	dir := "."
	if len(pos) > 0 {
		dir = pos[0]
	}
	if len(pos) > 1 {
		return fmt.Errorf("경로는 하나만 지정할 수 있습니다")
	}

	repo, err := gitx.Open(dir)
	if err != nil {
		return err
	}
	// 리비전은 서버를 띄우기 전에 검증한다. 오타 때문에 창이 떠 버리면 성가시다.
	sha, err := repo.ResolveCommit(*rev)
	if err != nil {
		return err
	}

	inst, existing, err := single.Acquire(repo.Root)
	if err != nil {
		return err
	}

	if inst == nil {
		// 이미 떠 있다. 그 화면을 이동시키고 끝낸다.
		if err := single.Notify(existing, "/api/goto", map[string]string{"commit": sha}); err != nil {
			return fmt.Errorf("실행 중인 뷰어에 전달하지 못했습니다: %w", err)
		}
		url := "http://" + existing
		fmt.Printf("이미 실행 중입니다 → %s (%s 로 이동)\n", url, repo.Short(sha))
		if !*noBrowser && !*printURL {
			openWindow(url, windowProfileDir(repo.Root))
		}
		return nil
	}
	defer inst.Release()

	srv := &http.Server{Handler: server.New(repo, sha, *rev != "")}
	url := inst.URL()

	if *printURL {
		fmt.Println(url)
		return nil
	}

	fmt.Printf("mgit  %s\n", repo.Root)
	fmt.Printf("커밋  %s  %s\n", repo.Short(sha), repo.Subject(sha))
	fmt.Printf("주소  %s\n\n", url)
	fmt.Println("종료하려면 Ctrl+C")

	errc := make(chan error, 1)
	go func() {
		if err := srv.Serve(inst.Listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()

	if !*noBrowser {
		openBrowser(url)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		fmt.Println("\n종료합니다.")
	}

	shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return srv.Shutdown(shutCtx)
}

// openBrowser 는 기본 브라우저로 주소를 연다. 실패해도 치명적이지 않으므로
// 주소를 이미 출력해 둔 것으로 충분하다.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		// rundll32 은 & 가 든 주소도 그대로 넘긴다. cmd /c start 는 & 를 잘라먹는다.
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "브라우저를 열지 못했습니다: %v\n", err)
		return
	}
	go cmd.Wait() // 좀비 프로세스 방지
}
