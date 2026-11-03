// Package gitx 는 git CLI 를 감싼 래퍼다. Fetch 를 빼면 전부 읽기 전용이다.
//
// go-git 대신 git 바이너리를 그대로 호출한다. credential helper, SSH config,
// LFS, submodule, sparse-checkout 을 전부 git 이 대신 처리해 주기 때문이다.
// 리비전 문법(HEAD~3, 태그, 짧은 SHA, @{u})도 rev-parse 에 위임해 공짜로 얻는다.
package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ErrNotARepo 는 대상 디렉토리가 git 워크트리가 아닐 때 반환된다.
var ErrNotARepo = errors.New("git 저장소가 아닙니다")

// Repo 는 특정 워크트리에 묶인 git 실행 컨텍스트다.
type Repo struct {
	Root string // 워크트리 최상위 절대경로
}

// Open 은 dir 이 속한 워크트리의 최상위를 찾아 Repo 를 만든다.
func Open(dir string) (*Repo, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	out, err := run(abs, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrNotARepo, abs)
	}
	// git 은 Windows 에서도 슬래시 경로를 뱉으므로 OS 경로로 정규화한다.
	return &Repo{Root: filepath.Clean(out)}, nil
}

// GitDir 은 .git 디렉토리 경로를 반환한다. 워크트리·서브모듈이면 파일이 아닌 실제 위치를 가리킨다.
func (r *Repo) GitDir() (string, error) {
	out, err := r.Run("rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", err
	}
	return filepath.Clean(out), nil
}

// ResolveCommit 은 리비전 문자열을 완전한 커밋 SHA 로 해석한다.
// HEAD~3, 태그, 브랜치, 짧은 SHA 등 git 이 아는 모든 문법을 그대로 받는다.
func (r *Repo) ResolveCommit(rev string) (string, error) {
	if strings.TrimSpace(rev) == "" {
		rev = "HEAD"
	}
	out, err := r.Run("rev-parse", "--verify", "--end-of-options", rev+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("리비전을 찾을 수 없습니다: %s", rev)
	}
	return out, nil
}

// Short 는 커밋 SHA 의 축약형을 반환한다.
func (r *Repo) Short(sha string) string {
	out, err := r.Run("rev-parse", "--short", sha)
	if err != nil {
		if len(sha) > 9 {
			return sha[:9]
		}
		return sha
	}
	return out
}

// FileLines 는 특정 커밋 시점의 파일 내용을 줄 단위로 읽는다.
// 반환 슬라이스의 인덱스 i 는 파일의 i+1 번째 줄에 대응한다.
func (r *Repo) FileLines(commit, path string) ([]string, error) {
	// git 은 트리 경로를 항상 슬래시로 받는다. Windows 경로가 들어와도 맞춰준다.
	spec := commit + ":" + filepath.ToSlash(path)
	out, err := r.Run("show", "--textconv", spec)
	if err != nil {
		return nil, fmt.Errorf("%s 커밋에 %s 파일이 없습니다", r.Short(commit), path)
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n"), nil
}

// RelPath 는 임의의 경로를 워크트리 기준 상대경로(슬래시 구분)로 바꾼다.
// 이미 상대경로면 그대로 정규화만 한다.
func (r *Repo) RelPath(path string) (string, error) {
	p := filepath.FromSlash(path)
	if !filepath.IsAbs(p) {
		return filepath.ToSlash(filepath.Clean(p)), nil
	}
	rel, err := filepath.Rel(r.Root, p)
	if err != nil {
		return "", fmt.Errorf("%s 는 저장소 바깥입니다", path)
	}
	if strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("%s 는 저장소 바깥입니다", path)
	}
	return filepath.ToSlash(rel), nil
}

// Subject 는 커밋 제목 한 줄을 반환한다.
func (r *Repo) Subject(commit string) string {
	out, err := r.Run("log", "-1", "--format=%s", commit)
	if err != nil {
		return ""
	}
	return out
}

// Run 은 이 저장소에서 git 을 실행하고 표준출력을 트림해 돌려준다.
func (r *Repo) Run(args ...string) (string, error) {
	return run(r.Root, args...)
}

// RemoteRefs 는 remote-tracking ref 와 태그의 현재 상태를 한 덩어리로 돌려준다.
// Fetch 전후로 비교해 실제로 받아온 게 있는지 판단하는 데 쓴다.
func (r *Repo) RemoteRefs() string {
	out, err := r.Run("for-each-ref", "--format=%(refname) %(objectname)", "refs/remotes", "refs/tags")
	if err != nil {
		return ""
	}
	return out
}

// Fetch 는 원격에서 ref 를 받아온다.
//
// 이 패키지의 유일한 네트워크 작업이고, .git 을 건드리는 유일한 작업이다.
// remote-tracking ref 와 FETCH_HEAD 만 갱신하며 워킹트리와 로컬 브랜치는 그대로다.
// 서브모듈은 따라가지 않는다 — 뷰어는 상위 저장소 그래프만 보여주는데 서브모듈
// fetch 는 느린 데다 포인터가 원격에 없는 커밋을 가리키면 통째로 실패한다.
func (r *Repo) Fetch(timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "fetch", "--all", "--no-recurse-submodules")
	cmd.Dir = r.Root
	// 자격증명 프롬프트가 뜨면 응답 없이 영영 멈춘다. 묻지 말고 실패시킨다.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	// git fetch 는 요약을 stderr 로 쓴다. 성공해도 stdout 은 대개 비어 있다.
	out := strings.TrimSpace(stderr.String() + stdout.String())
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("원격이 %s 안에 응답하지 않았습니다", timeout)
	}
	if err != nil {
		if out == "" {
			out = err.Error()
		}
		return "", errors.New(out)
	}
	return out, nil
}

func run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(msg)
	}
	return strings.TrimRight(stdout.String(), "\r\n"), nil
}
