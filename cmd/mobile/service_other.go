//go:build !android

package main

// StartForegroundService 在非安卓平台是空操作。前台服务是安卓专属概念，
// 但前端（App.tsx）是桌面/iOS/安卓共用的同一份代码，会在检测开始时无条件
// 调用这个方法，所以这里必须留一个空实现，否则桌面/iOS 构建时绑定里根本
// 没有这个方法，前端调用会直接报错。
func (g *GuiApp) StartForegroundService(title, body string) {}

// StopForegroundService 在非安卓平台是空操作，理由同上。
func (g *GuiApp) StopForegroundService() {}
