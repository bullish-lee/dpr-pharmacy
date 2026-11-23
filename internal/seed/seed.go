// Package seed 는 프로그램에 미리 넣어 두는 자료 — 지난 1년 치 복지부 약가 고시 목록표.
//
// 보건복지부 「약제 급여 목록 및 급여 상한금액표」 일부개정고시의 첨부 '별표1 목록표' 원본 그대로다 (고시는 저작권 보호 대상이 아닌 공공 자료).
// tools/noticeseed 로 받는다. 프로그램은 처음 켤 때 이 파일들을 자동 수집한 고시와 같은 방법으로 읽어 넣는다
// — 설치하자마자 지난 고시(인하·인상·신설·삭제·변경)가 보이고, 각 PC 가 복지부 게시판을 거슬러 받지 않아도 된다.
package seed

import (
	"embed"
	"encoding/json"
)

//go:embed notices/manifest.json notices/*.xlsx
var files embed.FS

// Notice : 미리 넣어 둔 고시 하나
type Notice struct {
	ListNo    string `json:"listNo"`    // 복지부 게시글 번호
	Published string `json:"published"` // 게시일
	Title     string `json:"title"`
	File      string `json:"file"`
	FileName  string `json:"fileName"` // 원래 첨부 파일 이름
	SHA256    string `json:"sha256"`
}

// Version : 넣어 둔 고시 묶음의 판 (바뀌면 프로그램이 다시 확인한다) — 고시를 더 넣으면 올린다
const Version = "2026-11-22"

// Notices 는 게시일 순(오래된 것부터) 목록.
func Notices() ([]Notice, error) {
	b, err := files.ReadFile("notices/manifest.json")
	if err != nil {
		return nil, err
	}
	var out []Notice
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Data 는 고시 목록표 파일 내용.
func Data(n Notice) ([]byte, error) { return files.ReadFile("notices/" + n.File) }
