package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"math/rand"
	"os"
	"testing"

	"dpr/internal/platform"
)

// 방향 표시(EXIF Orientation)가 붙은 사진 — 가상 그림.
// 바로 선 그림은 240×80 흰 바탕, 왼쪽 위 모서리에 60×30 검은 칸. 방향 값마다 '저장된(누운) 그림'에서 그 칸이 놓이는 자리를 직접 적었다.
// 휴대폰 카메라가 실제로 쓰는 값은 1(가로)·3(거꾸로 든 가로)·6·8(세로)이다.
var exifStored = map[int]struct {
	w, h  int
	block image.Rectangle
}{
	2: {240, 80, image.Rect(180, 0, 240, 30)},  // 좌우 뒤집힘 → 오른쪽 위
	3: {240, 80, image.Rect(180, 50, 240, 80)}, // 180도 → 오른쪽 아래
	4: {240, 80, image.Rect(0, 50, 60, 80)},    // 위아래 뒤집힘 → 왼쪽 아래
	5: {80, 240, image.Rect(0, 0, 30, 60)},     // 대각선 뒤집힘
	6: {80, 240, image.Rect(0, 180, 30, 240)},  // 세로로 찍음 (가장 흔함) → 왼쪽 아래
	7: {80, 240, image.Rect(50, 180, 80, 240)}, // 반대 대각선 뒤집힘
	8: {80, 240, image.Rect(50, 0, 80, 60)},    // 반대쪽 세로 → 오른쪽 위
}

func exifBlockImage(w, h int, block image.Rectangle) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(img, block, image.NewUniform(color.Black), image.Point{}, draw.Src)
	return img
}

// exifJPEG : JPEG 로 저장하고 방향 값을 담은 EXIF 조각(APP1)을 맨 앞에 끼운다 (o = 0 이면 끼우지 않음)
func exifJPEG(t *testing.T, img image.Image, o int, bo binary.ByteOrder) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	if o == 0 {
		return b
	}
	tiff := make([]byte, 8+2+12+4)
	copy(tiff, "II")
	if bo == binary.BigEndian {
		copy(tiff, "MM")
	}
	bo.PutUint16(tiff[2:], 42)
	bo.PutUint32(tiff[4:], 8)
	bo.PutUint16(tiff[8:], 1)       // 항목 1개
	bo.PutUint16(tiff[10:], 0x0112) // Orientation
	bo.PutUint16(tiff[12:], 3)      // SHORT
	bo.PutUint32(tiff[14:], 1)
	bo.PutUint16(tiff[18:], uint16(o))
	body := append([]byte("Exif\x00\x00"), tiff...)
	seg := []byte{0xFF, 0xE1, byte((len(body) + 2) >> 8), byte(len(body) + 2)}
	out := append([]byte{}, b[:2]...)
	out = append(out, seg...)
	out = append(out, body...)
	return append(out, b[2:]...)
}

func dark(img image.Image, x, y int) bool {
	r, g, b, _ := img.At(img.Bounds().Min.X+x, img.Bounds().Min.Y+y).RGBA()
	return (r+g+b)/3 < 0x8000
}

// uprightAt : 바로 선 그림인지 (k 배로 키운 그림도) — 크기 비율 3:1, 왼쪽 위 칸만 검다
func uprightAt(img image.Image, k int) bool {
	b := img.Bounds()
	return b.Dx() == 240*k && b.Dy() == 80*k && dark(img, 50*k, 10*k) && !dark(img, 10*k, 50*k) && !dark(img, 200*k, 60*k) && !dark(img, 200*k, 10*k)
}

func TestExifOrientationUprights(t *testing.T) {
	for o, st := range exifStored {
		for _, bo := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
			raw := exifJPEG(t, exifBlockImage(st.w, st.h, st.block), o, bo)
			if got := exifOrientation(raw); got != o {
				t.Fatalf("방향 %d (%v): %d 로 읽음", o, bo, got)
			}
			src, _, err := image.Decode(bytes.NewReader(raw))
			if err != nil {
				t.Fatal(err)
			}
			if uprightAt(src, 1) {
				t.Fatalf("방향 %d: 시험 그림이 처음부터 바로 서 있음", o)
			}
			if got := orientImage(src, o); !uprightAt(got, 1) {
				t.Errorf("방향 %d: 바로 서지 않음 (%v)", o, got.Bounds())
			}
		}
	}
	up := exifBlockImage(240, 80, image.Rect(0, 0, 60, 30))
	if orientImage(up, 1) != up || orientImage(up, 0) != up || orientImage(up, 9) != up {
		t.Error("방향 1·모르는 값은 그대로 돌려줘야 함")
	}
}

// 방향 표시가 없거나 깨진 파일: 1(그대로)로 보고, 어떤 내용이 와도 죽지 않는다 (휴대폰에서 받은 내용을 그대로 읽는다)
func TestExifOrientationBrokenInput(t *testing.T) {
	st := exifStored[6]
	img := exifBlockImage(st.w, st.h, st.block)
	if got := exifOrientation(exifJPEG(t, img, 0, nil)); got != 1 {
		t.Errorf("EXIF 없는 JPEG: %d", got)
	}
	var pb bytes.Buffer
	if err := png.Encode(&pb, img); err != nil {
		t.Fatal(err)
	}
	if got := exifOrientation(pb.Bytes()); got != 1 {
		t.Errorf("PNG: %d", got)
	}
	good := exifJPEG(t, img, 6, binary.LittleEndian)
	for n := 0; n <= len(good) && n < 200; n++ { // 어디서 잘려도
		exifOrientation(good[:n])
	}
	for _, v := range []int{9, 0xFFFF} { // 범위 밖 값
		if got := exifOrientation(exifJPEG(t, img, v, binary.BigEndian)); got != 1 {
			t.Errorf("방향 값 %d: %d", v, got)
		}
	}
	// 머리 부분(길이·위치·개수)을 아무렇게나 바꿔도 죽지 않고 1~8 만 돌려준다
	rnd := rand.New(rand.NewSource(1))
	for i := 0; i < 20000; i++ {
		b := append([]byte{}, good[:120]...)
		for k := 0; k < 1+rnd.Intn(3); k++ {
			b[2+rnd.Intn(40)] = byte(rnd.Intn(256))
		}
		if got := exifOrientation(b); got < 1 || got > 8 {
			t.Fatalf("깨진 머리: %d", got)
		}
	}
}

// ReadLotPhoto : 누운 사진(방향 6)은 두 글자 인식 모두에 바로 세운 그림을 준다 — 크기와 좌표가 같아야 줄을 짝짓는다.
// 방향 표시가 없는 사진은 보조 엔진에 올린 파일 그대로 준다.
func TestLotOCRUprightsExifPhoto(t *testing.T) {
	s := lotTestService(t)
	oldW, oldP := ocrRunner, paddleRunner
	t.Cleanup(func() { ocrRunner, paddleRunner = oldW, oldP })
	var winImgs []image.Image
	var padRaw []byte
	ocrRunner = func(ctx context.Context, paths []string) ([]platform.OCRResult, error) {
		winImgs = nil
		for _, p := range paths {
			f, err := os.Open(p)
			if err != nil {
				return nil, err
			}
			img, _, err := image.Decode(f)
			f.Close()
			if err != nil {
				return nil, err
			}
			winImgs = append(winImgs, img)
		}
		return make([]platform.OCRResult, len(paths)), nil
	}
	paddleRunner = func(ctx context.Context, paths []string) ([]platform.OCRResult, error) {
		b, err := os.ReadFile(paths[0])
		padRaw = b
		return []platform.OCRResult{{Engine: platform.PaddleEngine}}, err
	}
	read := func(raw []byte) {
		t.Helper()
		winImgs, padRaw = nil, nil
		if _, err := s.ReadLotPhoto(context.Background(), LotPhotoRequest{ImageBase64: base64.StdEncoding.EncodeToString(raw), Code: "900000010"}); err != nil {
			t.Fatal(err)
		}
		if len(winImgs) == 0 || len(padRaw) == 0 {
			t.Fatalf("두 엔진 모두 불려야 함: windows %d장, paddle %d바이트", len(winImgs), len(padRaw))
		}
	}

	for _, o := range []int{3, 6, 8} { // 휴대폰 카메라가 쓰는 값
		st := exifStored[o]
		read(exifJPEG(t, exifBlockImage(st.w, st.h, st.block), o, binary.BigEndian))
		for i, img := range winImgs {
			if !uprightAt(img, img.Bounds().Dx()/240) {
				t.Errorf("방향 %d: Windows 글자 인식에 준 그림 %d 이 바로 서지 않음 (%v)", o, i, img.Bounds())
			}
		}
		pimg, _, err := image.Decode(bytes.NewReader(padRaw))
		if err != nil {
			t.Fatal(err)
		}
		if !uprightAt(pimg, 1) {
			t.Errorf("방향 %d: 보조 엔진에 준 그림이 바로 서지 않음 (%v)", o, pimg.Bounds())
		}
	}

	// 방향 표시가 없는 사진·방향 1: 그대로
	for _, o := range []int{0, 1} {
		plain := exifJPEG(t, exifBlockImage(240, 80, image.Rect(0, 0, 60, 30)), o, binary.BigEndian)
		read(plain)
		if !bytes.Equal(padRaw, plain) {
			t.Errorf("방향 %d: 보조 엔진에는 올린 파일 그대로 줘야 함", o)
		}
		if !uprightAt(winImgs[0], winImgs[0].Bounds().Dx()/240) {
			t.Errorf("방향 %d: 그림이 바뀜 (%v)", o, winImgs[0].Bounds())
		}
	}
}
