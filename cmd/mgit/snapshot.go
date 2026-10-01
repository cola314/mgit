package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/cola314/mgit/internal/gitx"
	"github.com/cola314/mgit/internal/server"
)

// revList 는 -c 를 여러 번 받는다.
type revList []string

func (r *revList) String() string     { return fmt.Sprint(*r) }
func (r *revList) Set(v string) error { *r = append(*r, v); return nil }

// runSnapshot 은 지정한 커밋들을 서버 없이 열리는 HTML 한 장으로 저장한다.
func runSnapshot(args []string) error {
	fs := flag.NewFlagSet("mgit snapshot", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var revs revList
	fs.Var(&revs, "c", "담을 리비전 (여러 번 지정 가능, 첫 번째가 처음 열리는 커밋)")
	out := fs.String("o", "", "저장할 HTML 경로")

	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if *out == "" {
		return fmt.Errorf("-o <파일> 이 필요합니다")
	}
	dir := "."
	if len(pos) > 0 {
		dir = pos[0]
	}
	repo, err := gitx.Open(dir)
	if err != nil {
		return err
	}
	if len(revs) == 0 {
		revs = revList{"HEAD"}
	}
	shas := make([]string, 0, len(revs))
	for _, r := range revs {
		sha, err := repo.ResolveCommit(r)
		if err != nil {
			return err
		}
		shas = append(shas, sha)
	}

	page, err := server.New(repo, shas[0], true).Snapshot(shas)
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, page, 0o644); err != nil {
		return err
	}
	fmt.Printf("%s  (%d 커밋, %d KB)\n", *out, len(shas), len(page)/1024)
	return nil
}
