package gitx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// testRepo 는 머지가 하나 있는 작은 저장소를 임시 디렉토리에 만든다.
//
//	M  (merge: B, F)
//	|\
//	B | main 에서 이어진 커밋
//	| F feature 브랜치 커밋
//	|/
//	A  최초 커밋 (tag: v1)
func testRepo(t *testing.T) *Repo {
	t.Helper()
	dir := t.TempDir()

	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		// 커밋 해시를 재현 가능하게 만들고 사용자 설정에 영향받지 않게 한다.
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=tester", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=tester", "GIT_COMMITTER_EMAIL=t@example.com",
			"GIT_AUTHOR_DATE=2026-01-02T03:04:05+09:00",
			"GIT_COMMITTER_DATE=2026-01-02T03:04:05+09:00",
			"GIT_CONFIG_GLOBAL="+filepath.Join(dir, "nonexistent-gitconfig"),
			"GIT_CONFIG_SYSTEM="+filepath.Join(dir, "nonexistent-gitconfig"),
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	git("init", "-q", "-b", "main", ".")
	write("Foo.go", "package foo\n\nfunc A() int {\n\treturn 1\n}\n")
	git("add", "-A")
	git("commit", "-qm", "feat: A 추가")
	git("tag", "v1")

	git("checkout", "-q", "-b", "feature")
	write("Foo.go", "package foo\n\nfunc A() int {\n\treturn 2\n}\n")
	git("commit", "-qam", "fix: A 반환값 수정")

	git("checkout", "-q", "main")
	write("Bar.go", "package foo\n\nfunc B() {}\n")
	git("add", "-A")
	git("commit", "-qm", "feat: B 추가")

	git("merge", "-q", "--no-ff", "-m", "Merge branch 'feature'", "feature")

	repo, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

func TestOpenRejectsNonRepo(t *testing.T) {
	if _, err := Open(t.TempDir()); err == nil {
		t.Fatal("git 저장소가 아닌 경로에서 오류를 기대했다")
	}
}

func TestResolveCommit(t *testing.T) {
	r := testRepo(t)

	head, err := r.ResolveCommit("HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if len(head) != 40 {
		t.Errorf("HEAD SHA 길이 = %d, want 40", len(head))
	}

	// 태그, 상대 리비전, 짧은 SHA 를 git 에 그대로 위임해야 한다.
	tag, err := r.ResolveCommit("v1")
	if err != nil {
		t.Fatalf("태그 해석 실패: %v", err)
	}
	short, err := r.ResolveCommit(tag[:8])
	if err != nil {
		t.Fatalf("짧은 SHA 해석 실패: %v", err)
	}
	if short != tag {
		t.Errorf("짧은 SHA = %s, want %s", short, tag)
	}
	if _, err := r.ResolveCommit("main~1"); err != nil {
		t.Errorf("상대 리비전 해석 실패: %v", err)
	}

	// 빈 문자열은 HEAD 로 본다.
	if got, err := r.ResolveCommit(""); err != nil || got != head {
		t.Errorf("빈 리비전 = %s (%v), want HEAD %s", got, err, head)
	}
	if _, err := r.ResolveCommit("no-such-rev"); err == nil {
		t.Error("없는 리비전에서 오류를 기대했다")
	}
}

func TestLogTopoOrderAndParents(t *testing.T) {
	r := testRepo(t)
	commits, err := r.Log(LogOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 4 {
		t.Fatalf("커밋 수 = %d, want 4", len(commits))
	}

	// 첫 커밋은 머지여야 한다.
	if !commits[0].IsMerge() {
		t.Errorf("첫 커밋이 머지가 아니다: %+v", commits[0])
	}
	if len(commits[0].Parents) != 2 {
		t.Fatalf("머지 부모 수 = %d, want 2", len(commits[0].Parents))
	}

	// 위상 정렬: 모든 부모는 자식보다 뒤에 나와야 한다.
	// 레인 배치 알고리즘 전체가 이 전제 위에 서 있다.
	pos := map[string]int{}
	for i, c := range commits {
		pos[c.SHA] = i
	}
	for i, c := range commits {
		for _, p := range c.Parents {
			if j, ok := pos[p]; ok && j <= i {
				t.Errorf("%s(row %d) 의 부모 %s 가 row %d 에 있다 — 위상 정렬 위반",
					c.Short, i, p[:8], j)
			}
		}
	}

	// 메타데이터가 채워져야 한다.
	last := commits[len(commits)-1]
	if last.Author != "tester" {
		t.Errorf("작성자 = %q, want tester", last.Author)
	}
	if last.Date.IsZero() {
		t.Error("날짜가 비어 있다")
	}
	if last.Short == "" || len(last.Short) > 9 {
		t.Errorf("짧은 SHA = %q", last.Short)
	}
}

func TestLogParsesRefs(t *testing.T) {
	r := testRepo(t)
	commits, err := r.Log(LogOptions{})
	if err != nil {
		t.Fatal(err)
	}

	var sawTag, sawHead bool
	for _, c := range commits {
		for _, ref := range c.Refs {
			switch {
			case ref.Kind == "tag" && ref.Name == "v1":
				sawTag = true
			case ref.Kind == "head":
				sawHead = true
			}
		}
	}
	if !sawTag {
		t.Error("태그 v1 을 찾지 못했다")
	}
	if !sawHead {
		t.Error("HEAD ref 를 찾지 못했다")
	}
}

func TestLogLimit(t *testing.T) {
	r := testRepo(t)
	commits, err := r.Log(LogOptions{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 2 {
		t.Errorf("커밋 수 = %d, want 2", len(commits))
	}
}

func TestDiffAgainstFirstParent(t *testing.T) {
	r := testRepo(t)
	commits, _ := r.Log(LogOptions{})

	// 일반 커밋: Bar.go 가 새로 추가된 커밋을 찾는다.
	var barSHA string
	for _, c := range commits {
		if strings.Contains(c.Subject, "B 추가") {
			barSHA = c.SHA
		}
	}
	if barSHA == "" {
		t.Fatal("B 추가 커밋을 찾지 못했다")
	}

	files, err := r.Diff(barSHA, "", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Path != "Bar.go" {
		t.Fatalf("diff 파일 = %+v, want Bar.go 하나", files)
	}
	if files[0].Add == 0 {
		t.Error("추가된 줄이 0 이다")
	}
}

// 머지 커밋은 결합 diff 가 아니라 첫 부모 기준이어야 한다.
// 결합 diff 는 줄 번호가 모호해 메모 앵커로 쓸 수 없다.
func TestDiffOfMergeUsesFirstParent(t *testing.T) {
	r := testRepo(t)
	commits, _ := r.Log(LogOptions{})
	merge := commits[0]
	if !merge.IsMerge() {
		t.Fatalf("첫 커밋이 머지가 아니다")
	}

	files, err := r.Diff(merge.SHA, "", 3)
	if err != nil {
		t.Fatal(err)
	}
	// main 쪽(첫 부모)에서 보면 feature 의 Foo.go 변경만 들어온다.
	if len(files) != 1 || files[0].Path != "Foo.go" {
		t.Fatalf("머지 diff = %+v, want Foo.go 하나", files)
	}
	// 줄 번호가 실제로 매겨졌는지 확인한다.
	var haveNumbered bool
	for _, h := range files[0].Hunks {
		for _, l := range h.Lines {
			if l.NewNo > 0 || l.OldNo > 0 {
				haveNumbered = true
			}
		}
	}
	if !haveNumbered {
		t.Error("줄 번호가 하나도 매겨지지 않았다")
	}
}

func TestNumStat(t *testing.T) {
	r := testRepo(t)
	head, _ := r.ResolveCommit("HEAD")
	stats, err := r.NumStat(head)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 || stats[0].Path != "Foo.go" {
		t.Fatalf("numstat = %+v, want Foo.go 하나", stats)
	}
	if stats[0].Add != 1 || stats[0].Del != 1 {
		t.Errorf("Foo.go +%d/-%d, want +1/-1", stats[0].Add, stats[0].Del)
	}
}

// 루트 커밋은 부모가 없어 빈 트리와 비교해야 한다. 여기서 터지면
// 새 저장소 첫 커밋을 열 때마다 실패한다.
func TestDiffAndNumStatOfRootCommit(t *testing.T) {
	r := testRepo(t)
	root, err := r.ResolveCommit("v1")
	if err != nil {
		t.Fatal(err)
	}

	stats, err := r.NumStat(root)
	if err != nil {
		t.Fatalf("루트 커밋 numstat 실패: %v", err)
	}
	if len(stats) != 1 || stats[0].Path != "Foo.go" {
		t.Errorf("루트 numstat = %+v, want Foo.go", stats)
	}

	files, err := r.Diff(root, "", 3)
	if err != nil {
		t.Fatalf("루트 커밋 diff 실패: %v", err)
	}
	if len(files) != 1 || files[0].Add == 0 {
		t.Errorf("루트 diff = %+v, want 추가된 줄이 있는 파일 하나", files)
	}
}

func TestFileLines(t *testing.T) {
	r := testRepo(t)
	root, _ := r.ResolveCommit("v1")

	lines, err := r.FileLines(root, "Foo.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) < 4 {
		t.Fatalf("줄 수 = %d, want >= 4", len(lines))
	}
	if lines[0] != "package foo" {
		t.Errorf("첫 줄 = %q, want %q", lines[0], "package foo")
	}
	// 최초 커밋 시점 값이어야 한다 (이후 커밋에서 2 로 바뀜).
	if !strings.Contains(strings.Join(lines, "\n"), "return 1") {
		t.Error("최초 커밋 시점 내용이 아니다")
	}

	if _, err := r.FileLines(root, "NoSuch.go"); err == nil {
		t.Error("없는 파일에서 오류를 기대했다")
	}
}

func TestRelPath(t *testing.T) {
	r := testRepo(t)

	// 상대경로는 슬래시로 정규화만 한다.
	if got, err := r.RelPath("src/Foo.java"); err != nil || got != "src/Foo.java" {
		t.Errorf("RelPath(상대) = %q (%v)", got, err)
	}
	// 저장소 안 절대경로는 상대경로가 된다.
	abs := filepath.Join(r.Root, "Foo.go")
	if got, err := r.RelPath(abs); err != nil || got != "Foo.go" {
		t.Errorf("RelPath(절대) = %q (%v), want Foo.go", got, err)
	}
	// 저장소 바깥은 거부한다.
	outside := filepath.Join(filepath.Dir(r.Root), "outside.txt")
	if _, err := r.RelPath(outside); err == nil {
		t.Error("저장소 바깥 경로에서 오류를 기대했다")
	}
}

func TestSubjectAndShort(t *testing.T) {
	r := testRepo(t)
	head, _ := r.ResolveCommit("HEAD")

	if got := r.Subject(head); !strings.Contains(got, "Merge branch") {
		t.Errorf("Subject = %q, want Merge branch 포함", got)
	}
	short := r.Short(head)
	if short == "" || len(short) >= 40 {
		t.Errorf("Short = %q", short)
	}
	if !strings.HasPrefix(head, short) {
		t.Errorf("Short %q 가 %q 의 접두가 아니다", short, head)
	}
}

func TestGitDir(t *testing.T) {
	r := testRepo(t)
	gd, err := r.GitDir()
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(gd); err != nil || !fi.IsDir() {
		t.Errorf("GitDir = %q, 디렉토리가 아니다 (%v)", gd, err)
	}
}
