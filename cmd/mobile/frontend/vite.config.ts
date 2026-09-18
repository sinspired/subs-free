import { defineConfig } from "vite";
import wails from "@wailsio/runtime/plugins/vite";
import preact from "@preact/preset-vite"; // 引入插件
import path from "path";

// 是否为 Lite 构建：由 Taskfile（common:build:frontend）或 package.json 里的
// *:lite 脚本通过 LITE 环境变量传入，vite.bridge.config.ts 里也用同样的方式判断，
const isLite = process.env.LITE === "true";

export default defineConfig({
  server: {
    host: "127.0.0.1",
    port: Number(process.env.WAILS_VITE_PORT) || 9245,
    strictPort: true,
  },
  plugins: [
    wails("./bindings"),
    preact(), // 启用 preact 插件
  ],
  resolve: {
    alias: isLite
      ? [
          // Lite 构建时，在打包阶段把默认的 App / style 替换成 lite 版本，
          {
            find: /^\.\/App(\.tsx)?$/,
            replacement: path.resolve(import.meta.dirname, "src/App.lite.tsx"),
          },
          {
            find: /^\.\/style\.css$/,
            replacement: path.resolve(import.meta.dirname, "src/style.lite.css"),
          },
        ]
      : [],
  },
  build: {
    // 部分真机（尤其是系统 WebView 长期未更新的机型）不支持可选链 ?.、
    // 空值合并 ?? 等 ES2020+ 语法，esbuild 默认目标又偏新，
    // 不显式指定的话很容易在这类设备上直接 SyntaxError 白屏。
    // 这里降级到 es2019 兜底；chrome120 只是给 esbuild 一个现代基线做参考，
    // 实际生效的下限仍是二者中更旧的 es2019。
    target: ["es2019", "chrome120"],
    outDir: isLite ? "dist-lite" : "dist",
  },
});
