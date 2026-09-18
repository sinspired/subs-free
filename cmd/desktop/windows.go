// windows.go
//
// 窗口尺寸解析及弹出窗口创建辅助。
// 将原本散落在 app.go（OpenBrandURL、OpenInternalPage）和
// router.go（popupSize）中三份相同 windowSize switch 合并到此处，
// 消除不一致（原 OpenBrandURL large=1100、OpenInternalPage large=1200、
// popupSize 已统一为 1200）。
package main

import (
	"github.com/wailsapp/wails/v3/pkg/application"
)

// windowDimensions 将尺寸名称映射为窗口宽高（像素）。
// 这是项目中唯一的尺寸定义点，所有弹窗均调用此函数。
func windowDimensions(size string) (width, height int) {
	switch size {
	case "extraLarge":
		return 1920, 1440
	case "large":
		return 1600, 1200
	case "medium":
		return 1100, 700
	case "window-file":
		return 940, 640
	case "small":
		return 720, 720
	case "tiny":
		return 600, 600
	case "wide":
		return 1200, 700
	default:
		return 1200, 800
	}
}

// macWindowOpts 返回统一的 macOS 窗口装饰选项。
// titleBarHeight 对登录/关于等小窗用 30，大窗用 50。
func macWindowOpts(titleBarHeight int) application.MacWindow {
	return application.MacWindow{
		InvisibleTitleBarHeight: titleBarHeight,
		Backdrop:                application.MacBackdropTranslucent,
		TitleBar:                application.MacTitleBarHiddenInset,
	}
}

// newPopupOptions 返回用于弹出窗口的通用 WebviewWindowOptions。
// url 为初始加载地址，size 为尺寸名称（同 windowDimensions）。
func newPopupOptions(title, url, size string) application.WebviewWindowOptions {
	w, h := windowDimensions(size)
	return application.WebviewWindowOptions{
		Title:     title,
		Width:     w,
		Height:    h,
		MinWidth:  600,
		MinHeight: 400,
		URL:       url,
		Mac:       macWindowOpts(50),
		BackgroundType: application.BackgroundTypeTranslucent,
	}
}
