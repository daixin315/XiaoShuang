# 自定义形象指南

本项目自带"小双"形象（主图 + 18 表情 + 10 动作视频），你也可以**生成自己的形象**。

## 1. 生成主图

用任意 AI 图像模型（推荐通义万相 / Qwen-Image / Midjourney）生成一张 16:9 的人物立绘：

```
一个年轻的中国古典风少女，站在草地上，身后有雪山、凤凰、秋千…
阳光写实风格，1280x720
```

保存为 `assets/main.png`。

## 2. 生成表情动画

使用 [火山方舟 Seedance 2.0/2.5](https://console.volcengine.com/ark) 图生视频：

```bash
# 提示词模板（关键：镜头固定 + 表情自然 + 结尾还原）
镜头全程固定不动，女孩从平静的表情开始，{表情描述}，
表情真实自然像真人，眼睛无发光无变形，然后自然恢复初始画面
```

表情命名规范（存到 `assets/emotions/`）：

```
happy.mp4  smile.mp4  daze.mp4  sad.mp4  think.mp4
trance.mp4 surprised.mp4  proud.mp4  shy.mp4  sleepy.mp4
angry.mp4  excited.mp4  crying.mp4  wink.mp4  wronged.mp4
cute.mp4  scared.mp4  speechless.mp4
```

## 3. 生成动作视频

复杂动作用**分段拼接**（15秒/段，2.0 上限）：
- 每段只做 1-2 个动作
- 段2 首帧 = 段1 尾帧（首尾帧衔接）
- 最后一段专职"走回+还原"（首帧=前段尾帧 + 尾帧=主图）

动作视频存 `assets/actions/`。

## 4. 修改代码中的映射

`mood_daemon.go` 里 `emotionFiles` 映射表情名 → 文件：

```go
var emotionFiles = map[string]string{
	"idle":  "main.png",
	"happy": "happy.mp4",
	// ...
}
```

## 5. 生成工具脚本

仓库 `scripts/` 下可参考生成流程；提示词模板见项目文档。

> 注意：AI 生成的视频素材版权归生成者，开源分发时请确认所用模型的条款。
