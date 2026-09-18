// Package updater
// 处理自动更新的代理、匹配规则等
package updater

import (
	"strings"

	"github.com/wailsapp/wails/v3/pkg/updater"
	"github.com/wailsapp/wails/v3/pkg/updater/providers/github"
)

var archAliases = map[string][]string{
	"amd64": {"amd64", "x64", "x86_64"},
	"x64":   {"amd64", "x64", "x86_64"},
	"arm64": {"arm64", "aarch64"},
	"386":   {"386", "x86", "i386", "ia32"},
}

// tokenize 将文件名按 "_"、"-" 和 "." 切分为小写 token，
// 例如 "subs-free_windows_x64_setup.exe"
//   → ["subs", "free", "windows", "x64", "setup", "exe"]
func tokenize(name string) []string {
	s := strings.ToLower(name)
	s = strings.NewReplacer(".", "_", "-", "_").Replace(s)
	return strings.Split(s, "_")
}

func hasToken(tokens []string, want string) bool {
	for _, t := range tokens {
		if t == want {
			return true
		}
	}
	return false
}

// matchesPlatformArch 检查文件名是否同时包含平台标识和架构标识
func matchesPlatformArch(name, platform, arch string) bool {
	tokens := tokenize(name)
	if !hasToken(tokens, platform) {
		return false
	}
	aliases, ok := archAliases[arch]
	if !ok {
		aliases = []string{arch}
	}
	for _, a := range aliases {
		if hasToken(tokens, a) {
			return true
		}
	}
	return false
}

// AssetMatcher 是 github.Config 的 AssetMatcher 字段值。
// 帮助 Wails 准确从一堆混杂了 APK、DMG、DEB 的 Release 列表中找到合适的自更新包
func AssetMatcher(req updater.CheckRequest, assets []github.ReleaseAsset) int {
	platform := strings.ToLower(req.Platform) // windows / darwin / linux
	arch := strings.ToLower(req.Arch)         // amd64 / arm64 / 386

	switch platform {
	case "windows":
		// 1) 优先纯二进制 portable .exe（跳过 setup 安装包）
		for i, a := range assets {
			if matchesPlatformArch(a.Name, platform, arch) &&
				strings.HasSuffix(strings.ToLower(a.Name), ".exe") &&
				!hasToken(tokenize(a.Name), "setup") {
				return i
			}
		}
		// 2) 退化：没有 portable 版本时选 setup 安装包
		for i, a := range assets {
			if matchesPlatformArch(a.Name, platform, arch) &&
				strings.HasSuffix(strings.ToLower(a.Name), ".exe") {
				return i
			}
		}
		return -1

	case "darwin":
		// macOS：优先选 .tar.gz（Wails updater 能解压覆盖），跳过无法直接覆盖的 .dmg
		for i, a := range assets {
			if matchesPlatformArch(a.Name, platform, arch) &&
				strings.HasSuffix(strings.ToLower(a.Name), ".tar.gz") {
				return i
			}
		}
		// 退化：只要匹配平台和架构的都行
		for i, a := range assets {
			if matchesPlatformArch(a.Name, platform, arch) {
				return i
			}
		}
		return -1

	case "linux":
		// 优先裸二进制，跳过 .deb（防止 updater 把安装包当可执行文件覆盖）
		for i, a := range assets {
			if matchesPlatformArch(a.Name, platform, arch) &&
				!strings.HasSuffix(strings.ToLower(a.Name), ".deb") {
				return i
			}
		}
		return -1

	default: // darwin 等
		for i, a := range assets {
			if matchesPlatformArch(a.Name, platform, arch) {
				return i
			}
		}
		return -1
	}
}