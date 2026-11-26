# 글자 인식 비교 시험용 가상 사입현황 표 그림을 만든다 (실제 약국 자료 아님 — 도매 이름·제조번호 모두 지어낸 것).
#   python3 make.py <이 폴더>      (Pillow, numpy 필요. macOS 의 AppleSDGothicNeo 글꼴로 그린다)
# 만드는 것: table.png(보통 크기) · small.png(화면 캡처 크기의 작은 글씨) · color.png(색 바탕·색 글씨) ·
#            photo.jpg(모니터를 휴대폰으로 찍은 듯: 기울기·모아레·흐림)
# 정답은 truth.json — 그림을 다시 만들면 같이 바뀐다.
import json
import sys

import numpy as np
from PIL import Image, ImageDraw, ImageFilter, ImageFont

out = sys.argv[1]
FONT = '/System/Library/Fonts/AppleSDGothicNeo.ttc'
COLS = [('사입일자', 6.6), ('구분', 3.4), ('거래처', 7.4), ('수량', 4.4), ('단가', 4.6), ('유효기간', 6.2), ('제조번호', 6.6)]
ROWS = [
    ['2026-11-14', '사입', '가람도매', '30.00', '1,000', '20271031', '2400117'],
    ['2026-10-02', '사입', '나래도매', '100.00', '1,000', '20270930', 'KA5032'],
    ['2026-09-18', '사입', '다솜도매', '240.00', '980', '20280131', 'B24X017'],
    ['2026-08-05', '사입', '라온약품', '60.00', '980', '20271231', '31007-1'],
    ['2026-07-21', '사입', '마루팜', '500.00', '1,020', '20270531', '31007-2'],
    ['2026-06-09', '사입', '가람도매', '90.00', '1,020', '20270228', 'LOT-1001'],
    ['2026-05-27', '사입', '바른메디칼', '12.00', '1,050', '20261130', '6502481'],
    ['2026-04-13', '사입', '나래도매', '1,200.00', '1,050', '20280630', 'HW88023'],
]


def table(size):
    font = ImageFont.truetype(FONT, size, index=0)
    bold = ImageFont.truetype(FONT, size, index=6)
    rh = int(size * 2)
    pad = int(size * 0.4)
    widths = [int(w * size) for _, w in COLS]
    W = sum(widths) + 2 * pad
    H = rh * (len(ROWS) + 1) + 2 * pad
    img = Image.new('RGB', (W, H), 'white')
    d = ImageDraw.Draw(img)
    y = pad
    x = pad
    for (name, _), w in zip(COLS, widths):
        d.rectangle([x, y, x + w, y + rh], outline=(190, 190, 190), fill=(236, 240, 246))
        d.text((x + pad, y + rh * 0.24), name, font=bold, fill='black')
        x += w
    for r in ROWS:
        y += rh
        x = pad
        for w, v in zip(widths, r):
            d.rectangle([x, y, x + w, y + rh], outline=(210, 210, 210))
            d.text((x + pad, y + rh * 0.24), v, font=font, fill='black')
            x += w
    return img


def color_table(size):
    # 색이 있는 화면: 남색 머리(흰 글씨), 고른 줄(파란 바탕·흰 글씨), 빨간 글씨 줄, 연노랑 줄 — 색 채널 처리를 확인한다
    font = ImageFont.truetype(FONT, size, index=0)
    bold = ImageFont.truetype(FONT, size, index=6)
    rh = int(size * 2)
    pad = int(size * 0.4)
    widths = [int(w * size) for _, w in COLS]
    W = sum(widths) + 2 * pad
    H = rh * (len(ROWS) + 1) + 2 * pad
    img = Image.new('RGB', (W, H), (250, 250, 252))
    d = ImageDraw.Draw(img)
    styles = {1: ((0, 90, 200), (255, 255, 255)), 3: ((255, 255, 255), (200, 30, 30)), 4: ((255, 248, 200), (20, 20, 20)), 6: ((225, 240, 225), (0, 80, 160))}
    y = pad
    x = pad
    for (name, _), w in zip(COLS, widths):
        d.rectangle([x, y, x + w, y + rh], outline=(20, 40, 90), fill=(30, 60, 130))
        d.text((x + pad, y + rh * 0.24), name, font=bold, fill='white')
        x += w
    for i, r in enumerate(ROWS):
        y += rh
        x = pad
        bg, fg = styles.get(i, ((255, 255, 255), (0, 0, 0)))
        for w, v in zip(widths, r):
            d.rectangle([x, y, x + w, y + rh], outline=(210, 210, 210), fill=bg)
            d.text((x + pad, y + rh * 0.24), v, font=font, fill=fg)
            x += w
    return img


def photo(img):
    # 모니터 화면: 어두운 바탕 위에 표, 약간 기울고 원근이 있다
    rng = np.random.default_rng(7)
    W, H = 3200, 2400
    bg = Image.new('RGB', (W, H), (38, 40, 46))
    t = img.resize((2700, int(img.height * 2700 / img.width)), Image.LANCZOS)
    bg.paste(t, (250, 500))
    bg = bg.rotate(1.6, resample=Image.BICUBIC, fillcolor=(38, 40, 46))
    k = 0.035  # 원근
    bg = bg.transform((W, H), Image.QUAD, (0, 0, int(-W * k), H, int(W * (1 + k)), H, W, 0), resample=Image.BICUBIC, fillcolor=(20, 20, 22))
    a = np.asarray(bg).astype(np.float32)
    yy, xx = np.mgrid[0:H, 0:W].astype(np.float32)
    # 모아레: 화소 격자와 카메라 격자가 어긋나 생기는 물결 무늬
    moire = 1 + 0.10 * np.sin(xx * 0.93 + yy * 0.11) * np.sin(yy * 0.88 - xx * 0.07) + 0.06 * np.sin((xx + yy) * 0.045)
    # 화면 밝기 얼룩
    shade = 0.82 + 0.18 * np.cos((xx - W * 0.4) / W * 2.2) * np.cos((yy - H * 0.5) / H * 1.8)
    a = a * moire[..., None] * shade[..., None]
    a += rng.normal(0, 5, a.shape)
    p = Image.fromarray(np.clip(a, 0, 255).astype(np.uint8)).filter(ImageFilter.GaussianBlur(1.3))
    return p


table(26).save(f'{out}/table.png', optimize=True)
table(13).save(f'{out}/small.png', optimize=True)
color_table(20).save(f'{out}/color.png', optimize=True)
photo(table(30)).save(f'{out}/photo.jpg', quality=82)
keys = ['date', 'kind', 'supplier', 'qty', 'price', 'expiry', 'lot']
truth = {'note': '가상 자료 (실제 약국 자료 아님)', 'rows': [dict(zip(keys, r)) for r in ROWS]}
json.dump(truth, open(f'{out}/truth.json', 'w'), ensure_ascii=False, indent=1)
