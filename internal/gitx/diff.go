package gitx

import (
	"strconv"
	"strings"
)

// 줄 종류.
const (
	LineCtx = "ctx"
	LineAdd = "add"
	LineDel = "del"
)

// DiffLine 은 diff 한 줄이다. OldNo/NewNo 는 해당 쪽에 없으면 0 이다.
type DiffLine struct {
	Kind  string `json:"kind"`
	OldNo int    `json:"oldNo,omitempty"`
	NewNo int    `json:"newNo,omitempty"`
	Text  string `json:"text"`
}

// Hunk 는 @@ 블록 하나다.
type Hunk struct {
	Header string     `json:"header"`
	Lines  []DiffLine `json:"lines"`
}

// FileDiff 는 파일 하나의 변경이다.
type FileDiff struct {
	Path    string `json:"path"`
	OldPath string `json:"oldPath,omitempty"`
	Binary  bool   `json:"binary"`
	// ForcedText 는 git 이 바이너리로 취급했지만 내용이 텍스트라 강제로 펼친 경우다.
	// (.gitattributes 의 `*.conf binary` 같은 선언 때문에 생긴다)
	ForcedText bool   `json:"forcedText,omitempty"`
	Add        int    `json:"add"`
	Del        int    `json:"del"`
	Hunks      []Hunk `json:"hunks"`
}

// Renamed 는 파일 경로가 바뀐 변경인지 알려준다.
func (f FileDiff) Renamed() bool { return f.OldPath != "" && f.OldPath != f.Path }

// DefaultDiffContext 는 diff 를 뽑을 때 기본 문맥 줄 수다.
const DefaultDiffContext = 4

// Diff 는 커밋의 변경 내용을 파일별로 읽는다.
//
// 머지 커밋은 첫 부모 기준으로 본다. git 기본값인 결합 diff(combined diff)는
// 사람이 읽기 어렵고 줄 번호가 모호해 메모 앵커로 쓸 수 없다.
func (r *Repo) Diff(sha, path string, ctxLines int) ([]FileDiff, error) {
	if ctxLines <= 0 {
		ctxLines = DefaultDiffContext
	}
	base, err := r.diffBase(sha)
	if err != nil {
		return nil, err
	}
	if base == "" {
		base = emptyTree
	}
	args := []string{
		"diff", "--no-color", "--no-ext-diff", "--textconv", "-M",
		"-U" + strconv.Itoa(ctxLines), base, sha,
	}
	if path != "" {
		args = append(args, "--", path)
	}
	out, err := r.Run(args...)
	if err != nil {
		return nil, err
	}
	files := ParseUnifiedDiff(out)
	r.unwrapFalseBinaries(files, base, sha, ctxLines)
	return files, nil
}

// unwrapFalseBinaries 는 "바이너리"로 나온 파일 중 실제로는 텍스트인 것을 펼친다.
//
// git 은 `.gitattributes` 에 `*.conf binary` 처럼 선언돼 있으면 내용과 무관하게
// diff 를 내주지 않는다. 사람이 읽어야 하는 설정 파일이 이렇게 묶여 있는 저장소가
// 흔해서, 그런 파일은 `--text` 로 다시 뽑아
// 보여준다. NUL 이 하나라도 있으면 진짜 바이너리로 보고 그대로 둔다.
func (r *Repo) unwrapFalseBinaries(files []FileDiff, base, sha string, ctxLines int) {
	for i := range files {
		if !files[i].Binary {
			continue
		}
		path := files[i].Path
		if path == "" {
			path = files[i].OldPath
		}
		if path == "" {
			continue
		}
		out, err := r.Run("diff", "--no-color", "--no-ext-diff", "--text", "-M",
			"-U"+strconv.Itoa(ctxLines), base, sha, "--", path)
		if err != nil {
			continue
		}
		got := ParseUnifiedDiff(out)
		if len(got) != 1 || len(got[0].Hunks) == 0 || hasNUL(got[0]) {
			continue
		}
		got[0].Binary = false
		got[0].ForcedText = true
		files[i] = got[0]
	}
}

// hasNUL 은 diff 본문에 NUL 이 섞였는지 본다. 있으면 텍스트로 보여줄 수 없다.
func hasNUL(f FileDiff) bool {
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			if strings.ContainsRune(l.Text, 0) {
				return true
			}
		}
	}
	return false
}

// ParseUnifiedDiff 는 `git diff` 의 통합 diff 출력을 구조체로 바꾼다.
//
// 파서를 따로 떼어낸 이유는 git 없이 픽스처만으로 테스트하기 위해서다.
func ParseUnifiedDiff(out string) []FileDiff {
	var (
		files []FileDiff
		cur   *FileDiff
		hunk  *Hunk
		oldNo int
		newNo int
	)

	flushHunk := func() {
		if cur != nil && hunk != nil {
			cur.Hunks = append(cur.Hunks, *hunk)
		}
		hunk = nil
	}
	flushFile := func() {
		flushHunk()
		if cur != nil {
			files = append(files, *cur)
		}
		cur = nil
	}

	// 출력 끝의 개행을 먼저 떼지 않으면 마지막 헝크에 빈 컨텍스트 줄이 하나 더 붙는다.
	out = strings.TrimSuffix(strings.TrimSuffix(out, "\n"), "\r")

	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimRight(raw, "\r")

		switch {
		case strings.HasPrefix(line, "diff --git "):
			flushFile()
			old, new_ := parseDiffGitHeader(line)
			cur = &FileDiff{Path: new_, OldPath: old}
			continue

		case cur == nil:
			// 헤더 앞의 잡음은 버린다.
			continue

		case strings.HasPrefix(line, "--- "):
			if p := stripDiffPath(strings.TrimPrefix(line, "--- ")); p != "" {
				cur.OldPath = p
			}
			continue

		case strings.HasPrefix(line, "+++ "):
			if p := stripDiffPath(strings.TrimPrefix(line, "+++ ")); p != "" {
				cur.Path = p
			}
			continue

		case strings.HasPrefix(line, "Binary files ") || strings.HasPrefix(line, "GIT binary patch"):
			cur.Binary = true
			continue

		case strings.HasPrefix(line, "@@"):
			flushHunk()
			o, n, ok := parseHunkHeader(line)
			if !ok {
				continue
			}
			oldNo, newNo = o, n
			hunk = &Hunk{Header: line}
			continue

		case hunk == nil:
			// index/mode/rename 등 헝크 밖 메타데이터.
			continue

		case strings.HasPrefix(line, `\`):
			// "\ No newline at end of file" — 줄 번호에 영향 없음.
			continue
		}

		// 여기부터는 헝크 본문이다.
		var kind, text string
		switch {
		case line == "":
			// 컨텍스트 빈 줄. git 은 보통 " " 로 주지만 공백이 잘려 오는 경로도 있어 함께 받는다.
			kind, text = LineCtx, ""
		case line[0] == '+':
			kind, text = LineAdd, line[1:]
		case line[0] == '-':
			kind, text = LineDel, line[1:]
		case line[0] == ' ':
			kind, text = LineCtx, line[1:]
		default:
			// 알 수 없는 줄이면 헝크가 끝난 것으로 본다.
			flushHunk()
			continue
		}

		dl := DiffLine{Kind: kind, Text: text}
		switch kind {
		case LineAdd:
			dl.NewNo = newNo
			newNo++
			cur.Add++
		case LineDel:
			dl.OldNo = oldNo
			oldNo++
			cur.Del++
		default:
			dl.OldNo, dl.NewNo = oldNo, newNo
			oldNo++
			newNo++
		}
		hunk.Lines = append(hunk.Lines, dl)
	}

	flushFile()
	return files
}

// parseDiffGitHeader 는 `diff --git a/x b/y` 에서 두 경로를 뽑는다.
//
// 공백이 든 경로는 이 헤더만으로 확정할 수 없어서, 뒤따르는 ---/+++ 줄이
// 최종적으로 값을 덮어쓴다. 여기서는 최선 추정만 한다.
func parseDiffGitHeader(line string) (old, new_ string) {
	rest := strings.TrimPrefix(line, "diff --git ")
	i := strings.Index(rest, " b/")
	if i < 0 {
		return "", ""
	}
	return stripDiffPath(rest[:i]), stripDiffPath(rest[i+1:])
}

// stripDiffPath 는 a/ b/ 접두와 타임스탬프를 떼고 /dev/null 을 빈 값으로 만든다.
func stripDiffPath(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\t'); i >= 0 {
		s = s[:i]
	}
	if s == "/dev/null" {
		return ""
	}
	if len(s) > 2 && (strings.HasPrefix(s, "a/") || strings.HasPrefix(s, "b/")) {
		return s[2:]
	}
	return s
}

// parseHunkHeader 는 "@@ -12,7 +12,9 @@ 문맥" 에서 시작 줄 번호를 뽑는다.
func parseHunkHeader(line string) (oldStart, newStart int, ok bool) {
	i := strings.Index(line, "@@")
	j := strings.Index(line[i+2:], "@@")
	if i < 0 || j < 0 {
		return 0, 0, false
	}
	spec := strings.TrimSpace(line[i+2 : i+2+j])
	var old, new_ string
	for _, part := range strings.Fields(spec) {
		switch {
		case strings.HasPrefix(part, "-"):
			old = part[1:]
		case strings.HasPrefix(part, "+"):
			new_ = part[1:]
		}
	}
	os_, ok1 := leadingInt(old)
	ns, ok2 := leadingInt(new_)
	if !ok1 || !ok2 {
		return 0, 0, false
	}
	// 길이가 0인 쪽(순수 추가/삭제)은 git 이 시작을 0 으로 주기도 한다. 1 로 보정한다.
	if os_ == 0 {
		os_ = 1
	}
	if ns == 0 {
		ns = 1
	}
	return os_, ns, true
}

// leadingInt 는 "12,7" 에서 12 를 뽑는다.
func leadingInt(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	if i := strings.IndexByte(s, ','); i >= 0 {
		s = s[:i]
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return v, true
}
