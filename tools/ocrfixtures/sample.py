# 점검용 가상 사입현황 표 (실제 약국 자료 아님): python3 -I sample.py out.png
import sys
from PIL import Image, ImageDraw, ImageFont
F = ImageFont.truetype("/System/Library/Fonts/AppleSDGothicNeo.ttc", 22, index=0)
FB = ImageFont.truetype("/System/Library/Fonts/AppleSDGothicNeo.ttc", 22, index=6)
X = [10, 180, 270, 460, 560, 680, 840, 1010]
HEAD = ["사입일자", "구분", "거래처", "수량", "단가", "유효기간", "제조번호"]
ROWS = [
    ["2026-08-12", "사입", "가람도매", "45.00", "1,000", "20280131", "LOT-1001"],
    ["2026-06-03", "사입", "나래도매", "90.00", "1,000", "20271231", "LOT-1002"],
    ["2026-04-27", "사입", "다솜도매", "150.00", "1,000", "20280430", "LOT-1003"],
]
img = Image.new("RGB", (1020, 268), "white")
d = ImageDraw.Draw(img)
top, rh = 30, 52
d.rectangle([X[0], top, X[-1], top + rh], fill=(234, 238, 246))
for i, row in enumerate([HEAD] + ROWS):
    y = top + i * rh
    for j, cell in enumerate(row):
        d.text((X[j] + 10, y + rh / 2), cell, font=FB if i == 0 else F, fill=(20, 20, 20), anchor="lm")
for i in range(len(ROWS) + 2):
    d.line([X[0], top + i * rh, X[-1], top + i * rh], fill=(210, 210, 210))
for x in X:
    d.line([x, top, x, top + rh * (len(ROWS) + 1)], fill=(210, 210, 210))
img.save(sys.argv[1], optimize=True)
