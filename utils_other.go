//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// hideWindow 隐藏子进程控制台窗口（Windows 专用；其他平台空实现）
func hideWindow(cmd *exec.Cmd) {}

// setupLogFile Windows 专用日志重定向；其他平台无操作
func setupLogFile() {}

// captureScreen Linux 截图（gridhand，继承主机 X 环境；Wayland 下走 Portal）
func captureScreen(path string) error {
	cmd := exec.Command("gridhand", "screenshot", "--output", path)
	cmd.Env = append(os.Environ(), "DISPLAY=:0", "XAUTHORITY="+getXauthority())
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("截图失败: %v %s", err, strings.TrimSpace(string(out)))
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("截图文件未生成")
	}
	return nil
}
