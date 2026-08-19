#!/usr/bin/env python3
"""本地语音识别（faster-whisper）
用法: python3 whisper_stt.py <音频文件.wav> [模型名]
输出: 识别文字（stdout）
"""
import sys
import time

def main():
    if len(sys.argv) < 2:
        print("用法: whisper_stt.py <audio.wav> [model]", file=sys.stderr)
        sys.exit(1)
    audio = sys.argv[1]
    model = sys.argv[2] if len(sys.argv) > 2 else "medium"

    t0 = time.time()
    from faster_whisper import WhisperModel
    model_obj = WhisperModel(model, device="auto", compute_type="auto")
    segments, info = model_obj.transcribe(audio, language="zh", beam_size=1)
    text = "".join(s.text for s in segments).strip()
    print(text)
    print(f"[whisper {model} {time.time()-t0:.1f}s]", file=sys.stderr)

if __name__ == "__main__":
    main()
