#!/usr/bin/env python3
"""小双 Help 桌面观察：PaddleOCR 识别图片文字
用法: ocr_helper.py <图片路径> [lang]
输出: 识别文字（用 | 分隔），stdout
"""
import sys

from paddleocr import PaddleOCR


def main():
    if len(sys.argv) < 2:
        print("用法: ocr_helper.py <image> [lang]", file=sys.stderr)
        sys.exit(1)
    img = sys.argv[1]
    lang = sys.argv[2] if len(sys.argv) > 2 else "ch"
    ocr = PaddleOCR(lang=lang, use_gpu=True, show_log=False)
    r = ocr.ocr(img, cls=False)
    texts = [line[1][0] for line in (r[0] or [])]
    print(" | ".join(texts))


if __name__ == "__main__":
    main()
