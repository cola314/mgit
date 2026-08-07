package notes

import (
	"fmt"
	"io"
	"strings"
)

// Source 는 export 가 코드 문맥을 읽어오는 데 필요한 git 접근이다.
// *gitx.Repo 가 그대로 만족한다.
type Source interface {
	FileLines(commit, path string) ([]string, error)
	Short(sha string) string
}

// DefaultContext 는 메모 줄 위아래로 함께 보여줄 기본 줄 수다.
const DefaultContext = 3

// Export 는 에이전트 프롬프트에 그대로 붙여넣을 마크다운을 쓴다.
//
// 원시 JSON 을 던지는 것보다 코드 문맥을 인라인한 쪽이 훨씬 나은 프롬프트가 된다.
// 문맥은 앵커 커밋 시점의 파일에서 읽는다 — 그 시점이 메모가 실제로 가리키던 코드이고,
// 이후 코드가 바뀌어도 절대 어긋나지 않기 때문이다.
func Export(w io.Writer, list []*Note, src Source, ctx int) error {
	if ctx <= 0 {
		ctx = DefaultContext
	}
	if len(list) == 0 {
		_, err := fmt.Fprintln(w, "# 해당하는 메모가 없습니다.")
		return err
	}

	for i, n := range list {
		if i > 0 {
			fmt.Fprintln(w)
		}

		// 답글은 부모와 앵커가 같다. 헤더와 코드 문맥을 되풀이하지 않고 대화만 잇는다.
		if n.IsReply() {
			fmt.Fprintf(w, "↳ [%s] 답글 (→ %s)\n\n", n.ID, n.Parent)
			for _, line := range strings.Split(strings.TrimRight(n.Body, "\n"), "\n") {
				fmt.Fprintf(w, "> %s\n", line)
			}
			continue
		}

		short := n.Anchor.Commit
		if src != nil {
			short = src.Short(n.Anchor.Commit)
		}
		fmt.Fprintf(w, "## [%s] %s — %s\n", n.ID, n.Target(), n.Status)
		fmt.Fprintf(w, "커밋 %s", short)
		if n.Kind == KindLine {
			fmt.Fprintf(w, " · %s", n.Anchor.Path)
		}
		fmt.Fprintln(w)
		fmt.Fprintln(w)

		for _, line := range strings.Split(strings.TrimRight(n.Body, "\n"), "\n") {
			fmt.Fprintf(w, "> %s\n", line)
		}

		if n.Kind == KindLine && src != nil {
			if err := writeContext(w, n, src, ctx); err != nil {
				// 문맥을 못 읽어도 메모 자체는 내보낸다. 파일이 지워졌거나 이름이 바뀐 경우다.
				fmt.Fprintf(w, "\n<!-- 코드 문맥을 읽지 못했습니다: %v -->\n", err)
			}
		}

		if n.Status == StatusDone && n.Resolution != "" {
			fmt.Fprintf(w, "\n처리: %s\n", n.Resolution)
		}
	}
	return nil
}

// MaxRangeLines 는 범위 메모에서 그대로 펼쳐 보여줄 최대 줄 수다.
// 넘으면 앞뒤만 보여주고 가운데를 접는다 — 수백 줄을 프롬프트에 쏟지 않기 위해서다.
const MaxRangeLines = 40

func writeContext(w io.Writer, n *Note, src Source, ctx int) error {
	lines, err := src.FileLines(n.Anchor.Commit, n.Anchor.Path)
	if err != nil {
		return err
	}
	if n.Anchor.Line < 1 || n.Anchor.Line > len(lines) {
		return fmt.Errorf("%d번 줄이 파일 범위를 벗어납니다", n.Anchor.Line)
	}
	last := n.LastLine()
	if last > len(lines) {
		last = len(lines)
	}

	lo := n.Anchor.Line - ctx
	if lo < 1 {
		lo = 1
	}
	hi := last + ctx
	if hi > len(lines) {
		hi = len(lines)
	}
	width := len(fmt.Sprint(hi))

	// 범위가 너무 길면 가운데를 접는다. 접는 구간은 범위 안쪽만 건드린다.
	skipFrom, skipTo := 0, 0
	if last-n.Anchor.Line+1 > MaxRangeLines {
		skipFrom = n.Anchor.Line + MaxRangeLines*2/3
		skipTo = last - MaxRangeLines/3
	}

	fmt.Fprintf(w, "\n```%s\n", langOf(n.Anchor.Path))
	for i := lo; i <= hi; i++ {
		if skipFrom != 0 && i == skipFrom {
			fmt.Fprintf(w, "%*s  … (%d줄 생략)\n", width, "", skipTo-skipFrom+1)
		}
		if skipFrom != 0 && i >= skipFrom && i <= skipTo {
			continue
		}
		marker := ""
		switch {
		case !n.IsRange() && i == n.Anchor.Line:
			marker = "   ← 메모 위치"
		case n.IsRange() && i == n.Anchor.Line:
			marker = "   ← 메모 범위 시작"
		case n.IsRange() && i == last:
			marker = "   ← 메모 범위 끝"
		}
		fmt.Fprintf(w, "%*d| %s%s\n", width, i, strings.TrimRight(lines[i-1], " \t"), marker)
	}
	fmt.Fprintln(w, "```")

	// 현재 HEAD 에서 코드가 밀렸으면 알려준다. 에이전트가 옛 줄 번호로 헤매지 않도록.
	if cur, err := src.FileLines("HEAD", n.Anchor.Path); err == nil {
		if at, atEnd, d := n.LocateRange(cur); d == DriftMoved {
			if n.IsRange() {
				fmt.Fprintf(w, "\n※ 현재 HEAD 에서는 %d-%d번 줄로 이동했습니다.\n", at, atEnd)
			} else {
				fmt.Fprintf(w, "\n※ 현재 HEAD 에서는 %d번 줄로 이동했습니다.\n", at)
			}
		} else if d == DriftLost {
			fmt.Fprintf(w, "\n※ 현재 HEAD 에서는 이 코드를 찾지 못했습니다. 위 커밋 기준으로 판단하세요.\n")
		}
	}
	return nil
}

// langOf 는 확장자로 코드펜스 언어를 고른다.
func langOf(path string) string {
	i := strings.LastIndex(path, ".")
	if i < 0 {
		return ""
	}
	switch strings.ToLower(path[i+1:]) {
	case "java":
		return "java"
	case "go":
		return "go"
	case "ts", "tsx":
		return "typescript"
	case "js", "jsx":
		return "javascript"
	case "py":
		return "python"
	case "sql":
		return "sql"
	case "sh", "bash":
		return "bash"
	case "yml", "yaml":
		return "yaml"
	case "json":
		return "json"
	case "xml":
		return "xml"
	case "kt":
		return "kotlin"
	case "cs":
		return "csharp"
	default:
		return ""
	}
}
