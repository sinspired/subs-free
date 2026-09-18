import { defineConfig } from "vite";
import wails from "@wailsio/runtime/plugins/vite";

// 动态判断当前是否为 Lite 构建
const isLite = process.env.LITE === "true";
const outDir = isLite ? "dist-lite" : "dist";

// 独立于 vite.config.ts 的第二个构建入口：只打包 wails-bridge.ts，
// 输出成一个不依赖 ES module 的 IIFE（frontend/dist/wails-bridge.js），
// 供 webui 包下的纯 HTML+JS 页面（admin.html/analysis.html/files.html）
// 用普通 <script src> 标签引入，从而能够调用 Wails 生成的绑定
// （目前是 APIProxy，用于绕开安卓 WebView 拿不到 POST 请求体的限制）。
//
// 使用方式：
//   vite build --config vite.bridge.config.ts
// 建议在 package.json 的 build 脚本里和主 app 构建一起跑，见下方 package.json 示例。
export default defineConfig({
  plugins: [wails("./bindings")],
  build: {
    outDir,
    // 不清空目录：主 app（vite.config.ts）已经把 index.html / assets 构建到这里了，
    // 这里只是追加 wails-bridge.js，两次构建互不影响。
    emptyOutDir: false,
    target: ["es2019", "chrome120"],
    lib: {
      entry: "src/wails-bridge.ts",
      name: "WailsBridgeLib",
      formats: ["iife"],
      fileName: () => "wails-bridge.js",
    },
  },
});
