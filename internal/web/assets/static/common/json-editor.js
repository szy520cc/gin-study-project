/* =========================================================
   JSON 编辑器 — 基于 div 的 amis 自定义表单项（type: "app-json-editor"）
   零依赖（复用 amis 内置 React），离线可用。

   设计要点：
   · 编辑区是 contenteditable 的 div（不是 textarea），值通过 amis 表单
     store 原生双向绑定（props.value / props.onChange），无隐藏域、无 DOM 偷换；
   · 注册方式：amisRequire('amis-core').FormItem({type:'app-json-editor'})(Component)，
     按需加载、幂等，不依赖 window.AmisCustomRenderers（该钩子会重复注册报错）；
     类型名必须避开 amis 内置的 "json-editor"（代码编辑器版，需额外 codemirror 分片）；
   · React 取自 amis 内部同一实例（amisRequire('react')），hooks 才能生效；
   · 本文件须在 sdk.min.js 之后加载。

   编写体验（对齐 fmtjson.com 那类 Monaco JSON 编辑器）：
   · 自动缩进：回车时按当前行缩进 + 前一个非空字符是 { / [ 则再加深一级；
     光标正卡在 } / ] 之前时，回车补成「开括号行 + 空行 + 闭括号行」三行；
   · 自动配对：输入 { [ " 自动补出配对的 } ] " 并把光标留在中间（有选区则包裹选区）；
   · 越过闭合符：光标后正好是 } ] 时再输入同一字符，只移动光标不再重复插入；
   · 输入 } ] 且本行光标前只有空白时，自动回退一级缩进；
   · 粘贴：若内容本身是合法 JSON，自动美化后再插入；
   · 失焦：合法 JSON 自动美化（非法保持原样并标红）；
   · 工具栏「格式化 / 压缩」按钮；Tab 插入 2 空格。

   用法（amis schema）：
     { "type": "app-json-editor", "name": "data", "label": "目标参数（JSON）",
       "required": true, "validations": { "isJson": true } }
   ========================================================= */
'use strict';
(function () {
  if (window.__jsonEditorInstalled) return;
  window.__jsonEditorInstalled = true;

  var ReactRef = null;
  var INDENT = '  '; // 一级缩进 2 空格（与 JSON 常规风格一致）

  /* =========================================================
     工具：JSON 解析 / 美化 / 压缩
     ========================================================= */
  function parseState(text) {
    var s = (text == null ? '' : String(text)).trim();
    if (!s) return { valid: true, error: '' };
    try {
      JSON.parse(s);
      return { valid: true, error: '' };
    } catch (e) {
      return { valid: false, error: (e && e.message) ? e.message : '不是合法的 JSON' };
    }
  }
  function toPretty(text) { return JSON.stringify(JSON.parse(String(text).trim()), null, 2); }
  function toCompact(text) { return JSON.stringify(JSON.parse(String(text).trim())); }
  function tryPretty(text) {
    try { return JSON.stringify(JSON.parse(String(text).trim()), null, 2); }
    catch (e) { return null; }
  }

  /* =========================================================
     工具：语法着色（编辑区文字透明，颜色由这层负责渲染）
     ========================================================= */
  function esc(s) {
    return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;')
      .replace(/>/g, '&gt;').replace(/"/g, '&quot;');
  }

  // 字符串(可带冒号→键) | 数字 | 字面量 | 括号/逗号/冒号 | 空白/其它
  var TOKEN_RE = /("(?:\\.|[^"\\])*")([ \t]*:)?|(-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)|(true|false|null)|([{}\[\],:])|([\s\S])/g;

  function hlLineTokens(line, state) {
    var out = '';
    TOKEN_RE.lastIndex = 0;
    var m;
    while ((m = TOKEN_RE.exec(line)) !== null) {
      if (m.index === TOKEN_RE.lastIndex) TOKEN_RE.lastIndex++; // 防空匹配死循环
      if (m[1] !== undefined) {
        out += (m[2] !== undefined)
          ? '<span class="jt-key">' + esc(m[1]) + '</span>' + esc(m[2])
          : '<span class="jt-str">' + esc(m[1]) + '</span>';
      } else if (m[3] !== undefined) {
        out += '<span class="jt-num">' + esc(m[3]) + '</span>';
      } else if (m[4] !== undefined) {
        out += '<span class="jt-lit">' + esc(m[4]) + '</span>';
      } else if (m[5] !== undefined) {
        var ch = m[5], cls = '';
        if (ch === '{' || ch === '[') { state.depth++; cls = 'jt-b' + (state.depth % 3); }
        else if (ch === '}' || ch === ']') { cls = 'jt-b' + (state.depth % 3); state.depth = Math.max(0, state.depth - 1); }
        out += cls ? '<span class="' + cls + '">' + esc(ch) + '</span>' : '<span class="jt-punct">' + esc(ch) + '</span>';
      } else {
        out += esc(m[0]);
      }
    }
    return out;
  }

  /* 行号列文本：与内容行数严格一一对应（空内容也算 1 行） */
  function gutterNumbers(text) {
    var n = String(text == null ? '' : text).split('\n').length;
    var out = '';
    for (var i = 1; i <= n; i++) out += (i > 1 ? '\n' : '') + i;
    return out;
  }

  /* 逐行着色；行首空白单独包一层，用来画缩进参考虚线 */
  function highlight(text) {
    var s = String(text == null ? '' : text);
    if (s === '') return '<span class="je-ph">在此输入 JSON…</span>';
    var lines = s.split('\n');
    var state = { depth: 0 };
    var html = '';
    for (var i = 0; i < lines.length; i++) {
      if (i > 0) html += '\n';
      var line = lines[i];
      var m = /^[ \t]+/.exec(line);
      if (m) {
        html += '<span class="jt-ind">' + esc(m[0]) + '</span>';
        line = line.slice(m[0].length);
      }
      html += hlLineTokens(line, state);
    }
    return html;
  }

  /* =========================================================
     工具：DOM ↔ 纯文本（编辑区只由文本节点 + <br> 组成）
     ========================================================= */
  function readText(root) {
    var out = '';
    (function walk(node) {
      for (var i = 0; i < node.childNodes.length; i++) {
        var c = node.childNodes[i];
        if (c.nodeType === 3) out += c.nodeValue;
        else if (c.nodeType === 1) {
          if (c.nodeName === 'BR') out += '\n';
          else if (c.nodeName === 'DIV' || c.nodeName === 'P') {
            if (out && out.charAt(out.length - 1) !== '\n') out += '\n';
            walk(c);
            if (out && out.charAt(out.length - 1) !== '\n') out += '\n';
          } else walk(c);
        }
      }
    })(root);
    // Chrome 会往空的可编辑块里塞一个占位 <br>：那是「空行」不是换行符。
    // 不还原成空串，值里就会凭空多一个 \n，且与光标下标互相错位。
    if (out && !/[^\n]/.test(out)) return '';
    return out;
  }

  /* 把 (container, offset) 这个 DOM 位置换算成纯文本下标。
     走 Range 克隆 + readText，与取值函数共用同一套换算规则，避免两套算法错位。 */
  function offsetOfPoint(host, container, offset) {
    var r = document.createRange();
    try {
      r.selectNodeContents(host);
      r.setEnd(container, offset);
    } catch (e) { return -1; }
    return readText(r.cloneContents()).length;
  }

  /* 当前选区（纯文本下标）；不在编辑区内返回 null */
  function caretRangeOf(host) {
    var sel = window.getSelection();
    if (!sel || !sel.rangeCount) return null;
    var r = sel.getRangeAt(0);
    if (!host.contains(r.startContainer)) return null;
    var s = offsetOfPoint(host, r.startContainer, r.startOffset);
    var e = offsetOfPoint(host, r.endContainer, r.endOffset);
    if (s < 0 || e < 0) return null;
    return { start: s, end: e };
  }

  /* 把光标放到纯文本下标 off 处 */
  function setCaret(host, off) {
    var range = document.createRange();
    var pos = 0, done = false;
    (function walk(node) {
      for (var i = 0; i < node.childNodes.length; i++) {
        if (done) return;
        var c = node.childNodes[i];
        if (c.nodeType === 3) {
          var len = c.nodeValue.length;
          if (pos + len >= off) { range.setStart(c, Math.max(0, off - pos)); done = true; return; }
          pos += len;
        } else if (c.nodeType === 1) {
          if (c.nodeName === 'BR') {
            if (off <= pos) { range.setStartBefore(c); done = true; return; }
            if (off <= pos + 1) { range.setStartAfter(c); done = true; return; }
            pos += 1;
          } else { walk(c); if (done) return; }
        }
      }
    })(host);
    if (!done) { range.selectNodeContents(host); range.collapse(false); }
    else { range.collapse(true); }
    var sel = window.getSelection();
    sel.removeAllRanges();
    sel.addRange(range);
  }

  /* =========================================================
     组件
     ========================================================= */
  function JsonEditorControl(props) {
    var React = ReactRef;
    var hostRef = React.useRef(null);
    var hlRef = React.useRef(null);      // 语法着色层内层（跟着编辑区一起滚）
    var gutterRef = React.useRef(null);  // 行号列内层（只跟随纵向滚动）
    var emittedRef = React.useRef(null);
    var statusState = React.useState({ valid: true, error: '' });
    var status = statusState[0];
    var setStatus = statusState[1];

    var disabled = !!props.disabled || !!props.static;
    var value = props.value == null ? '' : String(props.value);

    function setHostText(host, text) {
      var next = text == null ? '' : String(text);
      if ((host.innerText || '') !== next) host.innerText = next;
    }
    function refreshStatus(text) {
      var r = parseState(text);
      setStatus(function (prev) {
        return (prev.valid === r.valid && prev.error === r.error) ? prev : r;
      });
    }
    function emit(next) {
      emittedRef.current = next;
      if (typeof props.onChange === 'function') props.onChange(next);
      refreshStatus(next);
    }
    function readNow() {
      var host = hostRef.current;
      return host ? readText(host) : '';
    }

    /* 行号列 / 着色层 / 编辑区是三层：文字只在着色层有颜色，编辑区文字透明、只负责光标与选区 */
    function syncScroll() {
      var host = hostRef.current;
      if (!host) return;
      if (hlRef.current) {
        hlRef.current.style.transform = 'translate(' + (-host.scrollLeft) + 'px,' + (-host.scrollTop) + 'px)';
      }
      if (gutterRef.current) {
        gutterRef.current.style.transform = 'translateY(' + (-host.scrollTop) + 'px)';
      }
    }
    function renderHl() {
      var text = readNow();
      if (hlRef.current) hlRef.current.innerHTML = highlight(text);
      if (gutterRef.current) gutterRef.current.textContent = gutterNumbers(text);
      syncScroll();
    }

    /* 用整段纯文本重写编辑区并复位光标（自动缩进/配对等编辑动作走这里） */
    function applyText(text, caret) {
      var host = hostRef.current;
      if (!host) return;
      setHostText(host, text);
      renderHl();
      emit(text);
      if (caret != null) {
        if (document.activeElement !== host) { try { host.focus(); } catch (e) {} }
        setCaret(host, caret);
      }
    }

    /* 外部值（回显 / initApi / 重置）同步进编辑区；忽略自己刚抛出去的值，避免光标跳动 */
    React.useEffect(function () {
      var host = hostRef.current;
      if (!host) return;
      if (emittedRef.current === value) return;
      setHostText(host, value);
      emittedRef.current = value;
      refreshStatus(value);
      renderHl();
    }, [value]);

    /* 首帧补一次着色（此时两个 ref 都已挂好） */
    React.useEffect(function () { renderHl(); }, []);

    /* 挂载：把 div 设为可编辑并写入首屏值。
       注意必须用 "true"，不能用 "plaintext-only"：
       amis 的 Table 快速编辑会在 document.body 上挂全局 keydown，其放行条件是
       target.tagName ∈ {INPUT,TEXTAREA} 或 target.contentEditable === "true"；
       "plaintext-only" 两者都不满足 → 上下左右四个方向键被 preventDefault，光标无法移动。
       富文本粘贴由 onPaste 强制转纯文本，取值也一律走 readText，保证内容始终是纯文本。 */
    var setHostRef = React.useCallback(function (el) {
      hostRef.current = el;
      if (!el || !el.setAttribute) return;
      el.setAttribute('contenteditable', disabled ? 'false' : 'true');
      if (!el.__jeInited) {
        el.__jeInited = true;
        var v = (emittedRef.current == null) ? value : emittedRef.current;
        setHostText(el, v);
        emittedRef.current = v;
      }
    }, [disabled]);

    function handleInput() {
      if (disabled) return;
      renderHl();
      emit(readNow());
    }

    /* ---------------- 智能编辑（对齐 Monaco JSON 的默认手感） ---------------- */

    /* 回车：沿用本行缩进；前一个非空字符是 {/[ 则加深一级；光标后紧跟 }/] 时补成三行 */
    function doEnter() {
      var host = hostRef.current;
      var range = caretRangeOf(host);
      if (!range) return false;
      var text = readText(host);
      var start = range.start, end = range.end;
      var lead = (/^[ \t]*/.exec(text.slice(end)) || [''])[0];
      end += lead.length; // 吞掉光标后紧邻的空白，避免残留
      var lineStart = text.lastIndexOf('\n', start - 1) + 1;
      var indent = (/^[ \t]*/.exec(text.slice(lineStart, start)) || [''])[0];
      var opened = /[{[]$/.test(text.slice(0, start).replace(/[ \t]+$/, ''));
      var closing = /^[}\]]/.test(text.slice(end).replace(/^[ \t]*/, ''));
      if (closing) {
        var ins = '\n' + indent + INDENT + '\n' + indent;
        applyText(text.slice(0, start) + ins + text.slice(end), start + 1 + indent.length + INDENT.length);
      } else {
        var ins2 = '\n' + indent + (opened ? INDENT : '');
        applyText(text.slice(0, start) + ins2 + text.slice(end), start + ins2.length);
      }
      return true;
    }

    /* 自动配对 { [ " ；有选区时用配对符包裹选区 */
    function autoPair(open, close) {
      var host = hostRef.current;
      var range = caretRangeOf(host);
      if (!range) return false;
      var text = readText(host);
      if (range.start !== range.end) {
        var inner = text.slice(range.start, range.end);
        applyText(text.slice(0, range.start) + open + inner + close + text.slice(range.end), range.end + open.length + close.length);
        return true;
      }
      var prev = text.charAt(range.start - 1);
      var next = text.charAt(range.start);
      if (open === '"' && /[\w\u4e00-\u9fa5]/.test(prev)) return false; // 正在手动闭合字符串
      if (/[\w\u4e00-\u9fa5]/.test(next) || next === close) return false; // 紧跟内容或已有闭合符，不补
      applyText(text.slice(0, range.start) + open + close + text.slice(range.end), range.start + open.length);
      return true;
    }

    /* 光标后正好是同一个闭合符：只越过，不重复插入 */
    function skipCloser(ch) {
      var host = hostRef.current;
      var range = caretRangeOf(host);
      if (!range || range.start !== range.end) return false;
      var text = readText(host);
      if (text.charAt(range.start) !== ch) return false;
      if (document.activeElement !== host) { try { host.focus(); } catch (e) {} }
      setCaret(host, range.start + 1);
      return true;
    }

    /* 输入 }/] 且本行光标前只有空白：先回退一级缩进再落字 */
    function dedentCloser(ch) {
      var host = hostRef.current;
      var range = caretRangeOf(host);
      if (!range || range.start !== range.end) return false;
      var text = readText(host);
      var lineStart = text.lastIndexOf('\n', range.start - 1) + 1;
      var prefix = text.slice(lineStart, range.start);
      if (!/^[ \t]+$/.test(prefix)) return false;
      var keep = prefix.slice(0, Math.max(0, prefix.length - INDENT.length));
      applyText(text.slice(0, lineStart) + keep + ch + text.slice(range.start), lineStart + keep.length + 1);
      return true;
    }

    function insertPlain(str) {
      var host = hostRef.current;
      var range = caretRangeOf(host);
      if (!range) return false;
      var text = readText(host);
      applyText(text.slice(0, range.start) + str + text.slice(range.end), range.start + str.length);
      return true;
    }

    function handleKeyDown(e) {
      if (disabled) return;
      if (e.isComposing || e.keyCode === 229) return;      // 输入法组词中，一律放行
      if (e.ctrlKey || e.metaKey || e.altKey) return;      // 组合键放行

      if (e.key === 'Tab') {
        e.preventDefault();
        insertPlain(INDENT);
        return;
      }
      if (e.key === 'Enter') {
        if (doEnter()) e.preventDefault();
        return;
      }
      if (e.key === '{' || e.key === '[') {
        if (autoPair(e.key, e.key === '{' ? '}' : ']')) e.preventDefault();
        return;
      }
      if (e.key === '"') {
        if (skipCloser('"') || autoPair('"', '"')) e.preventDefault();
        return;
      }
      if (e.key === '}' || e.key === ']') {
        if (skipCloser(e.key) || dedentCloser(e.key)) e.preventDefault();
        return;
      }
    }

    /* 粘贴一律落为纯文本；若内容本身是合法 JSON 则自动美化后插入 */
    function handlePaste(e) {
      if (disabled) return;
      e.preventDefault();
      var cd = e.clipboardData || window.clipboardData;
      var raw = cd ? cd.getData('text') : '';
      if (raw == null) return;
      var host = hostRef.current;
      var range = caretRangeOf(host);
      if (!range) return;
      var ins = tryPretty(raw);
      if (ins == null) ins = raw;
      var text = readText(host);
      applyText(text.slice(0, range.start) + ins + text.slice(range.end), range.start + ins.length);
    }

    function handleBlur() {
      if (disabled) return;
      var host = hostRef.current;
      if (!host) return;
      var cur = readNow();
      if (!parseState(cur).valid || !cur.trim()) return;
      try {
        var p = toPretty(cur);
        if (p !== cur) applyText(p, null);
      } catch (e) { /* 忽略：不改动原文 */ }
    }

    function applyTransform(fn) {
      if (disabled) return;
      var cur = readNow();
      if (!cur.trim()) return;
      var r = parseState(cur);
      if (!r.valid) { setStatus(r); return; }
      try {
        applyText(fn(cur), null);
      } catch (e) {
        setStatus({ valid: false, error: String((e && e.message) || e) });
      }
    }

    function toolBtn(label, fn) {
      return React.createElement('button', {
        type: 'button',
        className: 'je-btn',
        disabled: disabled,
        onMouseDown: function (e) { e.preventDefault(); },
        onClick: function () { applyTransform(fn); }
      }, label);
    }

    return React.createElement('div', {
      className: 'je-wrap'
        + (disabled ? ' is-disabled' : '')
        + (status.valid ? ' is-valid' : ' is-invalid')
    },
      React.createElement('div', { className: 'je-toolbar' },
        React.createElement('span', { className: 'je-hint' }, '自动缩进 · 括号补全 · Tab 2 空格'),
        toolBtn('格式化', toPretty),
        toolBtn('压缩', toCompact),
        React.createElement('span', { className: 'je-error' },
          status.valid ? '✓ 合法 JSON' : ('✕ ' + status.error))
      ),
      React.createElement('div', { className: 'je-main' },
        React.createElement('div', { className: 'je-gutter', 'aria-hidden': 'true' },
          React.createElement('div', { className: 'je-gutter-inner', ref: gutterRef })
        ),
        React.createElement('div', { className: 'je-hl', 'aria-hidden': 'true' },
          React.createElement('div', { className: 'je-hl-inner', ref: hlRef })
        ),
        React.createElement('div', {
          ref: setHostRef,
          className: 'je-proxy',
          spellCheck: false,
          onInput: handleInput,
          onKeyDown: handleKeyDown,
          onPaste: handlePaste,
          onScroll: syncScroll,
          onBlur: handleBlur
        })
      )
    );
  }
  JsonEditorControl.displayName = 'JsonEditorControl';

  /* ---------------- 注册为 amis 自定义表单项 ----------------
     不用 window.AmisCustomRenderers 钩子：amis-core 初始化会多次调用它，
     第二次即因重名抛 “The renderer with type "app-json-editor" has already exists”。
     直接调用 amis-core 导出的 FormItem HOC 注册，幂等且行为可控。 */
  function install(core, R) {
    if (window.__jsonEditorTypeInstalled) return;
    ReactRef = R; // 必须复用 amis 内部同一份 React，hooks 才与渲染共用 dispatcher
    core.FormItem({ type: 'app-json-editor', weight: 0, autoVar: false })(JsonEditorControl);
    window.__jsonEditorTypeInstalled = true;
  }

  /* ---------------- 载入 amis 内部 React / amis-core 后完成注册 ---------------- */
  (function boot() {
    var req = window.amisRequire || (window.amis && window.amis.require);
    if (typeof req !== 'function') { setTimeout(boot, 30); return; }
    req(['react'], function (m) {
      var R = (m && m.default) ? m.default : m;
      if (!R || typeof R.createElement !== 'function') { setTimeout(boot, 30); return; }
      req(['amis-core'], function (core) {
        try { install(core, R); }
        catch (e) { console.error('[json-editor] 注册自定义表单项失败：', e); }
      });
    });
  })();
})();
