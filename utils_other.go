//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// hideWindow 隐藏子进程控制台窗口（Windows 专用；其他平台空实现）
func hideWindow(cmd *exec.Cmd) {}

// setupLogFile Windows 专用日志重定向；其他平台无操作
func setupLogFile() {}

// captureScreen Linux 截图（XDG Desktop Portal 静默截屏：无快门声无闪屏动画）
// gridhand 改用：它调用 GNOME Shell Screenshot 服务会播声音+闪屏
func captureScreen(path string) error {
	script := filepath.Join(exeDir, "scripts", "portal_shot.py")
	cmd := exec.Command("/usr/bin/python3", script, path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("截图失败: %v %s", err, strings.TrimSpace(string(out)))
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("截图文件未生成")
	}
	return nil
}
