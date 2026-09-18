import { defineConfig } from "vite";
import preact from "@preact/preset-vite";
import wails from "@wailsio/runtime/plugins/vite";
import { resolve } from "path";

// https://vitejs.dev/config/
export default defineConfig({
  server: {
    host: "127.0.0.1",
    port: Number(process.env.WAILS_VITE_PORT) || 9245,
    strictPort: true,
  },
  plugins: [preact(), wails("./bindings")],
  build: {
    rollupOptions: {
      // ── 多页应用（MPA）入口配置 ───────────────────────────────
      // 每增加一个新窗口，在此处添加一行即可：
      //   newpage: resolve(__dirname, "newpage.html")
      // 同时在 frontend/ 根目录创建对应 HTML 文件。
      input: {
        // 主窗口：登录 / 管理界面
        main: resolve(__dirname, "index.html"),
        // 关于窗口（独立 Wails 窗口）
        about: resolve(__dirname, "about.html"),
        // 订阅链接窗口（独立 Wails 窗口）
        "sub-links": resolve(__dirname, "sub-links.html"),
        // 检查更新窗口（独立 Wails 窗口，复用前端 Markdown 渲染等代码/库）
        updater: resolve(__dirname, "updater.html"),
        // 灵动岛独立窗口
        notch: resolve(__dirname, "notch.html"),
      },
    },
  },
});