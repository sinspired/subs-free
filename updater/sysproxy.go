package updater

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// sysProxyAttemptTimeout 是第 1 层（系统代理直连）的连接/握手超时。
// 这一层只是"试一下走不走得通"，走不通就立即切到不经系统代理的兜底线路，
// 不能占用太多整体下载时间预算（预算见 MaxDownloadDuration）。兜底层不再
// 额外加短超时，只受调用方传入的 context 和 speedGuardTransport 约束。
const sysProxyAttemptTimeout = 8 * time.Second

// newSysProxyBaseTransport 通过系统代理（http.ProxyFromEnvironment 读取
// utils.GetSysProxy() 已设置好的 HTTP_PROXY/HTTPS_PROXY）转发请求。流量已
// 交给本地代理软件处理，DNS 污染、TLS 指纹限速都是代理软件自己要解决的
// 问题，这里不做额外伪装。
func newSysProxyBaseTransport() *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 60 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          10,
		IdleConnTimeout:       300 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

// fallbackTransport 按两层顺序尝试下载：
//  1. 系统代理可用 → 系统代理 + 原始地址（直连 GitHub，走本地代理软件）
//  2. 系统代理不可用，或第 1 层失败/超时 → 不再经过系统代理，改用
//     DoH+utls + GitHub 代理前缀兜底
//
// 之前还有一层"系统代理 + GitHub 代理前缀"夹在中间：系统代理软件本身已经
// 在处理直连，再叠加一层拼接了代理前缀的 URL 请求，链路更复杂、更容易在
// 系统代理与反代前缀的组合下出各种问题（例如下载完成后偶发不显示更新
// 日志），收益却很有限——去掉它，第 1 层一旦失败就直接、干净地切到完全
// 不依赖系统代理的兜底线路。
//
// 只安全用于无请求体的 GET 场景（Check/Download 均是 GET）：原始 req 在
// 第 1 层失败后仍未被消费，可以原样交给下一层重试。
type fallbackTransport struct {
	sysProxyAvailable bool
	sysProxyDirect    http.RoundTripper // 第 1 层：系统代理 + 原始地址
	ghProxy           http.RoundTripper // 第 2 层（兜底）：DoH+utls + GitHub 代理前缀，不经系统代理
}

func (t *fallbackTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.sysProxyAvailable {
		if resp, err := t.tryWithTimeout(t.sysProxyDirect, req, sysProxyAttemptTimeout); err == nil {
			return resp, nil
		} else {
			slog.Warn("系统代理直连 GitHub 失败，立即切换为不经系统代理的 GitHub 代理线路兜底",
				"url", req.URL.String(), "error", err)
		}
	}

	return t.ghProxy.RoundTrip(req)
}

// tryWithTimeout 用独立的短超时窗口判断"这一层线路是否连得通/反应够快"，
// 避免某一层卡死拖垮整体 fallback 流程。
//
// 关键点：net/http 用同一个 request context 控制"连接 + 响应头 + 响应体
// 读取"的全过程。之前的实现用 context.WithTimeout(req.Context(), timeout)
// 直接生成一个贯穿整个请求生命周期的 ctx，本意只是想限制"连接尝试"的
// 耗时，结果变成了"整个下载（含响应体读取）最多 timeout 秒"——对于几十
// 上百 MB 的安装包，下载到一半必然因为这个 ctx 到期而被腰斩，报出
// "context deadline exceeded"，即便当时网速完全正常。这正是"系统代理
// 开启、下载速度不慢，但下载没多久就必现失败"的根因。
//
// 修复：用 time.AfterFunc 起一个定时器，只在"RoundTrip 尚未返回（即还没
// 拿到响应头）"这段时间内代表"连接超时"去 cancel；一旦 RoundTrip 成功
// 返回（拿到响应头），立刻 Stop 定时器，解除这个短超时限制——后续响应体
// 读取只受调用方外层 context（整体下载预算）和 speedGuardTransport（限速
// 保护）约束，不会再被这个探测超时腰斩。
//
// cancel 的生命周期：失败路径（含探测超时触发）上会调用 cancel 释放资源；
// 成功路径上通过 newCancelOnCloseReader 把 cancel 传给响应体包装器，在
// 调用方 Close() 时才真正触发，两条路径最终都会调用 cancel，避免 context
// 泄漏（对应 go vet lostcancel 检查）。
func (t *fallbackTransport) tryWithTimeout(rt http.RoundTripper, req *http.Request, timeout time.Duration) (*http.Response, error) {
	ctx, cancel := context.WithCancel(req.Context())
	r := req.Clone(ctx)

	// 定时器只代表"探测阶段超时"：timeout 秒内还没拿到响应头，就认定这条
	// 线路不可用，主动 cancel 交给下一层 fallback；一旦 RoundTrip 返回就
	// 必须立刻 Stop，避免继续计时把已经在正常读取的下载腰斩。
	timer := time.AfterFunc(timeout, cancel)

	resp, err := rt.RoundTrip(r)
	timer.Stop()
	if err != nil {
		cancel()
		return nil, err
	}
	withBrowserHeaders(r)
	resp.Body = newCancelOnCloseReader(resp.Body, cancel)
	return resp, nil
}

// cancelOnCloseReader 包装 io.ReadCloser，在 Close() 时调用关联的
// context.CancelFunc，确保 tryWithTimeout 里创建的 context 无论下载成功
// 与否最终都会被释放。
type cancelOnCloseReader struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func newCancelOnCloseReader(rc io.ReadCloser, cancel context.CancelFunc) io.ReadCloser {
	return &cancelOnCloseReader{ReadCloser: rc, cancel: cancel}
}

func (r *cancelOnCloseReader) Close() error {
	err := r.ReadCloser.Close()
	r.cancel()
	return err
}