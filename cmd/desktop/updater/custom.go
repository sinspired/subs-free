package updater

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

type UpdateStatus struct {
	Available bool   `json:"available"`
	Version   string `json:"version"`
}

var (
	statusMu sync.RWMutex
	status   UpdateStatus
)

func GetUpdateStatus() UpdateStatus {
	statusMu.RLock()
	defer statusMu.RUnlock()
	return status
}

func SetUpdateStatus(v UpdateStatus) {
	statusMu.Lock()
	status = v
	statusMu.Unlock()
}

// EventStatusChanged 在 CheckUpdateStatus 每次检查结束后触发（无论有没有
// 新版本、也无论检查是否失败），前端订阅这个事件即可与后台检查结果保持
// 同步，而不必依赖“组件挂载时读一次缓存值”这种存在竞态的方式。
const EventStatusChanged = "gui:update:status-changed"

// CheckTimeout 更新检查请求经过代理服务器中转（github.com / api.github.com），
// 网络状况不如直连稳定；fallbackTransport 内部最多会先经历 sysProxyAttemptTimeout
// （8 秒）的系统代理探测（见 sysproxy.go），失败后立即切到不经系统代理的 GitHub 代理线路兜底，
// 外层超时仍需留出足够余量给这一次兜底尝试，
// 因此放宽到 20 秒；同时又不能设为 0（永不超时），避免代理彻底失联时
// goroutine 长期挂起。
const CheckTimeout = 20 * time.Second

// CheckUpdateStatus 只做“检查是否有新版本”，不下载、不安装。
func CheckUpdateStatus(wailsApp *application.App) {
	ctx, cancel := context.WithTimeout(context.Background(), CheckTimeout)
	defer cancel()

	rel, err := wailsApp.Updater.Check(ctx)
	if err != nil {
		slog.Debug("Updater check 失败", "error", err)
		// 检查失败时不覆盖上一次已知的状态（例如上次检查到的“有新版本”
		// 不应因为这次超时/网络抖动而被静默清空），只做事件通知，
		// 让前端可以在需要时提示“检查更新失败，可稍后重试”。
		emitStatusChanged(wailsApp, GetUpdateStatus())
		return
	}

	var next UpdateStatus
	if rel != nil {
		next = UpdateStatus{Available: true, Version: rel.Version}
	} else {
		next = UpdateStatus{Available: false, Version: ""}
	}
	SetUpdateStatus(next)
	emitStatusChanged(wailsApp, next)
}

func emitStatusChanged(wailsApp *application.App, st UpdateStatus) {
	if wailsApp == nil || wailsApp.Event == nil {
		return
	}
	wailsApp.Event.Emit(EventStatusChanged, st)
}