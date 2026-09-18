//go:build !lite

package main

import "embed"

//go:embed all:frontend/dist
var assets embed.FS
var assetsDir = "frontend/dist"
