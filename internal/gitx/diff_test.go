package gitx

import "testing"

// 실제 `git diff -U4` 출력에서 가져온 픽스처. git 없이 파서만 검증한다.
const diffFixture = `diff --git a/src/Foo.java b/src/Foo.java
index 4714c457b..e102191b6 100644
--- a/src/Foo.java
+++ b/src/Foo.java
@@ -5,6 +5,8 @@ import com.example.Base;
 import java.time.LocalDateTime;
+import java.time.ZoneId;
+import java.time.ZoneOffset;
 import java.util.List;

 public record Foo(List<Bar> bars) {
@@ -32,5 +34,6 @@ public record Foo(List<Bar> bars) {
     public String detail() {
-        return old(bars);
+        return next(bars);
     }
 }
diff --git a/README.md b/README.md
--- a/README.md
+++ b/README.md
@@ -1,3 +1,3 @@
-# 옛 제목
+# 새 제목

 본문
`

func TestParseUnifiedDiffFileSplit(t *testing.T) {
	files := ParseUnifiedDiff(diffFixture)
	if len(files) != 2 {
		t.Fatalf("파일 수 = %d, want 2", len(files))
	}
	if files[0].Path != "src/Foo.java" {
		t.Errorf("첫 파일 = %q, want src/Foo.java", files[0].Path)
	}
	if files[1].Path != "README.md" {
		t.Errorf("둘째 파일 = %q, want README.md", files[1].Path)
	}
}

func TestParseUnifiedDiffCounts(t *testing.T) {
	files := ParseUnifiedDiff(diffFixture)
	// Foo.java: +2 import, +1/-1 치환 → add 3, del 1
	if files[0].Add != 3 || files[0].Del != 1 {
		t.Errorf("Foo.java +%d/-%d, want +3/-1", files[0].Add, files[0].Del)
	}
	if files[1].Add != 1 || files[1].Del != 1 {
		t.Errorf("README.md +%d/-%d, want +1/-1", files[1].Add, files[1].Del)
	}
	if len(files[0].Hunks) != 2 {
		t.Errorf("Foo.java 헝크 수 = %d, want 2", len(files[0].Hunks))
	}
}

// 줄 번호가 어긋나면 메모 앵커가 통째로 틀어진다. 가장 중요한 검증이다.
func TestParseUnifiedDiffLineNumbers(t *testing.T) {
	files := ParseUnifiedDiff(diffFixture)
	h := files[0].Hunks[0]

	want := []struct {
		kind         string
		oldNo, newNo int
		text         string
	}{
		{LineCtx, 5, 5, "import java.time.LocalDateTime;"},
		{LineAdd, 0, 6, "import java.time.ZoneId;"},
		{LineAdd, 0, 7, "import java.time.ZoneOffset;"},
		{LineCtx, 6, 8, "import java.util.List;"},
		{LineCtx, 7, 9, ""},
		{LineCtx, 8, 10, "public record Foo(List<Bar> bars) {"},
	}
	if len(h.Lines) != len(want) {
		t.Fatalf("헝크 줄 수 = %d, want %d", len(h.Lines), len(want))
	}
	for i, w := range want {
		got := h.Lines[i]
		if got.Kind != w.kind || got.OldNo != w.oldNo || got.NewNo != w.newNo || got.Text != w.text {
			t.Errorf("줄 %d = %+v, want {%s %d %d %q}", i, got, w.kind, w.oldNo, w.newNo, w.text)
		}
	}
}

// 치환 헝크에서 삭제줄은 old 번호만, 추가줄은 new 번호만 가져야 한다.
func TestParseUnifiedDiffReplacement(t *testing.T) {
	files := ParseUnifiedDiff(diffFixture)
	h := files[0].Hunks[1]

	var del, add DiffLine
	for _, l := range h.Lines {
		switch l.Kind {
		case LineDel:
			del = l
		case LineAdd:
			add = l
		}
	}
	if del.OldNo != 33 || del.NewNo != 0 {
		t.Errorf("삭제줄 = old %d/new %d, want old 33/new 0", del.OldNo, del.NewNo)
	}
	if add.NewNo != 35 || add.OldNo != 0 {
		t.Errorf("추가줄 = old %d/new %d, want old 0/new 35", add.OldNo, add.NewNo)
	}
}

func TestParseUnifiedDiffNewAndDeletedFile(t *testing.T) {
	const fixture = `diff --git a/new.txt b/new.txt
new file mode 100644
index 0000000..3b18e51
--- /dev/null
+++ b/new.txt
@@ -0,0 +1,2 @@
+hello
+world
diff --git a/gone.txt b/gone.txt
deleted file mode 100644
--- a/gone.txt
+++ /dev/null
@@ -1,1 +0,0 @@
-bye
`
	files := ParseUnifiedDiff(fixture)
	if len(files) != 2 {
		t.Fatalf("파일 수 = %d, want 2", len(files))
	}

	// 새 파일: /dev/null 이 OldPath 를 비워야 하고 경로는 diff --git 헤더에서 온다.
	if files[0].Path != "new.txt" {
		t.Errorf("새 파일 경로 = %q, want new.txt", files[0].Path)
	}
	if files[0].Add != 2 || files[0].Del != 0 {
		t.Errorf("새 파일 +%d/-%d, want +2/-0", files[0].Add, files[0].Del)
	}
	// 시작이 0 인 헝크는 1 로 보정돼야 한다.
	if got := files[0].Hunks[0].Lines[0].NewNo; got != 1 {
		t.Errorf("새 파일 첫 줄 번호 = %d, want 1", got)
	}

	if files[1].Path != "gone.txt" {
		t.Errorf("삭제 파일 경로 = %q, want gone.txt", files[1].Path)
	}
	if files[1].Del != 1 {
		t.Errorf("삭제 파일 -%d, want -1", files[1].Del)
	}
}

func TestParseUnifiedDiffRename(t *testing.T) {
	const fixture = `diff --git a/old/Name.java b/new/Name.java
similarity index 92%
rename from old/Name.java
rename to new/Name.java
--- a/old/Name.java
+++ b/new/Name.java
@@ -1,2 +1,2 @@
 package x;
-// 옛 주석
+// 새 주석
`
	files := ParseUnifiedDiff(fixture)
	if len(files) != 1 {
		t.Fatalf("파일 수 = %d, want 1", len(files))
	}
	f := files[0]
	if f.Path != "new/Name.java" || f.OldPath != "old/Name.java" {
		t.Errorf("경로 = %q ← %q, want new/Name.java ← old/Name.java", f.Path, f.OldPath)
	}
	if !f.Renamed() {
		t.Error("Renamed() = false, want true")
	}
}

func TestParseUnifiedDiffBinary(t *testing.T) {
	const fixture = `diff --git a/logo.png b/logo.png
index a1b2c3d..d4e5f6a 100644
Binary files a/logo.png and b/logo.png differ
`
	files := ParseUnifiedDiff(fixture)
	if len(files) != 1 {
		t.Fatalf("파일 수 = %d, want 1", len(files))
	}
	if !files[0].Binary {
		t.Error("Binary = false, want true")
	}
	if len(files[0].Hunks) != 0 {
		t.Errorf("바이너리 파일에 헝크가 %d개 있다", len(files[0].Hunks))
	}
}

// "\ No newline at end of file" 은 줄 번호를 밀면 안 된다.
func TestParseUnifiedDiffNoNewlineMarker(t *testing.T) {
	const fixture = `diff --git a/a.txt b/a.txt
--- a/a.txt
+++ b/a.txt
@@ -1,2 +1,2 @@
 first
-second
\ No newline at end of file
+second!
\ No newline at end of file
`
	files := ParseUnifiedDiff(fixture)
	h := files[0].Hunks[0]
	if len(h.Lines) != 3 {
		t.Fatalf("줄 수 = %d, want 3 (마커는 제외)", len(h.Lines))
	}
	if h.Lines[2].Kind != LineAdd || h.Lines[2].NewNo != 2 {
		t.Errorf("마지막 줄 = %+v, want add/new 2", h.Lines[2])
	}
}

func TestParseUnifiedDiffEmpty(t *testing.T) {
	if got := ParseUnifiedDiff(""); len(got) != 0 {
		t.Errorf("빈 입력 = %d 파일, want 0", len(got))
	}
}

func TestParseHunkHeader(t *testing.T) {
	tests := []struct {
		in       string
		old, new int
		ok       bool
	}{
		{"@@ -5,6 +5,8 @@ import x;", 5, 5, true},
		{"@@ -1 +1 @@", 1, 1, true},
		{"@@ -0,0 +1,2 @@", 1, 1, true}, // 0 은 1 로 보정
		{"@@ -12,7 +34,9 @@", 12, 34, true},
		{"not a hunk", 0, 0, false},
	}
	for _, tt := range tests {
		o, n, ok := parseHunkHeader(tt.in)
		if ok != tt.ok || (ok && (o != tt.old || n != tt.new)) {
			t.Errorf("parseHunkHeader(%q) = %d,%d,%v want %d,%d,%v",
				tt.in, o, n, ok, tt.old, tt.new, tt.ok)
		}
	}
}
