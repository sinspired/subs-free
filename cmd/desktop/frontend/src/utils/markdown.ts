/**
 * markdown.ts
 * Markdown → 安全 HTML 转换工具（基于 marked + DOMPurify）
 *
 * 用于「关于」窗口检查更新面板渲染 GitHub Release Notes。
 *
 * - marked：负责 GFM 语法解析（标题、列表、表格、删除线、
 *   自动链接、围栏代码块、软换行等），覆盖比手写正则解析器
 *   更完整的 CommonMark/GFM 边界情况。
 * - DOMPurify：对 marked 输出的 HTML 做白名单清洗，
 *   防止 Release Notes 中混入的恶意 HTML/脚本造成 XSS。
 *
 * 依赖：
 *   npm install marked dompurify
 *   npm install -D @types/dompurify
 */
import { marked } from 'marked';
import DOMPurify from 'dompurify';

// GFM 风格：支持表格、删除线、任务列表等；
// breaks: true 让单个换行也渲染为 <br>，更贴近 GitHub Release 页面的显示效果。
marked.setOptions({
  gfm: true,
  breaks: true,
});

/**
 * 允许通过的标签白名单。
 * 仅保留 Release Notes 中常见的结构/排版标签，
 * 不放行 <script>、<iframe>、<style>、事件属性等。
 */
const ALLOWED_TAGS = [
  'p', 'br', 'hr',
  'strong', 'em', 'del', 's',
  'code', 'pre',
  'h1', 'h2', 'h3', 'h4', 'h5', 'h6',
  'ul', 'ol', 'li',
  'a', 'img',
  'table', 'thead', 'tbody', 'tr', 'th', 'td',
  'blockquote',
];

const ALLOWED_ATTR = ['href', 'src', 'alt', 'title', 'target', 'rel'];

/**
 * 将 Markdown（GitHub Release Notes）转换为安全的 HTML 字符串。
 *
 * @param src 原始 Markdown 文本（可能包含 \r\n）
 * @returns   经过清洗、可直接用于 dangerouslySetInnerHTML 的 HTML 字符串
 */
export function md2html(src: string): string {
  if (!src) return '';

  // 统一换行符，避免 \r\n 在某些解析路径下产生多余空行
  const normalized = src.replace(/\r\n/g, '\n').replace(/\r/g, '\n');

  const rawHtml = marked.parse(normalized, { async: false }) as string;

  return DOMPurify.sanitize(rawHtml, {
    ALLOWED_TAGS,
    ALLOWED_ATTR,
    // 外部链接统一交给 dangerouslySetInnerHTML 渲染后的
    // onClick 拦截器处理（见 AboutApp.tsx 的 handleNotesClick），
    // 这里不强制改写 target/rel，保留 marked 生成的属性。
  });
}