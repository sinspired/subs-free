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
//	verinfo android-code <version>                         semver 转 Android versionCode（单个正整数）
//	verinfo gen-syso <infoIn> <manifestIn> <infoOut> <manifestOut> <version>
//	                                                        生成 syso 所需的 info.json / manifest 临时文件
//
// 环境变量：
//
//	CORE_VERSION / CORE_COMMIT   手动覆盖，跳过自动探测
//	TASK_DEBUG=1                 打印调试信息到 stderr
//
// 版本号编码原则：结果【只由传入的版本字符串决定】，与构建时间、构建机器无关。
// 这样同一个 tag 无论重跑多少次、arm64/amd64/lite 各变体在哪一天编译，
// 产出的 versionCode 都完全一致，构建可复现；且 semver 的先后顺序
// （alpha < beta < rc < 正式版）与数值大小严格一致，Android 才会允许覆盖升级。
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
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
		die("missing subcommand (core-version | core-commit | numeric | android-code | gen-syso)")
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
	case "android-code":
		if len(os.Args) < 3 {
			die("android-code requires a version argument")
		}
		fmt.Println(toAndroidVersionCode(os.Args[2]))
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

// core-version

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

// core-commit

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

// semver 解析

var (
	semverRe     = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.\-]+))?(?:\+.*)?$`)
	prereleaseRe = regexp.MustCompile(`^([A-Za-z]+)[.\-]?(\d*)`)
)

type semver struct {
	major, minor, patch int
	pre                 string // 预发布标识，如 "rc.1"；正式版为空
	valid               bool   // false 表示 dev / 空 / 无法解析
}

func parseSemver(version string) semver {
	v := strings.TrimPrefix(strings.TrimSpace(version), "v")
	if v == "" || v == "dev" {
		return semver{}
	}
	m := semverRe.FindStringSubmatch(v)
	if m == nil {
		// 无法解析时直接报错，而不是悄悄当成 1.0.0：
		// 否则 tag 写错会得到一个错误（且可能偏低）的 versionCode，用户就无法升级。
		die("cannot parse version %q (expected vMAJOR.MINOR.PATCH[-prerelease])", version)
	}
	s := semver{valid: true, pre: m[4]}
	s.major, _ = strconv.Atoi(m[1])
	s.minor, _ = strconv.Atoi(m[2])
	s.patch, _ = strconv.Atoi(m[3])
	return s
}

// prereleaseTier 预发布类型的先后顺序：nightly < alpha < beta/preview < rc。
func prereleaseTier(name string) int {
	switch strings.ToLower(name) {
	case "nightly":
		return 1
	case "alpha":
		return 2
	case "beta", "preview":
		return 3
	case "rc":
		return 4
	default:
		return 0
	}
}

const (
	// 稳定版在“发布阶段”这一段里的取值：必须大于任何预发布值（最大 4999），
	// 同时也大于旧算法（5000 + 距 2024-01-01 的天数）在未来几年内可能产生的值，
	// 保证已经安装了旧算法构建的用户仍可覆盖升级。
	stageStable = 9000
)

// stageValue 返回 [0, 9999] 内的“发布阶段”值：预发布 = 类型*1000 + 序号(≤999)，正式版 = 9000。
func (s semver) stageValue() int {
	if s.pre == "" {
		return stageStable
	}
	m := prereleaseRe.FindStringSubmatch(s.pre)
	if m == nil {
		return 0
	}
	n := 0
	if m[2] != "" {
		n, _ = strconv.Atoi(m[2])
	}
	return prereleaseTier(m[1])*1000 + min(n, 999)
}

// ── numeric（semver → 4 段数字版本，供 Windows 资源版本号使用）──────────────
//
// Windows 版本号每段最大 65535。第 4 段：预发布 = 阶段值(0~4999)，正式版固定 60000
// （旧算法为 50000+天数，仍小于 60000，保证可覆盖升级）。
func toNumericVersion(version string) string {
	s := parseSemver(version)
	build := s.stageValue()
	if s.valid && s.pre == "" {
		build = 60000
	}
	return fmt.Sprintf("%d.%d.%d.%d", s.major, s.minor, s.patch, build)
}

// android-code（semver → Android versionCode，单个正整数）
//
// Android versionCode 上限 2_100_000_000。
// 编码：major*1e8 + minor*1e6 + patch*1e4 + stage
//
//	major ≤ 20，minor ≤ 99，patch ≤ 99，stage ∈ [0, 9999]
//
// 任意两个不同版本，semver 大者 versionCode 必大；
// 同一版本字符串在任何时间、任何机器上得到相同结果。
func toAndroidVersionCode(version string) string {
	s := parseSemver(version)
	if s.major > 20 || s.minor > 99 || s.patch > 99 {
		die("version %q out of range for android versionCode (major<=20, minor<=99, patch<=99)", version)
	}
	code := s.major*100_000_000 + s.minor*1_000_000 + s.patch*10_000 + s.stageValue()
	if code <= 0 {
		code = 1
	}
	return strconv.Itoa(code)
}

// gen-syso（生成 Windows 版本资源 / manifest 临时文件）

// assemblyIdentityRe 只替换目标程序自身的 assemblyIdentity version 属性，
// 不影响其后的依赖项（如 Microsoft.Windows.Common-Controls）。
var assemblyIdentityRe = regexp.MustCompile(`(name="com\.sinspired\.subs_free"\s+version=")[^"]*"`)

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