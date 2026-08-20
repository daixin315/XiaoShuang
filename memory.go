package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ===== 小双的六层记忆系统 v2（分隔符文本 + 内存缓存）=====
//   长期层（textCache 模式，每行一条，行序=时间序）：
//     core.txt      核心区：名字/性别/喜好，不淘汰只能更新
//     system.txt    系统区：运行环境/设备，不淘汰只能更新，涉及系统话题才注入
//     important.txt 重要区：重要事情，满40条淘汰最旧
//   时间层（timeline 时间为键）：
//     timeline.txt  每行: 时间戳|内容（时间戳为 key，Split 即解析）
//       按 ts 分区: now-24h 前→week段, now-7d 前→far段
//       far 段有货 → 批量 AI 判断：重要→important，不重要→删除
//   时间流转纯算法；核心/系统/重要判断由 AI（总结分层 + far 批量归档）

// TimelineEntry 时间记忆
type TimelineEntry struct {
	TS   int64  // 时间戳（key）
	Text string // 内容
}

var (
	memDir    string
	memMu     sync.Mutex
	coreLines []string        // 核心区（行序=时间序，旧在前）
	sysLines  []string        // 系统区
	impLines  []string        // 重要区
	actions   []string        // 动作区（小双会表演的表情+动作名，启动时从资源扫描生成）
	timeline  []TimelineEntry // 时间流水（按 ts 升序）
	lastTick  time.Time
)

const (
	coreMax      = 30  // 核心区上限（AI 合并，程序兜底删最旧）
	systemMax    = 20  // 系统区上限
	importantMax = 40  // 重要区上限（满了才淘汰）
	memSep       = "︿" // 罕见 Unicode 分隔符（参考 z 库 SepUnit1，正常文本不会出现，无需转义）
)

// normalizeText 入库规范化：单行化 + 限长（分隔符为罕见字符无需转义）
func normalizeText(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.Join(strings.Fields(s), " ") // 压缩连续空白
	runes := []rune(s)
	if len(runes) > 100 {
		s = string(runes[:100])
	}
	return s
}

// loadMemory 读取 memory/ 目录（无则创建；旧 memory.json 存在则自动迁移）
func loadMemory(exeDir string) {
	memMu.Lock()
	defer memMu.Unlock()
	memDir = filepath.Join(exeDir, "memory")
	if migrateLegacy(exeDir) {
		// 迁移完成，旧文件已改名，直接读新目录
	}
	os.MkdirAll(memDir, 0o755)
	coreLines = readLines(filepath.Join(memDir, "core.txt"))
	sysLines = readLines(filepath.Join(memDir, "system.txt"))
	impLines = readLines(filepath.Join(memDir, "important.txt"))
	timeline = readTimeline(filepath.Join(memDir, "timeline.txt"))
}

// legacyEntry 旧 memory.json 迁移用
type legacyEntry struct {
	Text string `json:"text"`
}

// migrateLegacy 兼容旧 memory.json（六层 JSON 或单层数组），迁移后改名防重复迁移
func migrateLegacy(exeDir string) bool {
	old := filepath.Join(exeDir, "memory.json")
	data, err := os.ReadFile(old)
	if err != nil {
		return false
	}
	os.MkdirAll(filepath.Join(exeDir, "memory"), 0o755)
	// 尝试六层结构
	var six struct {
		Core      []legacyEntry `json:"core"`
		System    []legacyEntry `json:"system"`
		Important []legacyEntry `json:"important"`
		Today     []legacyEntry `json:"today"`
		Week      []legacyEntry `json:"week"`
		Far       []legacyEntry `json:"far"`
	}
	if json.Unmarshal(data, &six) == nil && (len(six.Core)+len(six.System)+len(six.Important)+len(six.Today)+len(six.Week)+len(six.Far)) > 0 {
		writeLines(joinTexts(six.Core), filepath.Join(exeDir, "memory", "core.txt"))
		writeLines(joinTexts(six.System), filepath.Join(exeDir, "memory", "system.txt"))
		writeLines(joinTexts(six.Important), filepath.Join(exeDir, "memory", "important.txt"))
		var tl []TimelineEntry
		now := time.Now().Unix()
		for _, e := range six.Today {
			tl = append(tl, TimelineEntry{TS: now, Text: e.Text})
		}
		for _, e := range six.Week {
			tl = append(tl, TimelineEntry{TS: now - 2*24*3600, Text: e.Text})
		}
		for _, e := range six.Far {
			tl = append(tl, TimelineEntry{TS: now - 8*24*3600, Text: e.Text})
		}
		writeTimelineAt(filepath.Join(exeDir, "memory", "timeline.txt"), tl)
		os.Rename(old, old+".bak")
		return true
	}
	// 旧单层数组
	var legacy []legacyEntry
	if json.Unmarshal(data, &legacy) == nil && legacy != nil {
		writeLines(joinTexts(legacy), filepath.Join(exeDir, "memory", "important.txt"))
		os.Rename(old, old+".bak")
		return true
	}
	return false
}

func joinTexts(entries []legacyEntry) []string {
	var out []string
	for _, e := range entries {
		if e.Text != "" {
			out = append(out, e.Text)
		}
	}
	return out
}

func writeLines(lines []string, path string) {
	var sb strings.Builder
	for _, l := range lines {
		sb.WriteString(l)
		sb.WriteString("\n")
	}
	os.WriteFile(path, []byte(sb.String()), 0o644)
}

func writeTimelineAt(path string, tl []TimelineEntry) {
	var sb strings.Builder
	for _, e := range tl {
		sb.WriteString(strconv.FormatInt(e.TS, 10))
		sb.WriteString(memSep)
		sb.WriteString(e.Text)
		sb.WriteString("\n")
	}
	os.WriteFile(path, []byte(sb.String()), 0o644)
}

// readLines 读文本文件，每行一条（跳过空行）
func readLines(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// readTimeline 读 timeline.txt（每行: ts|text，分隔符解析，快）
func readTimeline(path string) []TimelineEntry {
	lines := readLines(path)
	var tl []TimelineEntry
	for _, line := range lines {
		parts := strings.SplitN(line, memSep, 2)
		if len(parts) != 2 {
			continue
		}
		ts, err := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
		if err != nil {
			continue
		}
		tl = append(tl, TimelineEntry{TS: ts, Text: strings.TrimSpace(parts[1])})
	}
	return tl
}

// writeLayerFile 全量写回某层文件（调用方持锁）
func writeLayerFile(layer string, lines []string) {
	path := filepath.Join(memDir, layer+".txt")
	var sb strings.Builder
	for _, l := range lines {
		sb.WriteString(l)
		sb.WriteString("\n")
	}
	os.WriteFile(path, []byte(sb.String()), 0o644)
}

// writeTimelineFile 写回 timeline（调用方持锁）
func writeTimelineFile() {
	path := filepath.Join(memDir, "timeline.txt")
	var sb strings.Builder
	for _, e := range timeline {
		sb.WriteString(strconv.FormatInt(e.TS, 10))
		sb.WriteString(memSep)
		sb.WriteString(e.Text)
		sb.WriteString("\n")
	}
	os.WriteFile(path, []byte(sb.String()), 0o644)
}

// addMem 添加记忆
//
//	layer: core / system / important → 长期层
//	layer: today / week / far → timeline（ts=now 统一入 timeline，分区由 tick 决定）
func addMem(text, layer string) {
	text = normalizeText(text)
	if text == "" {
		return
	}
	memMu.Lock()
	defer memMu.Unlock()
	now := time.Now().Unix()

	switch layer {
	case "core":
		if upsertLine(&coreLines, text, coreMax) {
			writeLayerFile("core", coreLines)
		}
	case "system":
		if upsertLine(&sysLines, text, systemMax) {
			writeLayerFile("system", sysLines)
		}
	case "important":
		if upsertLine(&impLines, text, importantMax) {
			writeLayerFile("important", impLines)
		}
	default: // today/week/far 统一入 timeline（时间为键，分区由 tick 决定）
		// 去重：最近 50 条里相同则跳过
		n := len(timeline)
		for i := n - 1; i >= 0 && i >= n-50; i-- {
			if timeline[i].Text == text {
				return
			}
		}
		timeline = append(timeline, TimelineEntry{TS: now, Text: text})
		writeTimelineFile()
	}
}

// upsertLine 长期层写入：同主题（共享前6字）更新替换，否则追加；上限删最旧
func upsertLine(lines *[]string, text string, max int) bool {
	for i := range *lines {
		if sameTopic((*lines)[i], text) {
			(*lines)[i] = text // 更新（信息升级）
			return true
		}
	}
	*lines = append(*lines, text)
	for len(*lines) > max {
		*lines = (*lines)[1:]
	}
	return true
}

// sameTopic 共享前6字视为同主题（核心/系统区"更新而非新增"）
func sameTopic(a, b string) bool {
	ra, rb := []rune(a), []rune(b)
	if len(ra) < 6 || len(rb) < 6 {
		return a == b
	}
	return string(ra[:6]) == string(rb[:6])
}

// removeMemory 各区删除包含关键词的记忆，返回删除条数
func removeMemory(keyword string) int {
	memMu.Lock()
	defer memMu.Unlock()
	kw := strings.TrimSpace(keyword)
	if kw == "" {
		return 0
	}
	n := 0
	drop := func(lines []string) []string {
		keep := lines[:0]
		for _, l := range lines {
			if strings.Contains(l, kw) {
				n++
				continue
			}
			keep = append(keep, l)
		}
		return keep
	}
	coreLines = drop(coreLines)
	sysLines = drop(sysLines)
	impLines = drop(impLines)
	keepTl := timeline[:0]
	for _, e := range timeline {
		if strings.Contains(e.Text, kw) {
			n++
			continue
		}
		keepTl = append(keepTl, e)
	}
	timeline = keepTl
	if n > 0 {
		writeLayerFile("core", coreLines)
		writeLayerFile("system", sysLines)
		writeLayerFile("important", impLines)
		writeTimelineFile()
	}
	return n
}

// ===== 时间流转（纯算法）=====

// maybeTick 每小时最多跑一次流转
func maybeTick() {
	memMu.Lock()
	if time.Since(lastTick) < time.Hour {
		memMu.Unlock()
		return
	}
	lastTick = time.Now()
	memMu.Unlock()
	tickMemories()
}

// tickMemories timeline 按 ts 分区：now-24h 前=week段，now-7d 前=far段；far 有货则异步 AI 归档
func tickMemories() {
	memMu.Lock()
	now := time.Now()
	var weekFar []TimelineEntry // 过期条目（24h 前）
	keep := timeline[:0]
	for _, e := range timeline {
		if now.Sub(time.Unix(e.TS, 0)) > 24*time.Hour {
			weekFar = append(weekFar, e)
		} else {
			keep = append(keep, e)
		}
	}
	timeline = keep
	writeTimelineFile()
	memMu.Unlock()

	if len(weekFar) == 0 {
		return
	}
	// week 段保留 7 天内，7 天前的进 far 待 AI 归档
	var far []TimelineEntry
	var weekKeep []TimelineEntry
	for _, e := range weekFar {
		if now.Sub(time.Unix(e.TS, 0)) > 7*24*time.Hour {
			far = append(far, e)
		} else {
			weekKeep = append(weekKeep, e)
		}
	}
	_ = weekKeep // week 段无需落盘（timeline 已按 24h 保留，week 段只是概念分区）
	if len(far) > 0 {
		enqueue(func() { judgeFar(far) }) // 排队串行，不并发跑 AI
	}
}

// judgeFar 批量 AI 判断更远记忆（异步，静默失败）
func judgeFar(far []TimelineEntry) {
	var sb strings.Builder
	sb.WriteString("以下是过期记忆，请判断哪些值得长期保留（用户的重要个人信息、重大事件、重要约定）。\n")
	for i, e := range far {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, e.Text))
	}
	sb.WriteString("\n输出格式（只输出编号）：保留: 1,3\n删除: 2,4")
	reply, err := chatWithAIEx([]ChatMessage{
		{Role: "system", Content: "你是记忆归档器，只输出保留/删除编号。"},
		{Role: "user", Content: sb.String()},
	}, false, false)
	if err != nil {
		return // 静默失败，下轮 tick 再判
	}

	keepIdx := map[int]bool{}
	for _, seg := range strings.Split(reply, "\n") {
		seg = strings.TrimSpace(seg)
		if !strings.HasPrefix(seg, "保留") {
			continue
		}
		for _, part := range strings.Split(seg, ":")[1:] {
			for _, tok := range strings.Split(part, ",") {
				var idx int
				if _, err := fmt.Sscanf(strings.TrimSpace(tok), "%d", &idx); err == nil && idx >= 1 && idx <= len(far) {
					keepIdx[idx-1] = true
				}
			}
		}
	}

	memMu.Lock()
	for i, e := range far {
		if keepIdx[i] {
			impLines = append(impLines, e.Text)
		}
	}
	for len(impLines) > importantMax {
		impLines = impLines[1:]
	}
	writeLayerFile("important", impLines)
	memMu.Unlock()
	fmt.Printf("[memory] 更远区归档: %d 条保留进重要区, 其余删除\n", len(keepIdx))
}

// ===== 注入 =====

// memoryPrompt 生成注入文本：核心+重要常驻；includeSystem 时加系统区
func memoryPrompt(includeSystem bool) string {
	memMu.Lock()
	defer memMu.Unlock()
	var sb strings.Builder
	write := func(title string, lines []string) {
		if len(lines) == 0 {
			return
		}
		sb.WriteString(title)
		for _, l := range lines {
			sb.WriteString("- " + l + "\n")
		}
	}
	write("核心记忆（关于用户和你的事实，最重要）：\n", coreLines)
	if includeSystem {
		write("系统记忆（用户当前运行环境，涉及系统/设备话题时参考）：\n", sysLines)
	}
	write("重要记忆（用户的重要事情）：\n", impLines)
	if sb.Len() == 0 {
		return ""
	}
	return "以下是你的长期记忆（聊天时自然运用；除非用户问起，否则不要主动展示这份清单）：\n" + strings.TrimSuffix(sb.String(), "\n")
}

// systemKeywords 系统话题关键词（命中则注入系统区）
var systemKeywords = []string{"系统", "电脑", "设备", "装机", "Ubuntu", "Windows", "Linux", "macOS",
	"显卡", "CPU", "内存", "硬盘", "网络", "配置", "机器", "主机", "系统盘"}

// buildActions 从资源扫描结果生成动作区（表情+动作名），写盘 memory/actions.txt
// 在 scanResources() 之后调用
func buildActions() {
	memMu.Lock()
	defer memMu.Unlock()
	actions = nil
	for _, n := range exprNames {
		actions = append(actions, "表情:"+n)
	}
	for _, n := range actionNames {
		actions = append(actions, "动作:"+n)
	}
	if memDir != "" {
		writeLayerFile("actions", actions)
	}
	fmt.Printf("[memory] 动作区: %d 个（表情%d+动作%d）\n", len(actions), len(exprNames), len(actionNames))
}

// actionAbilityPrompt 生成"你会表演什么"的能力描述（注入 system，让 AI 回复时选动作）
func actionAbilityPrompt() string {
	memMu.Lock()
	defer memMu.Unlock()
	if len(actions) == 0 {
		return ""
	}
	return "你会表演这些表情和动作：" + strings.Join(actions, "、") +
		"。回复时如果觉得合适，在回复末尾单独一行输出 [表演]名字（只能选一个，必须是上面列出的完整名字）；不需要表演就不输出。"
}

// extractAction 从 AI 回复中解析 [表演]名字，返回(清理后的回复, 动作名或空)
// 清理后为空（AI 只输出了表演行）→ 回退原文，避免空回复
func extractAction(reply string) (string, string) {
	lines := strings.Split(reply, "\n")
	var keep []string
	action := ""
	for _, l := range lines {
		// 行内也可能出现 [表演]（AI 常拼在正文末尾），从标记处截断
		if idx := strings.Index(l, "[表演]"); idx >= 0 {
			before := strings.TrimSpace(l[:idx])
			name := strings.TrimSpace(strings.TrimPrefix(l[idx:], "[表演]"))
			// AI 可能输出 "动作:河边戏水" / "表情:开心"（带前缀），剥离
			name = strings.TrimPrefix(name, "动作:")
			name = strings.TrimPrefix(name, "表情:")
			name = strings.TrimSpace(name)
			if _, ok := actionFiles[name]; ok {
				action = name
			} else if _, ok := exprFiles[name]; ok {
				action = name
			}
			// 标记前有正文则保留（名字无效也一样保留正文，只是不播放）
			if before != "" {
				keep = append(keep, before)
			}
			continue
		}
		keep = append(keep, l)
	}
	result := strings.Join(keep, "\n")
	if strings.TrimSpace(result) == "" {
		return reply, action // 回退原文（表演照播）
	}
	return result, action
}

// playExtractedAction 播放 AI 选的表情/动作（动作单次，表情循环）
func playExtractedAction(name string, player *VideoPlayer) {
	if player == nil {
		return
	}
	if _, ok := actionFiles[name]; ok {
		fmt.Printf("[action] AI 选择动作: %s\n", name)
		playAction(name, player)
		return
	}
	if _, ok := exprFiles[name]; ok {
		fmt.Printf("[action] AI 选择表情: %s\n", name)
		playExpr(name, player)
	}
}

// listMemory 分层查看（/记忆 命令）
func listMemory() string {
	memMu.Lock()
	defer memMu.Unlock()
	section := func(title string, lines []string) string {
		if len(lines) == 0 {
			return ""
		}
		var sb strings.Builder
		sb.WriteString(title + "：\n")
		for i, l := range lines {
			sb.WriteString(fmt.Sprintf("  %d. %s\n", i+1, l))
		}
		return sb.String()
	}
	now := time.Now()
	var today, week, far []string
	for _, e := range timeline {
		age := now.Sub(time.Unix(e.TS, 0))
		switch {
		case age <= 24*time.Hour:
			today = append(today, e.Text)
		case age <= 7*24*time.Hour:
			week = append(week, e.Text)
		default:
			far = append(far, e.Text)
		}
	}
	var sb strings.Builder
	sb.WriteString("📝 记忆清单：\n")
	sb.WriteString(section("【核心】", coreLines))
	sb.WriteString(section("【系统】", sysLines))
	sb.WriteString(section("【重要】", impLines))
	sb.WriteString(section("【动作】", actions))
	sb.WriteString(section("【今天】", today))
	sb.WriteString(section("【本周】", week))
	sb.WriteString(section("【更远·待归档】", far))
	if strings.TrimSpace(sb.String()) == "📝 记忆清单：" {
		return "📝 我还没有记住什么～"
	}
	return strings.TrimSpace(sb.String())
}

// ===== AI 总结分层 =====

// summarizeForMemory 对话后自动提炼记忆（AI 判断分层：核心/系统/重要/一般→timeline）
func summarizeForMemory(history []ChatMessage) {
	settingsMu.RLock()
	key := settings.APIKey
	settingsMu.RUnlock()
	if key == "" {
		return
	}

	memMu.Lock()
	var ctx strings.Builder
	ctx.WriteString("核心记忆：\n")
	for _, l := range coreLines {
		ctx.WriteString("- " + l + "\n")
	}
	ctx.WriteString("系统记忆：\n")
	for _, l := range sysLines {
		ctx.WriteString("- " + l + "\n")
	}
	ctx.WriteString("重要记忆：\n")
	for _, l := range impLines {
		ctx.WriteString("- " + l + "\n")
	}
	memMu.Unlock()

	h := history
	if len(h) > 8 {
		h = h[len(h)-8:]
	}
	var sb strings.Builder
	sb.WriteString("已有记忆：\n" + ctx.String() + "\n刚才的对话：\n")
	for _, m := range h {
		sb.WriteString(m.Role + ": " + m.Content + "\n")
	}
	sb.WriteString(`
请提取这段对话中值得长期记住的新信息（用户个人信息/偏好/习惯/约定/重要事件）。
每条按重要性分层输出，格式：
【核心】用户的根本信息（名字/性别/喜好），已存在则输出更新后的完整条目
【系统】用户运行环境/设备信息
【重要】重要事件或约定
一般: 当天的小事（今天做了什么）
要求：每层最多1条，每条不超过40字；已有记忆里完全相同的不要重复；完全没有新信息就只输出"无"。`)

	msgs := []ChatMessage{
		{Role: "system", Content: "你是记忆提取器，按分层格式输出记忆条目或\"无\"。"},
		{Role: "user", Content: sb.String()},
	}
	reply, err := chatWithAIEx(msgs, false, false)
	if err != nil {
		return // 静默失败
	}
	added := 0
	for _, line := range strings.Split(reply, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, "无") {
			continue
		}
		layer := "today"
		switch {
		case strings.HasPrefix(line, "【核心】"):
			layer = "core"
			line = strings.TrimPrefix(line, "【核心】")
		case strings.HasPrefix(line, "【系统】"):
			layer = "system"
			line = strings.TrimPrefix(line, "【系统】")
		case strings.HasPrefix(line, "【重要】"):
			layer = "important"
			line = strings.TrimPrefix(line, "【重要】")
		case strings.HasPrefix(line, "一般:"):
			line = strings.TrimPrefix(line, "一般:")
		}
		line = strings.TrimSpace(line)
		line = strings.TrimSpace(strings.TrimPrefix(line, "-"))
		if line == "" || len([]rune(line)) > 60 {
			continue
		}
		addMem(line, layer)
		added++
	}
	if added > 0 {
		fmt.Printf("[memory] 自动提炼 %d 条\n", added)
	}
}
