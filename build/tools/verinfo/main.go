// build/tools/verinfo/main.go
//
// 构建期版本信息小工具：用 `go run` 调用，不依赖 Python/Bash，
// 只要能编译这个项目就一定有 go 命令，天然跨平台、零额外依赖。
//
// 子命令：
//
//	verinfo core-version                                   打印 go.mod 里内核依赖的版本号
//	verinfo core-commit                                    打印内核依赖版本对应的 git commit 短哈希
//	verinfo numeric <version>                              semver 转 4 段数字版本（用于 Windows 资源版本号）
//	verinfo gen-syso <infoIn> <manifestIn> <infoOut> <manifestOut> <version>
//	                                                        生成 syso 所需的 info.json / manifest 临时文件
//
// 环境变量：
//
//	CORE_VERSION / CORE_COMMIT   手动覆盖，跳过自动探测
//	TASK_DEBUG=1                 打印调试信息到 stderr
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

const coreModule = "github.com/sinspired/subs-check-pro/v3"

var debugEnabled = os.Getenv("TASK_DEBUG") == "1"

func debugf(format string, args ...any) {
	if debugEnabled {
		fmt.Fprintf(os.Stderr, "[verinfo] "+format+"\n", args...)
	}
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "verinfo: "+format+"\n", args...)
	os.Exit(1)
}

func main() {
	if len(os.Args) < 2 {
		die("missing subcommand (core-version | core-commit | numeric | gen-syso)")
	}

	switch os.Args[1] {
	case "core-version":
		fmt.Println(coreVersion())
	case "core-commit":
		fmt.Println(coreCommit())
	case "numeric":
		if len(os.Args) < 3 {
			die("numeric requires a version argument")
		}
		fmt.Println(toNumericVersion(os.Args[2]))
	case "gen-syso":
		if len(os.Args) < 7 {
			die("gen-syso requires: <infoIn> <manifestIn> <infoOut> <manifestOut> <version>")
		}
		genSyso(os.Args[2], os.Args[3], os.Args[4], os.Args[5], os.Args[6])
	default:
		die("unknown subcommand %q", os.Args[1])
	}
}

// ── go.mod 查找 / 解析 ──────────────────────────────────────────────────────

// findGoMod 从当前目录向上查找 go.mod（兼容 task 在子目录下执行的情况）。
func findGoMod() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for range 6 {
		p := filepath.Join(dir, "go.mod")
		if _, err := os.Stat(p); err == nil {
			return p
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
	return ""
}

var requireLineRe = regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(coreModule) + `\s+(\S+)`)

// parseCoreVersion 从 go.mod 文本中解析内核依赖的版本号。
func parseCoreVersion(goModPath string) (string, error) {
	data, err := os.ReadFile(goModPath)
	if err != nil {
		return "", err
	}
	m := requireLineRe.FindStringSubmatch(string(data))
	if m == nil {
		return "", fmt.Errorf("module %s not found in %s", coreModule, goModPath)
	}
	return m[1], nil
}

// ── core-version ────────────────────────────────────────────────────────────

func coreVersion() string {
	if v := strings.TrimSpace(os.Getenv("CORE_VERSION")); v != "" {
		debugf("using CORE_VERSION from env: %s", v)
		return v
	}

	goMod := findGoMod()
	if goMod == "" {
		debugf("go.mod not found, fallback to 'dev'")
		return "dev"
	}

	v, err := parseCoreVersion(goMod)
	if err != nil {
		debugf("%v, fallback to 'dev'", err)
		return "dev"
	}
	debugf("parsed version=%s from %s", v, goMod)
	return v
}

// ── core-commit ─────────────────────────────────────────────────────────────

// moduleDownloadInfo 对应 `go mod download -json` 输出中我们关心的字段。
type moduleDownloadInfo struct {
	Origin struct {
		Hash string `json:"Hash"`
	} `json:"Origin"`
}

func coreCommit() string {
	if v := strings.TrimSpace(os.Getenv("CORE_COMMIT")); v != "" {
		debugf("using CORE_COMMIT from env: %s", v)
		return v
	}

	goMod := findGoMod()
	if goMod == "" {
		debugf("go.mod not found, fallback to 'unknown'")
		return "unknown"
	}

	version, err := parseCoreVersion(goMod)
	if err != nil {
		debugf("%v, fallback to 'unknown'", err)
		return "unknown"
	}

	moduleAtVersion := coreModule + "@" + version
	debugf("running: go mod download -json %s", moduleAtVersion)

	cmd := exec.Command("go", "mod", "download", "-json", moduleAtVersion)
	cmd.Dir = filepath.Dir(goMod)
	out, err := cmd.Output()
	if err != nil {
		debugf("go mod download failed: %v", err)
		return "unknown"
	}

	var info moduleDownloadInfo
	if err := json.Unmarshal(out, &info); err != nil {
		debugf("failed to parse go mod download output: %v", err)
		return "unknown"
	}
	if info.Origin.Hash == "" {
		debugf("Origin.Hash missing from go mod download output (GOPROXY 可能未透传该字段)")
		return "unknown"
	}

	hash := info.Origin.Hash
	if len(hash) > 7 {
		hash = hash[:7]
	}
	debugf("resolved commit=%s", hash)
	return hash
}

// ── numeric（semver → 4 段数字版本，供 Windows 资源版本号使用）──────────────

var numericVersionRe = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)(?:[^0-9]*(\d+))?`)

// toNumericVersion 将形如 "v1.2.3-beta.5" 的版本号转换为 Windows 资源
// 版本号要求的 4 段纯数字格式 "1.2.3.5"；无法解析时兜底为 "1.0.0.0"。
func toNumericVersion(version string) string {
	v := strings.TrimPrefix(strings.TrimSpace(version), "v")
	if v == "" || v == "dev" {
		v = "0.0.0"
	}
	m := numericVersionRe.FindStringSubmatch(v)
	if m == nil {
		return "1.0.0.0"
	}
	build := m[4]
	if build == "" {
		build = "0"
	}
	return fmt.Sprintf("%s.%s.%s.%s", m[1], m[2], m[3], build)
}

// ── gen-syso（生成 Windows 版本资源 / manifest 临时文件）────────────────────

// assemblyIdentityRe 只替换目标程序自身的 assemblyIdentity version 属性，
// 不影响其后的依赖项（如 Microsoft.Windows.Common-Controls）。
var assemblyIdentityRe = regexp.MustCompile(`(name="com\.sinspired\.subs-check-pro-gui"\s+version=")[^"]*"`)

func genSyso(infoIn, manifestIn, infoOut, manifestOut, version string) {
	numeric := toNumericVersion(version)
	display := strings.TrimPrefix(strings.TrimSpace(version), "v")
	if display == "" || display == "dev" {
		display = "0.0.0"
	}

	// info.json：注入 file_version（数字版本）与各语言 ProductVersion（显示版本）
	raw, err := os.ReadFile(infoIn)
	if err != nil {
		die("read %s: %v", infoIn, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		die("parse %s: %v", infoIn, err)
	}

	if fixed, ok := doc["fixed"].(map[string]any); ok {
		fixed["file_version"] = numeric
	}
	info, _ := doc["info"].(map[string]any)
	if info == nil {
		info = map[string]any{}
		doc["info"] = info
	}
	for _, lang := range info {
		if langMap, ok := lang.(map[string]any); ok {
			if _, has := langMap["ProductVersion"]; has {
				langMap["ProductVersion"] = display
			}
		}
	}
	// 确保常用 LCID（简体中文 / 英文）都带上 ProductVersion
	for _, lcid := range []string{"0000", "0409"} {
		langMap, ok := info[lcid].(map[string]any)
		if !ok {
			langMap = map[string]any{}
			info[lcid] = langMap
		}
		langMap["ProductVersion"] = display
	}

	out, err := json.MarshalIndent(doc, "", "\t")
	if err != nil {
		die("marshal info.json: %v", err)
	}
	if err := os.WriteFile(infoOut, out, 0o644); err != nil {
		die("write %s: %v", infoOut, err)
	}

	// manifest：只替换本程序自身 assemblyIdentity 的 version 属性
	manifestRaw, err := os.ReadFile(manifestIn)
	if err != nil {
		die("read %s: %v", manifestIn, err)
	}
	patched := assemblyIdentityRe.ReplaceAllString(string(manifestRaw), `${1}`+numeric+`"`)
	if err := os.WriteFile(manifestOut, []byte(patched), 0o644); err != nil {
		die("write %s: %v", manifestOut, err)
	}

	fmt.Printf("generate:syso  display=%s numeric=%s\n", display, numeric)
}
