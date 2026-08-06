// Package gitx 는 git CLI 를 감싼 읽기 전용 래퍼다.
//
// go-git 대신 git 바이너리를 그대로 호출한다. credential helper, SSH config,
// LFS, submodule, sparse-checkout 을 전부 git 이 대신 처리해 주기 때문이다.
// 리비전 문법(HEAD~3, 태그, 짧은 SHA, @{u})도 rev-parse 에 위임해 공짜로 얻는다.
package gitx

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
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
