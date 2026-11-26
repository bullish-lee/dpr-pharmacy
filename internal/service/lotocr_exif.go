package service

// 사진의 방향 정보(EXIF Orientation).
//
// 휴대폰 카메라는 세로로 찍은 사진을 가로로 누운 화소 그대로 저장하고 "보여 줄 때 돌려라"는 표시만 붙인다.
// 화면(브라우저)은 이 표시대로 바로 세워 보여 주지만 Go 의 image.Decode 는 표시를 보지 않는다 →
// 사용자 눈에는 바로 선 사진인데 글자 인식에는 누운 그림이 들어가 한 줄도 읽지 못한다.
// 거래명세서 같은 종이를 세로로 찍은 사진(휴대폰이 3.5MB 미만이라 그대로 보낸 것, PC 로 옮겨 끌어다 놓은 것)이 그렇다.
// 화면 캡처(PNG)나 메신저가 다시 저장한 사진에는 이 표시가 없어 아무 일도 하지 않는다.

import (
	"encoding/binary"
	"image"
	"image/draw"
)

// exifOrientation : JPEG 의 EXIF 방향 값(1~8). JPEG 이 아니거나 표시가 없거나 깨졌으면 1 (그대로).
// 휴대폰에서 받은 내용을 그대로 읽으므로 모든 위치를 길이와 견준다 (잘린 파일로 죽지 않게).
func exifOrientation(b []byte) int {
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
		return 1
	}
	for i := 2; i+4 <= len(b); {
		if b[i] != 0xFF {
			return 1
		}
		m := b[i+1]
		if m == 0xFF { // 채움 바이트
			i++
			continue
		}
		if m == 0xDA || m == 0xD9 { // 화소 자료가 시작됨 — 방향 표시는 그 앞에만 있다
			return 1
		}
		if m == 0x01 || (m >= 0xD0 && m <= 0xD7) { // 길이가 없는 표시
			i += 2
			continue
		}
		n := int(binary.BigEndian.Uint16(b[i+2:]))
		if n < 2 || i+2+n > len(b) {
			return 1
		}
		seg := b[i+4 : i+2+n]
		i += 2 + n
		if m != 0xE1 || len(seg) < 14 || string(seg[:6]) != "Exif\x00\x00" {
			continue
		}
		t := seg[6:] // TIFF
		var bo binary.ByteOrder
		switch string(t[:2]) {
		case "II":
			bo = binary.LittleEndian
		case "MM":
			bo = binary.BigEndian
		default:
			return 1
		}
		if bo.Uint16(t[2:]) != 42 {
			return 1
		}
		off := int(bo.Uint32(t[4:]))
		if off < 8 || off+2 > len(t) {
			return 1
		}
		cnt := int(bo.Uint16(t[off:]))
		for k := 0; k < cnt; k++ {
			e := off + 2 + k*12
			if e+12 > len(t) {
				return 1
			}
			if bo.Uint16(t[e:]) != 0x0112 { // Orientation
				continue
			}
			if bo.Uint16(t[e+2:]) != 3 || bo.Uint32(t[e+4:]) != 1 { // SHORT 1개
				return 1
			}
			if v := int(bo.Uint16(t[e+8:])); v >= 1 && v <= 8 {
				return v
			}
			return 1
		}
		return 1
	}
	return 1
}

// orientImage : EXIF 방향 값대로 그림을 바로 세운다 (1 이면 그대로 돌려준다).
//
//	2 좌우 뒤집기 · 3 180도 · 4 위아래 뒤집기 · 5 대각선 뒤집기 · 6 시계 방향 90도 · 7 반대 대각선 뒤집기 · 8 반시계 방향 90도
func orientImage(src image.Image, o int) image.Image {
	if o < 2 || o > 8 {
		return src
	}
	sb := src.Bounds()
	w, h := sb.Dx(), sb.Dy()
	in, ok := src.(*image.RGBA)
	if !ok || sb.Min != (image.Point{}) {
		in = image.NewRGBA(image.Rect(0, 0, w, h))
		draw.Draw(in, in.Bounds(), src, sb.Min, draw.Src)
	}
	W, H := w, h
	if o >= 5 {
		W, H = h, w
	}
	out := image.NewRGBA(image.Rect(0, 0, W, H))
	for y := 0; y < H; y++ {
		for x := 0; x < W; x++ {
			var sx, sy int
			switch o {
			case 2:
				sx, sy = w-1-x, y
			case 3:
				sx, sy = w-1-x, h-1-y
			case 4:
				sx, sy = x, h-1-y
			case 5:
				sx, sy = y, x
			case 6:
				sx, sy = y, h-1-x
			case 7:
				sx, sy = w-1-y, h-1-x
			default: // 8
				sx, sy = w-1-y, x
			}
			copy(out.Pix[y*out.Stride+x*4:y*out.Stride+x*4+4], in.Pix[sy*in.Stride+sx*4:sy*in.Stride+sx*4+4])
		}
	}
	return out
}
