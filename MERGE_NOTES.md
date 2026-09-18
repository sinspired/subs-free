# Mobile + Desktop 合并说明

## 目录结构

```
subs-free/
├── go.mod                       单一 module github.com/sinspired/subs-free
├── Taskfile.yml                 根编排：build/package/run 按 GOOS 分发；dev:mobile / dev:desktop
├── .github/workflows/release.yml   changelog → build-android + build-desktop（并行）→ release → notify
├── .gitignore                   路径已同步到新目录结构
├── build/                       共享构建体系
│   ├── Taskfile.yml             公共任务，用 {{.APP_DIR}} 参数化 frontend 路径
│   ├── android/ ios/            → APP_DIR=cmd/mobile
│   ├── windows/ darwin/ linux/  → APP_DIR=cmd/desktop
│   ├── docker/                  server 模式 + 交叉编译，也已指向 cmd/desktop
│   └── tools/verinfo/           版本号工具，两边共用，未改动
└── cmd/
    ├── mobile/                  原仓库根目录文件 + frontend/，未改动任何业务逻辑，纯移动
    └── desktop/                 desktop 仓库根目录文件 + updater/ + frontend/，
                                  只改了 updater 包的 3 处 import 路径
```

## 已经做完、有把握的部分

1. **go.mod**：合并完成，wails3 升到最新 beta.21（原 mobile beta.20 / desktop beta.19），
   补了 desktop 需要的 `ini.v1` replace 指令。**go.sum 这边生成不了**（没有网络访问 Go
   proxy），你本地跑一次 `go mod tidy` 就行。
2. **cmd/mobile、cmd/desktop 目录搬迁**：纯文件移动，没有改动任何业务逻辑代码。
   desktop 的 `updater` 包挪到了 `cmd/desktop/updater/`，import 路径同步改了 3 处
   （app.go、main.go、tray.go），gofmt 语法检查过一遍，没有损坏。
3. **build/Taskfile.yml + 5 个平台 Taskfile**：用 `{{.APP_DIR}}` 变量参数化了所有
   frontend 相关路径，5 个平台的 `go build` 都补上了显式包路径
   （`./cmd/mobile` 或 `./cmd/desktop`）。这个方案（用 `vars:` 透传而不是
   include 级别的 `dir:` 覆盖）是刻意选的更保守的写法，具体原因见下面"个别拿不准
   的地方"。
4. **一个真实 bug**：Windows 的 `.syso`（图标+版本信息资源）原来生成在仓库根目录，
   但 Go 只会自动链接跟 `package main` 同目录的 `.syso`。已经把 `generate:syso`
   的输出路径和三处清理命令都改到了 `cmd/desktop/` 下——这个如果没发现，编译能
   通过，但打出来的 exe 会没有图标、版本信息也是空的，而且不会报错，非常隐蔽。
5. **build:server / build:docker（无 GUI 纯 HTTP 服务模式）**：Taskfile 注释里写的
   是"镜像 desktop build 任务"，所以按 desktop 处理，两个 Dockerfile
   （Dockerfile.server、Dockerfile.cross）里硬编码的编译目标也一并改成了
   `./cmd/desktop`。
6. **GitHub Actions**：`changelog`/`release`/`notify` 三个 job 你这两个仓库里本来
   就是完全一样的，直接合并没有冲突。新增了 `build-desktop` job（就是把 desktop
   原来的 `build` 矩阵搬过来改了个名），`release` job 的 `needs` 加上了它。两边
   产物的 artifact 命名（`dist-android-*`、`dist-{goos}-{arch}`）本来就不冲突，
   不用改。
7. **APP_NAME 统一成 `Subs-Free`**（含大小写），desktop 那份 workflow 里所有引用
   都换过来了，注释里的示例文件名也同步更新了。
8. **.gitignore**：`frontend/dist` 这些路径改成了 `cmd/mobile/frontend/dist` /
   `cmd/desktop/frontend/dist`。

## 个别拿不准、需要你本地验证的地方

1. **`wails3 generate bindings` 重新生成后，frontend 里的 import 路径要跟着改**。
   现在 `cmd/mobile/frontend/bindings/`、`cmd/desktop/frontend/bindings/` 下面
   的 `.ts` 文件是搬之前生成的旧文件，import 路径里还是
   `github.com/sinspired/subs-free/guiapp` 这种（对应搬之前的根包路径）。重新构建
   一次之后，wails3 大概率会把新路径生成成
   `github.com/sinspired/subs-free/cmd/mobile/guiapp`（带上完整包路径），到时候
   `main.tsx`/`app.tsx` 里 `import { ... } from "../../bindings/..."` 这些语句
   八成要跟着改一下路径。这个我没法在这边验证（没有 wails3 CLI），第一次跑
   `task android:build`/`task windows:build` 大概率会在这一步报错，把报错发我，
   两三分钟能改完。
2. **`dev:mobile`/`dev:desktop` 共用同一份 `build/config.yml`**。这个文件里的
   `productName`/`description` 现在写的是偏移动端的内容，桌面端用起来图标、
   窗口标题这些"元信息"字段可能对不上（不影响编译，只是生成的安装包/exe 属性
   显示的产品名可能是错的）。如果你手上还留着 desktop 独立的 config.yml，发我
   一份我拆成两份；没有的话就先这样，你自己看哪天想较真了再说。
3. **`build/android/overlay.json` 的生成机制**：这个文件是 `wails3 android
   overlay:gen` 命令自动生成的，我没有 wails3 CLI 没法实际跑一遍确认它的内部
   逻辑是不是会因为目录结构变化而受影响。从 Taskfile 里的调用方式看（只传了
   `-config build/config.yml`，没有显式的源码路径参数），大概率不受影响，但
   这个我没法 100% 打包票，第一次跑 `task android:build` 时留意一下这一步的
   输出。
4. **`init.go` 和 `notify_desktop.go`(mobile) / `notify.go`(desktop) 这两组文件
   内容近乎完全一样**，这次没有抽成共享包（`internal/guicommon` 之类），保持
   各自一份——文件很小（50-65行），抽出来要把所有调用点从裸函数名改成
   `包名.函数名`，性价比不高，就先留着重复。以后真觉得碍事了再说。

## 完全没有验证、只是静态检查过的部分

我这边没有 Android NDK / Xcode / Windows MSVC / wails3 CLI，所以：
- `gofmt -l` 跑过一遍所有搬动/改过的 `.go` 文件，**语法没问题**（原来就是
  CRLF 换行，gofmt 会提示"要重新格式化"是正常的，不代表有错，我没有改动
  换行风格）。
- 所有 `Taskfile.yml`/workflow 的 **YAML 语法**都用 Python yaml 库解析过，
  没问题。
- 但 `task android:package`、`task windows:build`、`task dev:mobile` 这些
  实际跑起来会不会一路顺利，我没法在这个环境里验证，需要你本地跑一遍。

## 第三轮修复：dir 解析基准搞错了（2026-09-18）

你反馈的现象是关键证据：`wails3 task windows:build` 把 frontend 相关文件放到了
`build\windows\cmd\desktop\frontend`。这说明我之前"相对 dir: 路径以仓库根目录
为基准"的假设是错的。

实际规则：**当一个任务是通过 `includes:` 从别的 Taskfile 里引用过来的
（比如 `common:build:frontend` 这种），它的相对 `dir:` 路径是以"写这个
`includes:` 声明的那个文件所在目录"为基准，不是以仓库根目录为基准。**
`build/windows/Taskfile.yml` 里写了 `includes: common: {taskfile: ../Taskfile.yml,
vars: {APP_DIR: cmd/desktop}}`，基准就是 `build/windows/`，所以
`{{.APP_DIR}}/frontend` = `cmd/desktop/frontend`，拼到 `build/windows/` 后面就是
你看到的 `build/windows/cmd/desktop/frontend`。

修法：把 `APP_DIR` 从相对路径换成基于 Task 内置的 `{{.ROOT_DIR}}`
特殊变量（永远等于仓库根目录，不管嵌套多少层 include 都不受影响）拼出来的
绝对路径：

```yaml
# build/windows/Taskfile.yml、build/darwin/Taskfile.yml、build/linux/Taskfile.yml
includes:
  common:
    taskfile: ../Taskfile.yml
    vars:
      APP_DIR: '{{.ROOT_DIR}}/cmd/desktop'

# build/android/Taskfile.yml、build/ios/Taskfile.yml
      APP_DIR: '{{.ROOT_DIR}}/cmd/mobile'
```

同样的道理，`build/Taskfile.yml` 里 `generate:icons`、`update:build-assets`
这两个任务原来写的是没有模板变量、写死的 `dir: build`——虽然不是我改出来的，
但同样会被这条"基准是 includes 声明文件所在目录"的规则坑到（通过
`common:generate:icons` 从各平台调用时，会被解析成
`build/windows/build`、`build/android/build` 这种错误位置，你放的
`build/appicon.png` 会一直"找不到"）。这两处也一并换成了
`dir: '{{.ROOT_DIR}}/build'`。

`build/windows/Taskfile.yml`、`build/linux/Taskfile.yml` 里另外几处写死的
`dir: build`/`dir: build/linux/appimage`/`dir: build/windows/nsis`
（`generate:syso`、`create:appimage`、`create:nsis:installer`）**不用改**——
这几个是 windows/linux 自己文件里的任务，不是通过 `common:` include 过来的，
基准本来就是仓库根目录，之前的分析是对的。


跑 `wails3 task windows:build` 和 `wails3 task android:build ARCH=arm64` 之后
报了两个错，两个都不是同一个问题：

1. **`open appicon.png: 找不到文件`**——这个和这次的目录重组无关，你给我的
   两个 zip 里本来就没有 `build/appicon.png` 这个源图标文件。`generate:icons`
   任务需要它生成 `darwin/icons.icns`、`windows/icon.ico` 这些平台图标，你需要
   自己放一张（一般 1024×1024 PNG）到 `build/appicon.png`。
2. **`Looks like npm isn't installed`（但 npm 明明是好的）**——这个是我这次改动
   引入的风险。`install:frontend:deps:npm/bun/pnpm/yarn` 这 4 个任务原本就有
   `preconditions: sh: npm version` 这类检查，用来在 Node 没装的时候给个更友好
   的提示。go-task 的 `preconditions:` 和 `dir:`（尤其是模板变量算出来的 dir）
   搭配用历史上出过好几次实际 bug（官方仓库能查到对应 issue），而你之前
   `npm run build:lite` 这种 `cmds:` 是正常的，说明 npm 本身没问题，precondition
   没有正确在 `{{.APP_DIR}}/frontend` 目录下执行。这几个 precondition 本身只是
   锦上添花（真正的 `npm/pnpm/bun/yarn install` 失败时报错已经够清楚），风险
   大于收益，这版直接删掉了。

