// gui_routes.go
package main

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func registerGuiRoutes(router *gin.Engine) {
	if router == nil {
		return
	}

	// 跨域处理（如果需要的话，WebUI 和 Wails 处于不同域）
	router.Use(func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Next()
	})

	// 当在 WebUI 内点击“退出登录/返回首页”时请求此接口
	router.GET("/gui/back-to-home", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
		if globalGuiApp != nil {
			globalGuiApp.BackToHome()
		}
	})
}
