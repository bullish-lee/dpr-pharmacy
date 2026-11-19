package store

import (
	"os"
	"path/filepath"
	"testing"
)

// 폴더 이름에 '#'·'%'·'?' 가 있어도 그 폴더의 파일을 연다 (예전에는 '약국#2' → '약국' 이라는 다른 파일을 만들었다)
func TestOpenOddPaths(t *testing.T) {
	for _, name := range []string{"약국#2", "x%41y", "100%진짜", "a b", "물음?표", "a#b%41c?"} {
		parent := t.TempDir()
		dir := filepath.Join(parent, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Skipf("%q: 이 OS 에서 만들 수 없는 이름: %v", name, err)
		}
		path := filepath.Join(dir, "dpr.db")
		db, err := Open(path)
		if err != nil {
			t.Fatalf("%q: %v", name, err)
		}
		if err := db.SetSetting("probe", name); err != nil {
			t.Fatal(err)
		}
		db.Close()
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%q: 고른 경로에 파일이 없습니다: %v", name, err)
		}
		entries, _ := os.ReadDir(parent)
		if len(entries) != 1 {
			var names []string
			for _, e := range entries {
				names = append(names, e.Name())
			}
			t.Fatalf("%q: 다른 파일이 생겼습니다: %v", name, names)
		}
		db, err = Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := db.GetSetting("probe", ""); got != name {
			t.Fatalf("%q: 다시 열었더니 %q", name, got)
		}
		db.Close()
	}
}

func TestFileURI(t *testing.T) {
	cases := map[string]string{
		`C:\Users\약국\DPR\dpr.db`:       "file:C:/Users/약국/DPR/dpr.db",
		`D:\약국#2\dpr.db`:               "file:D:/약국%232/dpr.db",
		`\\server\share\DPR\dpr.db`:    "file:////server/share/DPR/dpr.db",
		"/Users/a/100%/x?y/dpr.db":     "file:/Users/a/100%25/x%3Fy/dpr.db",
		"/Users/a/x%41y/dpr.db":        "file:/Users/a/x%2541y/dpr.db",
		"/Users/a/Application Support": "file:/Users/a/Application Support",
	}
	for in, want := range cases {
		if got := fileURI(in); got != want {
			t.Errorf("fileURI(%q) = %q, want %q", in, got, want)
		}
	}
}
