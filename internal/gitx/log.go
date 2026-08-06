package gitx

import (
	"strconv"
	"strings"
	"time"
)

// 레코드/필드 구분자로 ASCII 제어문자를 쓴다. 커밋 제목이나 ref 이름에
// 절대 들어갈 수 없는 값이라 파싱이 깨지지 않는다.
const (
	recSep = "\x1e"
	fldSep = "\x1f"
)

// logFormat 은 Commit 을 채우는 데 필요한 필드를 순서대로 뽑는다.
const logFormat = "%x1e%H" + fldSep + "%P" + fldSep + "%an" + fldSep + "%aI" + fldSep + "%D" + fldSep + "%s"

// Ref 는 커밋에 붙은 브랜치·태그 표시다.
type Ref struct {
	Kind string `json:"kind"` // "head" | "tag" | "branch" | "remote"
	Name string `json:"name"`
}

// Commit 은 그래프에 한 줄로 그려지는 커밋 하나다.
type Commit struct {
	SHA     string    `json:"sha"`
	Short   string    `json:"short"`
	Parents []string  `json:"parents"`
	Author  string    `json:"author"`
	Date    time.Time `json:"date"`
	Refs    []Ref     `json:"refs"`
	Subject string    `json:"subject"`
}

// IsMerge 는 부모가 둘 이상인지 알려준다.
func (c Commit) IsMerge() bool { return len(c.Parents) > 1 }

// LogOptions 는 Log 호출 조건이다.
type LogOptions struct {
	Limit int      // 0 이면 기본값(200)
	Revs  []string // 비어 있으면 --all
}

// Log 는 위상 정렬된 커밋 목록을 읽는다.
//
// 위상 정렬(--topo-order)이 중요하다. 레인 배치 알고리즘이 "부모는 항상 자식보다
// 뒤에 온다"는 전제 위에서 돌기 때문이다.
func (r *Repo) Log(opt LogOptions) ([]Commit, error) {
	limit := opt.Limit
	if limit <= 0 {
		limit = 200
	}
	args := []string{"log", "--topo-order", "--format=" + logFormat, "-n", strconv.Itoa(limit)}
	if len(opt.Revs) == 0 {
		args = append(args, "--all")
	} else {
		args = append(args, opt.Revs...)
	}
	out, err := r.Run(args...)
	if err != nil {
		return nil, err
	}
	return parseLog(out), nil
}

func parseLog(out string) []Commit {
	var commits []Commit
	for _, rec := range strings.Split(out, recSep) {
		rec = strings.Trim(rec, "\r\n")
		if rec == "" {
			continue
		}
		f := strings.Split(rec, fldSep)
		if len(f) < 6 {
			continue
		}
		c := Commit{
			SHA:     f[0],
			Author:  f[2],
			Refs:    parseRefs(f[4]),
			Subject: f[5],
		}
		if len(c.SHA) >= 9 {
			c.Short = c.SHA[:9]
		} else {
			c.Short = c.SHA
		}
		if p := strings.Fields(f[1]); len(p) > 0 {
			c.Parents = p
		}
		if t, err := time.Parse(time.RFC3339, f[3]); err == nil {
			c.Date = t
		}
		commits = append(commits, c)
	}
	return commits
}

// parseRefs 는 %D 의 출력("HEAD -> main, tag: v1.0, origin/main")을 쪼갠다.
func parseRefs(s string) []Ref {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var refs []Ref
	for _, part := range strings.Split(s, ", ") {
		part = strings.TrimSpace(part)
		switch {
		case part == "":
			continue
		case strings.HasPrefix(part, "HEAD -> "):
			refs = append(refs, Ref{Kind: "head", Name: strings.TrimPrefix(part, "HEAD -> ")})
		case part == "HEAD":
			refs = append(refs, Ref{Kind: "head", Name: "HEAD"})
		case strings.HasPrefix(part, "tag: "):
			refs = append(refs, Ref{Kind: "tag", Name: strings.TrimPrefix(part, "tag: ")})
		case strings.HasPrefix(part, "origin/") || strings.Contains(part, "/HEAD"):
			refs = append(refs, Ref{Kind: "remote", Name: part})
		default:
			refs = append(refs, Ref{Kind: "branch", Name: part})
		}
	}
	return refs
}

// FileStat 은 커밋 한 건이 파일 하나에 만든 변경량이다.
type FileStat struct {
	Path  string `json:"path"`
	Add   int    `json:"add"`
	Del   int    `json:"del"`
	Bin   bool   `json:"bin"`
	Renam string `json:"renamedFrom,omitempty"`
}

// NumStat 은 커밋의 파일별 증감을 읽는다. 머지 커밋은 첫 부모 기준이다.
func (r *Repo) NumStat(sha string) ([]FileStat, error) {
	base, err := r.diffBase(sha)
	if err != nil {
		return nil, err
	}
	args := []string{"diff", "--numstat", "--no-color", "-M"}
	if base == "" {
		// 루트 커밋: 빈 트리와 비교한다.
		args = append(args, emptyTree, sha)
	} else {
		args = append(args, base, sha)
	}
	out, err := r.Run(args...)
	if err != nil {
		return nil, err
	}
	return parseNumStat(out), nil
}

// emptyTree 는 git 의 고정된 빈 트리 오브젝트 해시다. 루트 커밋 diff 에 쓴다.
const emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

func parseNumStat(out string) []FileStat {
	var stats []FileStat
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		f := strings.SplitN(line, "\t", 3)
		if len(f) < 3 {
			continue
		}
		st := FileStat{Path: f[2]}
		if f[0] == "-" || f[1] == "-" {
			st.Bin = true
		} else {
			st.Add, _ = strconv.Atoi(f[0])
			st.Del, _ = strconv.Atoi(f[1])
		}
		// 이름이 바뀐 경우 git 은 "old => new" 또는 "{a => b}/c" 형태로 준다.
		if i := strings.Index(st.Path, " => "); i >= 0 {
			st.Renam = st.Path
		}
		stats = append(stats, st)
	}
	return stats
}

// diffBase 는 커밋의 diff 기준점(첫 부모)을 돌려준다. 루트 커밋이면 빈 문자열.
func (r *Repo) diffBase(sha string) (string, error) {
	out, err := r.Run("rev-list", "--parents", "-n", "1", sha)
	if err != nil {
		return "", err
	}
	f := strings.Fields(out)
	if len(f) < 2 {
		return "", nil // 부모 없음 = 루트 커밋
	}
	return f[1], nil
}
