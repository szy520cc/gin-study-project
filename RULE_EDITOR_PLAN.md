# Starlark 规则编辑器升级方案

> 目标：把「规则（Starlark）」从裸 `textarea` 升级为 **带语法高亮、行号、Tab 缩进、变量快捷插入** 的规则编辑器，
> 对齐《Starlark规则编辑器.png》《Ctrl触发选项.png》的交互：键入 `control` 唤起字段下拉 → 选中 → 插入光标处 → 变量以紫色标签呈现。
> 本文只做方案设计（含取舍与风险），不含最终代码。

---

## 1. 现状

| 项 | 现状 |
|---|---|
| 规则输入控件 | `textarea`（新增弹层 / 编辑弹层 / 验证弹层，共 3 处） |
| 字段引用方式 | 手写占位符 `##<字段ID>**<解析路径>##`，例如 `##209**material.vertical_type##` |
| 字段信息来源 | 只能去「指标管理」页查 ID 与解析路径后手抄 → **手动维护字段信息** 的痛点来源 |
| 前端底座 | amis **6.13.0**（本地 `static/amis/sdk.min.js`）+ Bootstrap 5.3（本地），**纯离线**，无 CDN |
| 后端字段接口 | `GET /api/v1/fields/list` 已支持 `project_id` 精确 + `name` 模糊 + `status` 过滤，返回 `id/name/type/parse_path/...` |
| 规则接口 | 新增 `POST /configs/rule/add`、保存 `POST /configs/rule/save`、详情 `GET /configs/rule/detail`、验证 `POST /configs/testrun` |

痛点归纳：**无高亮/无行号/Tab 不可用 + 字段占位符全靠手抄 + 写完才能发现语法错**。

---

## 2. 硬约束（决定方案走向）

1. **离线部署**：`index.html` 只加载本地资源，页面不能依赖任何 CDN。
2. **Monaco 资源缺失**：amis 的 `editor` 组件内置 Monaco 集成，但它固定从
   `./thirds/monaco-editor/min/vs/loader.js` 加载；当前 `static/amis/thirds/` 下只有 `@fortawesome/`。
   → 要用 amis 原生 `editor`，必须先下载 Monaco 运行时（裁剪后约 5–8MB）并放置到该路径。
3. **amis 未暴露编辑器实例**：即便 Monaco 就位，`control` 触发远程补全、自定义紫色高亮装饰
   需要拿到 monaco editor 实例注册 `CompletionItemProvider` / `deltaDecorations`。
   amis `editor` 的 JSON 配置项未公开这些入口（需写自定义 Renderer 或 hack），SDK 中亦未发现 `onMount` 类自定义钩子。
4. **无 React 外部引用**：SDK 内部打包了 React 18，但页面层拿不到 `window.React`，
   因此「写 amis 自定义 Renderer（JSX）」这条路在无构建环境下成本高。

---

## 3. 方案对比

| 方案 | 做法 | 依赖 | 工作量 | 风险 | 结论 |
|---|---|---|---|---|---|
| **A. Monaco 本地化**（amis 原生 editor） | 下载 monaco 到 `thirds/`，用 `editor` 组件 + 自定义补全 | 需下载 5–8MB 第三方资源 | 大 | amis 未暴露 editor 实例；补全/装饰接入不确定 | 备选（二期） |
| **B. 零依赖自研编辑器**（推荐） | `textarea` 之上叠加「高亮层 + 行号 + 自研变量下拉」，复用现有表单提交链路 | **无**（纯 JS/CSS，约 400 行） | 小 | DOM 挂载时机、React 受控值同步 | **一期主选** |
| **C. 自研全屏编辑模态框** | 点「编辑规则」打开自研 Bootstrap 模态框，保存/校验直连后端接口，绕开 amis 表单 | 无 | 中 | 脱离 amis 表单校验/提示体系 | 兜底（B 值同步失败时启用） |
| D. 妥协版（纯 amis：下拉 + 插入按钮） | `editor`/`textarea` + select + 「插入变量」按钮 | 无 | 极小 | 不是 `control` 触发，与截图交互差距大 | 不推荐 |

**推荐 B**：零下载、离线 100% 可用、不依赖 amis 私有 API、工作量最小且可完全复刻截图交互；
同时把编辑器做成 `mount/unmount` 接口化模块，未来可平滑替换为 A。

---

## 4. 推荐方案（B）设计

### 4.1 产物与目录

```
internal/web/assets/static/common/
├── rule-editor.js    新增：编辑器内核（IIFE，暴露 window.RuleEditor）
└── rule-editor.css   新增：高亮层/行号/紫色标签/下拉浮层样式（沿用现有主题 CSS 变量）
internal/web/assets/pages/rule.json        改造：3 处 textarea 标记 class，自动增强
internal/web/assets/index.html             改造：引入 rule-editor.js/css（带版本号）
internal/model/rule.go                     小改：RuleResponse 增补 project_id
internal/controller/rule.go                小改：GetRule 组装 project_id（可选）
```

### 4.2 运行时结构（DOM）

```html
<div class="re-wrap" data-mounted="1">
  <div class="re-toolbar">键入「control」可快捷插入变量，继续输入可筛选</div>
  <div class="re-main">
    <div class="re-gutter" aria-hidden="true">1\n2\n3…</div>          <!-- 行号 -->
    <div class="re-layers">
      <pre class="re-highlight" aria-hidden="true"><code>…</code></pre> <!-- 高亮层（紫色标签） -->
      <textarea class="re-input" name="rule">…</textarea>               <!-- 真实输入层（文字透明、光标可见） -->
      <div class="re-dropdown" hidden>…</div>                            <!-- 变量下拉浮层 -->
    </div>
  </div>
</div>
```

- 高亮层与 textarea **同字体（`ui-monospace/Consolas`）、同 `line-height`、同 `padding`、同 `tab-size:4`、`white-space:pre`**，滚动同步。
- textarea 前景色透明、`caret-color` 保留 → 看到的是高亮层，编辑的是 textarea，天然支持 IME、撤销栈、移动端。

### 4.3 能力清单（对齐截图）

| 能力 | 实现要点 |
|---|---|
| 语法高亮（Starlark≈Python） | 正则 token 化：注释 `#…`、字符串 `' " '''`、数字、关键字（`def/return/if/elif/else/for/in/not/and/or/None/True/False/load/result`）、内置函数、占位符 |
| **变量紫色标签** | 占位符 `##209**material.vertical_type##` → 渲染为 `<span class="re-var">资源类型·material.vertical_type</span>`（紫色胶囊，同截图） |
| 行号 + 当前行高亮 | 行号层随内容/换行重算；当前行加 `.re-active-line` |
| Tab / Shift+Tab | 插入 4 空格；选中多行整块缩进/反缩进 |
| Enter 自动缩进 | 继承上一行缩进；上一行以 `:` 结尾则多缩进一级（Python/Starlark 习惯） |
| **键入 `control` 唤起下拉** | 取光标前单词，`=== 'control'` 时打开浮层（同时支持 `Ctrl+Space` / `Ctrl+I` 快捷键兜底） |
| 远程字段搜索 | `GET /api/v1/fields/list?project_id=&name=<输入>&page=1&page_size=50`，复用现有 token 请求封装，防抖 200ms；`project_id` 由编辑弹层上下文提供（见 4.5） |
| 键盘导航 | ↑/↓ 移动、Enter/Tab 选中、Esc 关闭、点击选中；加载中/空态提示 |
| **插入光标位置** | 删除触发词 `control`，在光标处插入 `##<id>**<parse_path>##`，光标移到其后 |
| 值回写 amis 表单（关键） | 写 `textarea.value`（用原生 value setter 绕开 React value tracker）后 `dispatchEvent(new Event('input',{bubbles:true}))` → amis 表单值同步，提交链路不变 |
| 已引用字段面板（S3，解决“手动维护字段”） | 解析脚本中的占位符，列出「字段名 / ID / 解析路径 / 是否有效」，无效（字段已删除或改路径）高亮告警 |
| 语法校验（S3，可选） | 编辑器内「校验」按钮 → 新增轻量接口 `POST /configs/rule/validate`（只编译不保存），即时反馈行号与错误 |

### 4.4 模块接口（便于后期替换实现）

```js
window.RuleEditor = {
  mount(textareaEl, { projectId, triggerWord = 'control' }),  // 返回 handle
  unmount(handle),
  autoload(root)   // 扫描 root 内 [data-rule-editor] 的 textarea 自动增强（含去重）
};
```

- 挂载时机：amis 弹层 DOM 是动态创建的 → 用 `MutationObserver` 监听页面容器，
  发现 `textarea[data-rule-editor]` 即挂载，并用 `data-mounted` 去重；弹层关闭时卸载。
- **降级**：脚本缺失/挂载失败时，textarea 保持原生可用，功能不受影响。

### 4.5 后端改动（极小）

| 改动 | 说明 |
|---|---|
| `RuleResponse` 增补 `project_id` | 让编辑器只展示该规则所属项目的字段，下拉更精准（字段少时不筛选也可） |
| （可选 S3）`POST /api/v1/configs/rule/validate` | 只做 `ValidateRule + CompileRule`，返回错误行号；不落库 |
| 其余 | 字段列表、规则保存、验证接口**均不动** |

### 4.6 三处改造点

| 位置 | 改造 |
|---|---|
| 新增规则弹层 | `textarea[name=rule]` 加 `"className": "rule-editor-input"`（或 `data-rule-editor`），`rows` 提到 16；`project_id` 由表单当前值提供 |
| 编辑规则弹层 | 同上；`initApi` adaptor 里把后端返回的 `project_id` 一并写入表单隐藏字段 |
| 验证弹层 | 同上（可只做高亮 + Tab，下拉也需要，因为常临时改脚本） |

---

## 5. 实施计划（分步可验收）

| 阶段 | 内容 | 验收标准 |
|---|---|---|
| **S0 风险验证（必做，约 0.5h）** | 最小注入实验：在 amis 弹层 textarea 上 ① 叠加高亮层；② 用 setter+input 事件回写值，提交看后端是否收到 | 确认「DOM 挂载时机」与「React 受控值同步」两条链路可行；失败则切换到方案 C（已在设计中预留） |
| **S1 编辑器骨架** | CSS + DOM 结构 + 高亮（含紫色占位符标签）+ 行号 + Tab/Shift+Tab + 自动缩进 + 滚动同步 | 新增/编辑弹层内代码有高亮与行号，Tab 可用，保存后入库内容与所见一致 |
| **S2 变量下拉** | `control` 触发、远程搜索（带 project 过滤）、键盘导航、插入占位符、值同步 | 键入 control → 出现字段列表 → 选中插入 `##id**path##` → 提交保存 → 「验证」用该规则可跑通 |
| **S3 体验与健壮** | 已引用字段面板、快捷键兜底、IME 处理、空态/错误态、主题适配（含深色）、三处弹层统一、可选语法校验接口 | 对照两张截图逐条核对；手写脚本与插入脚本两种路径都可用 |
| **S4（可选）** | 若后续需要 VSCode 级体验，按同一 `mount/unmount` 接口替换为 Monaco 实现 | 接口不变，页面零改动 |

估算：S0–S3 约 1 个工作日内可完成并自测。

---

## 8. 落地记录（2026-09-08）

**已实施（S0–S3 一次完成，含两轮真实浏览器验证）**

- 新增 `static/common/rule-editor.css` + `rule-editor.js`（window.RuleEditor，零依赖）；`index.html` 引入（v=20260908a）。
- 后端：`RuleResponse` 增补 `project_id`；`buildRuleResponse` 填充（config.ProjectID）。编译通过，无表结构变更。
- `rule.json`：新增/编辑/验证三处 rule 弹层 rows 提到 16/10；编辑/验证 `initApi` adaptor 写 `window.RuleEditorContext.projectId`；新增弹层 project select 的 `onEvent.change`（custom script）同步上下文，实现「按项目过滤字段」。
- 编辑器自动挂载策略：**`textarea[name="rule"]` + `.rule-editor` 双选择器**。经验证 amis 的 `className`/`inputClassName` 都不会落到原生 `<textarea>`（只作用于外层包装），因此不依赖 class 透传。

**验证结论（Edge headless 实测）**

| 项 | 结果 |
|---|---|
| 静态自测（无 amis）：挂载/高亮/行号/键入 control 触发/远程下拉/插入 `##id**path##`/引用面板/Tab 缩进 | 全 PASS |
| amis 真实渲染：弹层 DOM 中 `textarea[name=rule]` 自动挂载 | PASS |
| **编辑器改值 → amis 表单 store 同步**（static 联动回显证明） | PASS |
| 值同步方式 | 原生 value setter + `dispatchEvent(new Event('input'))`，绕过 React value tracker |
| 测试页清理 | 两个临时 self-test 页面已删除并从内嵌资源移除 |

**已知取舍**
- 编辑器默认在 amis 弹层内运行，行号/高亮用正则实现（Starlark≈Python 子集）；若后续要 VSCode 级能力可走 S4（同接口换 Monaco）。
- `triggerFrom` 触发词按产品原型定为键入文本 `control`，另支持 `Ctrl+Space`/`Ctrl+I` 手动唤起。

---

## 6. 风险与对策

| 风险 | 影响 | 对策 |
|---|---|---|
| React 受控 textarea 值不同步 | 编辑器内容无法提交 | S0 先验证；失败则用方案 C（自研模态框 + 直连接口保存），或改为写入 amis `hidden` 字段 |
| amis 弹层重建导致重复挂载/内存泄漏 | 界面异常 | MutationObserver + `data-mounted` 去重；弹层关闭卸载；销毁时移除监听 |
| 高亮层与输入层像素不对齐 | 视觉错位 | 统一字体/行高/padding/tab-size；禁止换行（`white-space: pre`）；用 `scrollTop/Left` 同步 |
| 中文输入法（IME）误触发下拉 | 输入中文时弹窗乱跳 | `compositionstart/end` 期间关闭触发与高亮重算 |
| 字段过多导致下拉慢 | 卡顿 | 防抖 200ms + `page_size=50` 上限 + 结果缓存（同 project+keyword） |
| 占位符被用户手改坏 | 引擎解析失败 | 已引用字段面板校验 ID/路径有效性；保存前前端轻量校验 + 后端既有校验兜底 |

---

## 7. 验收对照（截图）

- [ ] 编辑器有多行代码高亮与行号，Tab 缩进/自动缩进正常
- [ ] 键入 `control` 出现字段下拉，继续输入可过滤（远程接口）
- [ ] 选中项插入光标处，且以紫色标签呈现（实际文本仍是 `##id**path##`）
- [ ] 保存后列表/详情可见；「验证」可用真实参数执行通过
- [ ] 纯离线可用（断网/无 CDN 依赖）；脚本失效时降级为原生 textarea 不报错
