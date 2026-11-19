package platform

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func withBase(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	old := ConfigBase
	ConfigBase = func() (string, error) { return base, nil }
	t.Cleanup(func() { ConfigBase = old })
	t.Setenv("DPR_DATA_DIR", "")
	return base
}

// 옮긴 데이터 폴더를 찾지 못하면(드라이브가 빠짐) 기본 위치의 빈 자료로 열지 않고 오류
func TestLocateFailsClosedWhenMovedFolderMissing(t *testing.T) {
	base := withBase(t)
	if d, src, err := Locate(); err != nil || src != SourceDefault || d != filepath.Join(base, "DPR") {
		t.Fatalf("기본: %s %s %v", d, src, err)
	}
	moved := filepath.Join(t.TempDir(), "자료 폴더")
	if err := os.MkdirAll(moved, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := SetDataLocation(moved); err != nil {
		t.Fatal(err)
	}
	if d, src, err := Locate(); err != nil || src != SourceCustom || d != moved {
		t.Fatalf("옮긴 위치: %s %s %v", d, src, err)
	}
	_ = os.RemoveAll(moved) // 드라이브가 빠진 것처럼
	_, _, err := Locate()
	var miss *DataDirMissingError
	if !errors.As(err, &miss) || miss.Pointer != LocationFile() {
		t.Fatalf("없는 폴더는 오류여야 함 (기본 위치로 열면 안 됨): %v", err)
	}
	if err := SetDataLocation(""); err != nil {
		t.Fatal(err)
	}
	if _, src, err := Locate(); err != nil || src != SourceDefault {
		t.Fatalf("기록을 지우면 기본 위치: %s %v", src, err)
	}
	// 메모장으로 고친 파일 (BOM·설명 줄·CRLF)
	_ = os.MkdirAll(filepath.Join(base, "DPR"), 0o755)
	again := t.TempDir()
	_ = os.WriteFile(LocationFile(), []byte("\xef\xbb\xbf# 설명\r\n"+again+"\r\n"), 0o644)
	if d, _, err := Locate(); err != nil || d != again {
		t.Fatalf("메모장 파일: %q %v", d, err)
	}
}

// v0.2.1 까지는 위치 기록이 기본 데이터 폴더 안에 있었다 — 옮긴 뒤 원래 폴더를 지우면 기록도 사라져 빈 자료로 열렸다.
// 이제 기록은 기본 폴더 옆에 두고, 옛 기록도 읽는다.
func TestLocationFileOutsideDefaultFolder(t *testing.T) {
	base := withBase(t)
	def := filepath.Join(base, "DPR")
	if filepath.Dir(LocationFile()) != base || filepath.Base(LocationFile()) != "DPR-data-location.txt" {
		t.Fatalf("위치 기록은 기본 폴더 옆: %s", LocationFile())
	}
	// 옛 버전이 남긴 기록만 있음 → 그대로 따른다
	moved := t.TempDir()
	_ = os.MkdirAll(def, 0o755)
	legacy := filepath.Join(def, "data-location.txt")
	_ = os.WriteFile(legacy, []byte("# 옛 기록\n"+moved+"\n"), 0o644)
	if d, src, err := Locate(); err != nil || src != SourceCustom || d != moved || LocationFileInUse() != legacy {
		t.Fatalf("옛 기록: %s %s %v (%s)", d, src, err, LocationFileInUse())
	}
	// 다시 기록하면 새 위치로 옮기고 옛 기록은 지운다
	if err := SetDataLocation(moved); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacy); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("옛 기록이 남음: %v", err)
	}
	if LocationFileInUse() != LocationFile() {
		t.Fatalf("새 기록을 써야 함: %s", LocationFileInUse())
	}
	// 안내대로 원래(기본) 폴더를 통째로 지워도 옮긴 위치를 그대로 찾는다
	if err := os.RemoveAll(def); err != nil {
		t.Fatal(err)
	}
	if d, src, err := Locate(); err != nil || src != SourceCustom || d != moved {
		t.Fatalf("원래 폴더를 지운 뒤: %s %s %v", d, src, err)
	}
	// 기본 위치로 되돌리면 두 기록 모두 지운다
	_ = os.MkdirAll(def, 0o755)
	_ = os.WriteFile(legacy, []byte(moved+"\n"), 0o644)
	if err := SetDataLocation(""); err != nil {
		t.Fatal(err)
	}
	if _, src, err := Locate(); err != nil || src != SourceDefault || LocationFileInUse() != "" {
		t.Fatalf("되돌린 뒤: %s %v %q", src, err, LocationFileInUse())
	}
	// 찾지 못할 때 오류는 실제로 읽은 기록 파일을 가리킨다
	_ = os.WriteFile(legacy, []byte(filepath.Join(base, "없는 폴더")+"\n"), 0o644)
	var miss *DataDirMissingError
	if _, _, err := Locate(); !errors.As(err, &miss) || miss.Pointer != legacy {
		t.Fatalf("오류의 기록 파일: %v", err)
	}
}
