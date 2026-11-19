// Package platform 은 OS 별 기능(작업 스케줄러 등록, 토스트 알림, 폴더 열기)을 감싼다.
package platform

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// 데이터 폴더를 정한 방법
const (
	SourceEnv      = "env"      // 환경변수 DPR_DATA_DIR (개발·지원용)
	SourcePortable = "portable" // DPR.exe 옆 dpr-data 폴더 (USB 휴대용)
	SourceCustom   = "custom"   // 설정에서 옮긴 위치 (기본 위치 옆 DPR-data-location.txt 가 가리킴)
	SourceDefault  = "default"  // 기본 위치 (Windows: %APPDATA%\DPR, macOS: ~/Library/Application Support/DPR)
)

// ConfigBase : 기본 데이터 폴더의 부모 폴더 (테스트에서 바꾼다)
var ConfigBase = os.UserConfigDir

const (
	locationFileName   = "DPR-data-location.txt" // 기본 데이터 폴더(DPR) 옆 — 그 폴더를 지워도 남는다
	legacyLocationName = "data-location.txt"     // v0.2.1 까지: 기본 데이터 폴더 안 (읽기만 — 다음에 기록할 때 옮긴다)
)

// DefaultDataDir : 기본 데이터 폴더
func DefaultDataDir() string {
	if base, err := ConfigBase(); err == nil && base != "" {
		return filepath.Join(base, "DPR")
	}
	return filepath.Join(".", "dpr-data")
}

// LocationFile : 옮긴 데이터 폴더 위치를 적어 두는 파일. 기본 데이터 폴더 '옆'(같은 부모 폴더)에 둔다 —
// 옮긴 뒤 원래 폴더(기본 위치)를 지워도 위치를 잃지 않게. 앱과 작업 스케줄러가 같은 곳을 찾는다.
func LocationFile() string { return filepath.Join(filepath.Dir(DefaultDataDir()), locationFileName) }

// legacyLocationFile : v0.2.1 까지 쓰던 위치 기록 (기본 데이터 폴더 안)
func legacyLocationFile() string { return filepath.Join(DefaultDataDir(), legacyLocationName) }

// DataDirMissingError : 옮긴 데이터 폴더를 찾을 수 없음 (USB·네트워크 드라이브가 빠진 경우 등).
// 이때 기본 위치의 빈 DB 를 새로 열면 자료가 둘로 갈라지므로 시작하지 않는다.
type DataDirMissingError struct{ Dir, Pointer string }

func (e *DataDirMissingError) Error() string {
	return fmt.Sprintf("데이터 폴더를 찾을 수 없습니다:\n%s\n\n"+
		"드라이브(USB·외장 디스크)가 연결되어 있는지 확인한 뒤 다시 켜 주세요.\n\n"+
		"기본 위치(%s)로 돌아가려면 아래 파일을 지운 뒤 다시 켜세요. 기본 위치에는 옮기기 전의 옛 자료가 있거나 비어 있습니다 — "+
		"옮긴 폴더를 되찾을 수 없을 때만 지우고, 백업 폴더의 가장 최근 백업으로 되살리세요:\n%s", e.Dir, DefaultDataDir(), e.Pointer)
}

// Locate 는 데이터 폴더와 정한 방법을 돌려준다.
//  1. 환경변수 DPR_DATA_DIR
//  2. 실행 파일 옆에 'dpr-data' 폴더가 있으면 그 폴더 (USB 등 휴대용 실행)
//  3. 위치 기록(DPR-data-location.txt, 옛 버전은 기본 위치 안 data-location.txt)이 가리키는 폴더 (설정에서 옮긴 경우) — 없으면 오류
//  4. 기본 위치
func Locate() (dir, source string, err error) {
	if d := os.Getenv("DPR_DATA_DIR"); d != "" {
		return d, SourceEnv, nil
	}
	if exe, err := os.Executable(); err == nil {
		portable := filepath.Join(filepath.Dir(exe), "dpr-data")
		if st, err := os.Stat(portable); err == nil && st.IsDir() {
			return portable, SourcePortable, nil
		}
	}
	def := DefaultDataDir()
	if p, file, ok := readLocation(); ok {
		if !filepath.IsAbs(p) {
			return p, SourceCustom, &DataDirMissingError{Dir: p + " (전체 경로가 아님)", Pointer: file}
		}
		if st, err := os.Stat(p); err != nil || !st.IsDir() {
			return p, SourceCustom, &DataDirMissingError{Dir: p, Pointer: file}
		}
		return p, SourceCustom, nil
	}
	return def, SourceDefault, nil
}

// LocationFileInUse : 지금 데이터 폴더 위치를 읽은 기록 파일 (없으면 "")
func LocationFileInUse() string {
	_, file, _ := readLocation()
	return file
}

// readLocation : 위치 기록의 경로와 그 파일 (# 설명 줄·메모장 BOM 무시). 새 위치를 먼저, 없으면 옛 위치를 본다.
func readLocation() (dir, file string, ok bool) {
	for _, f := range []string{LocationFile(), legacyLocationFile()} {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, l := range strings.Split(strings.TrimPrefix(string(b), "\xef\xbb\xbf"), "\n") {
			l = strings.TrimSpace(l)
			if l != "" && !strings.HasPrefix(l, "#") {
				return l, f, true
			}
		}
	}
	return "", "", false
}

// DataDir 은 데이터 폴더 (정한 방법·오류가 필요 없을 때). 옮긴 폴더를 찾지 못하면 그 경로를 그대로 돌려준다.
func DataDir() string {
	d, _, _ := Locate()
	return d
}

// SetDataLocation 은 옮긴 데이터 폴더 위치를 기록한다. dir 이 "" 이면 기록을 지워 기본 위치로 돌아간다.
// 옛 버전의 기록(기본 위치 안)은 지운다 — 남아 있으면 새 기록을 지웠을 때 옛 위치로 돌아간다.
func SetDataLocation(dir string) error {
	removeLegacy := func() error {
		if err := os.Remove(legacyLocationFile()); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if dir == "" {
		if err := os.Remove(LocationFile()); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return removeLegacy()
	}
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("전체 경로가 아닙니다: %s", dir)
	}
	if err := os.MkdirAll(filepath.Dir(LocationFile()), 0o755); err != nil {
		return err
	}
	body := "# 급여약품 관리 데이터 폴더 위치. 이 파일을 지우면 기본 위치(" + DefaultDataDir() + ")의 자료로 돌아갑니다 —\n" +
		"# 그곳에는 옮기기 전의 옛 자료가 있거나 비어 있습니다.\n" + dir + "\n"
	tmp := LocationFile() + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, LocationFile()); err != nil {
		return err
	}
	return removeLegacy()
}

func IsWindows() bool { return runtime.GOOS == "windows" }
