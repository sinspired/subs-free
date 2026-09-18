// frontend/src/wails-bridge.ts
import { GuiApp } from "../bindings/github.com/sinspired/subs-free/cmd/mobile";
import { Events } from "@wailsio/runtime"; // 引入 Events

declare global {
  interface Window {
    WailsBridge: {
      /**
       * 在应用进程内直接把请求灌入内核的 gin 路由器处理，完全不经过网络层。
       * 用于绕开 Android WebView 无法获取 POST 请求体的平台级限制。
       * 返回值为 JSON 字符串：{"status": <int>, "body": <string>}
       */
      APIProxy: (method: string, path: string, body: string) => Promise<string>;
      /**
       * Wails v3 事件系统，用于 GUI 与前端的事件通讯 (屏蔽动态引入 /runtime.js 的找不到文件问题)
       */
      Events: typeof Events;
      GetPublicInfo: () => Promise<{
        listenPort: string;
        subStorePort: string;
        subStorePath: string;
        guiVersion: string;
        coreVersion: string;
      }>;
      /**
       * 同步系统状态栏的图标颜色（深色/浅色背景各配对应图标）。原生方法调用
       * （$Call.ByID），不经过网络拦截层，跟 APIProxy 要绕开的那个安卓限制无关，
       * 桌面/安卓都能直接用。isDark 传当前是否为深色主题。
       */
      SetStatusBarAppearance: (isDark: boolean) => Promise<void>;

      /**
       *获取屏幕安全区
       */
      GetSafeArea: () => Promise<{ top: number; bottom: number; left: number; right: number }>;
    };
  }
}

window.WailsBridge = {
  APIProxy: GuiApp.APIProxy,
  GetPublicInfo: GuiApp.GetPublicInfo,
  Events: Events, // 暴露 Events 给外部原生 JS 使用
  SetStatusBarAppearance: GuiApp.SetStatusBarAppearance,
  GetSafeArea: GuiApp.GetSafeArea,
};