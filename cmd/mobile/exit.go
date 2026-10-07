// cmd\mobile\exit.go
package main

import (
	"log/slog"
	"os"
	"sync"
	"time"
)

var shutdownCoreOnce sync.Once

// ShutdownCore 供前端「退出应用」调用：先关闭后端内核，再由 Java 层结束进程。
//
// 顺序：
//  1. 停止前台服务（否则通知会残留）
//  2. 调用 core.Shutdown()：取消 ctx（Sub-Store 等子服务退出）、停止定时任务与配置监听、
//     优雅关闭 HTTP 服务器（内部最长等待 5 秒，另有 500ms 清理等待）
//
// 整体设置 8 秒上限，防止个别任务卡死导致"退出"按钮一直无响应。
// 返回 true 表示内核已完整关闭，false 表示超时（调用方仍应继续退出流程）。
// 多次调用安全：只会真正关闭一次。
func (g *GuiApp) ShutdownCore() bool {
	ok := true

	shutdownCoreOnce.Do(func() {
		slog.Info("用户请求退出：开始关闭内核")

		func() {
			defer func() { _ = recover() }() // 原生层异常不能阻断退出流程
			g.StopForegroundService()
		}()

		if g.backend == nil {
			return
		}

		done := make(chan error, 1)
		go func() { done <- g.backend.Shutdown() }()

		select {
		case err := <-done:
			if err != nil {
				slog.Warn("内核关闭时出现错误", "error", err)
			}
		case <-time.After(8 * time.Second):
			slog.Warn("内核关闭超时，继续退出流程")
			ok = false
		}
	})

	return ok
}

// ExitApp 退出应用：先优雅关闭内核（ShutdownCore），再结束进程。
// 不依赖任何 Java 层改动。结束进程前短暂等待，让异步投递的 stopService 先执行，
// 避免 START_STICKY 的前台服务在进程退出后被系统重新拉起、残留通知。
func (g *GuiApp) ExitApp() {
	g.ShutdownCore()
	time.Sleep(300 * time.Millisecond)
	slog.Info("应用退出")
	os.Exit(0)
}