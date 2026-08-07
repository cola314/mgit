// Command mgit 은 로컬 git 뷰어의 콘솔 진입점이다.
//
// 인자 없이 부르면 뷰어를 띄우고, note 하위 명령으로 코드 메모를 다룬다.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/cola314/mgit/internal/gitx"
	"github.com/cola314/mgit/internal/notes"
)

const version = "0.1.0"

const usage = `mgit — 로컬 git 뷰어 + 에이전트 메모

사용법:
  mgit [<경로>] [-c <리비전>]      뷰어를 연다 (브라우저)
      -no-browser                 브라우저를 자동으로 열지 않는다
      -print-url                  주소만 출력하고 종료한다

  mgit note add <경로>:<줄> -m <내용> [-c <리비전>]
  mgit note add -c <리비전> -m <내용>          커밋 전체에 대한 메모
  mgit note list [--status open|done|all] [--json]
  mgit note show <id>
  mgit note done <id> [-m <처리내용>]
  mgit note reopen <id>
  mgit note rm <id>
  mgit note export [--status open|all] [--context N]

리비전은 git 문법을 그대로 쓴다 (HEAD, HEAD~3, 태그, 짧은 SHA, 브랜치명).
메모는 <저장소>/.mgit/notes.jsonl 에 저장되고 .git/info/exclude 로 무시된다.
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "mgit: "+err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "-h", "--help", "help":
			fmt.Print(usage)
			return nil
		case "-v", "--version", "version":
			fmt.Println("mgit " + version)
			return nil
		case "note":
			return runNote(args[1:])
		}
	}
	return runOpen(args)
}

func runNote(args []string) error {
	if len(args) == 0 {
		fmt.Print(usage)
		return nil
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "add":
		return noteAdd(rest)
	case "list", "ls":
		return noteList(rest)
	case "show":
		return noteShow(rest)
	case "done":
		return noteDone(rest, true)
	case "reopen":
		return noteDone(rest, false)
	case "rm", "remove":
		return noteRemove(rest)
	case "export":
		return noteExport(rest)
	default:
		return fmt.Errorf("알 수 없는 note 하위 명령입니다: %s", sub)
	}
}

func noteAdd(args []string) error {
	fs := flag.NewFlagSet("note add", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	body := fs.String("m", "", "메모 내용")
	rev := fs.String("c", "HEAD", "앵커로 삼을 리비전")
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if strings.TrimSpace(*body) == "" {
		return fmt.Errorf("메모 내용을 -m 으로 지정하세요")
	}
	if len(pos) > 1 {
		return fmt.Errorf("위치는 하나만 지정할 수 있습니다")
	}

	repo, err := gitx.Open(".")
	if err != nil {
		return err
	}
	sha, err := repo.ResolveCommit(*rev)
	if err != nil {
		return err
	}
	st, err := notes.Open(repo.Root)
	if err != nil {
		return err
	}

	n := &notes.Note{
		ID:      st.NextID(),
		Body:    strings.TrimSpace(*body),
		Status:  notes.StatusOpen,
		Created: time.Now(),
	}

	if len(pos) == 0 {
		// 위치가 없으면 커밋 전체에 대한 메모다.
		n.Kind = notes.KindCommit
		n.Anchor = notes.Anchor{Commit: sha}
	} else {
		path, line, err := notes.ParseTarget(pos[0])
		if err != nil {
			return err
		}
		rel, err := repo.RelPath(path)
		if err != nil {
			return err
		}
		lines, err := repo.FileLines(sha, rel)
		if err != nil {
			return err
		}
		if line > len(lines) {
			return fmt.Errorf("%s 는 %d줄뿐입니다 (%d번 줄 지정)", rel, len(lines), line)
		}
		n.Kind = notes.KindLine
		n.Anchor = notes.Anchor{Commit: sha, Path: rel, Line: line}
		n.Fingerprint = notes.MakeFingerprint(lines, line)
	}

	st.Add(n)
	if err := st.Save(); err != nil {
		return err
	}
	fmt.Printf("%s 추가됨  %s @ %s\n", n.ID, n.Target(), repo.Short(sha))
	return nil
}

func noteList(args []string) error {
	fs := flag.NewFlagSet("note list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	status := fs.String("status", "all", "open | done | all")
	asJSON := fs.Bool("json", false, "JSON 배열로 출력 (에이전트용)")
	if _, err := parseMixed(fs, args); err != nil {
		return err
	}

	repo, st, err := openBoth()
	if err != nil {
		return err
	}
	list := st.Select(*status)

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		if list == nil {
			list = []*notes.Note{}
		}
		return enc.Encode(list)
	}

	if len(list) == 0 {
		fmt.Println("메모가 없습니다.")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\t상태\t커밋\t위치\t내용")
	for _, n := range list {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			n.ID, n.Status, repo.Short(n.Anchor.Commit), targetShort(n), firstLine(n.Body, 52))
	}
	return w.Flush()
}

func noteShow(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("메모 ID 를 지정하세요")
	}
	repo, st, err := openBoth()
	if err != nil {
		return err
	}
	n := st.Find(args[0])
	if n == nil {
		return fmt.Errorf("%s 메모를 찾을 수 없습니다", args[0])
	}
	return notes.Export(os.Stdout, []*notes.Note{n}, repo, notes.DefaultContext)
}

func noteDone(args []string, done bool) error {
	fs := flag.NewFlagSet("note done", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	res := fs.String("m", "", "처리 내용")
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return fmt.Errorf("메모 ID 를 지정하세요")
	}

	_, st, err := openBoth()
	if err != nil {
		return err
	}
	n := st.Find(pos[0])
	if n == nil {
		return fmt.Errorf("%s 메모를 찾을 수 없습니다", pos[0])
	}
	now := time.Now()
	n.Updated = &now
	if done {
		n.Status = notes.StatusDone
		if s := strings.TrimSpace(*res); s != "" {
			n.Resolution = s
		}
	} else {
		n.Status = notes.StatusOpen
		n.Resolution = ""
	}
	if err := st.Save(); err != nil {
		return err
	}
	fmt.Printf("%s → %s\n", n.ID, n.Status)
	return nil
}

func noteRemove(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("메모 ID 를 지정하세요")
	}
	_, st, err := openBoth()
	if err != nil {
		return err
	}
	if !st.Remove(args[0]) {
		return fmt.Errorf("%s 메모를 찾을 수 없습니다", args[0])
	}
	if err := st.Save(); err != nil {
		return err
	}
	fmt.Printf("%s 삭제됨\n", args[0])
	return nil
}

func noteExport(args []string) error {
	fs := flag.NewFlagSet("note export", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	status := fs.String("status", notes.StatusOpen, "open | done | all")
	ctx := fs.Int("context", notes.DefaultContext, "메모 줄 위아래로 보여줄 줄 수")
	if _, err := parseMixed(fs, args); err != nil {
		return err
	}
	repo, st, err := openBoth()
	if err != nil {
		return err
	}
	return notes.Export(os.Stdout, st.Select(*status), repo, *ctx)
}

// openBoth 는 현재 디렉토리의 저장소와 메모 스토어를 함께 연다.
func openBoth() (*gitx.Repo, *notes.Store, error) {
	repo, err := gitx.Open(".")
	if err != nil {
		return nil, nil, err
	}
	st, err := notes.Open(repo.Root)
	if err != nil {
		return nil, nil, err
	}
	return repo, st, nil
}

// parseMixed 는 위치 인자와 플래그가 섞여 있어도 파싱한다.
//
// 표준 flag 패키지는 첫 위치 인자에서 멈추기 때문에, 사람이 자연스럽게 쓰는
// `note add src/Foo.java:120 -m "..."` 순서를 그대로 받으려면 번갈아 파싱해야 한다.
func parseMixed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	for {
		rest := fs.Args()
		if len(rest) == 0 {
			return pos, nil
		}
		pos = append(pos, rest[0])
		if err := fs.Parse(rest[1:]); err != nil {
			return nil, err
		}
	}
}

func targetShort(n *notes.Note) string {
	if n.Kind == notes.KindCommit {
		return "커밋 전체"
	}
	return fmt.Sprintf("%s:%d", n.Base(), n.Anchor.Line)
}

func firstLine(s string, max int) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	r := []rune(s)
	if len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return s
}
