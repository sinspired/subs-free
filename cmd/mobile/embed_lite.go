//go:build lite

package main

import "embed"

//go:embed all:frontend/dist-lite
var assets embed.FS
var assetsDir = "frontend/dist-lite"
