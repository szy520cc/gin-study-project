/* =========================================================
   Starlark 规则编辑器（零依赖，离线可用）
   原生 textarea 之上叠加：语法高亮 + 行号 + Tab/自动缩进 +
   键入「control」唤起字段下拉（远程搜索）+ 选中插入占位符 +
   已引用字段面板。值经原生 setter + input 事件回写，与 amis 表单同步。

   用法：amis textarea 配 "className": "rule-editor" 自动增强；
        手动亦可 window.RuleEditor.mount(textareaEl, {projectId:'1'})。
   降级：脚本未加载时 textarea 保持原生可用。
   ========================================================= */
'use strict';
(function () {
  var TRIGGER = 'control';
  var API_FIELDS = '/api/v1/fields/list';
  var INDENT = '    ';
  var KEYWORDS = 'def return if elif else for while in not and or is None True False load lambda break continue pass global'.split(' ');
  var BUILTINS = 'len int str float bool list dict tuple set min max abs round range sorted enumerate zip any all print'.split(' ');
  var TOKEN_RE = /(##\d+\*\*[^#]+##)|(#[^\n]*)|('''[\s\S]*?'''|"""[\s\S]*?"""|'(?:\\.|[^'\\\n])*'|"(?:\\.|[^"\\\n])*")|(\b\d+(?:\.\d+)?\b)|([A-Za-z_]\w*)/g;
  var VAR_RE = /##(\d+)\*\*([^#]+)##/g;
  var SEARCH_RE = /(^|[^\w])control([\w\u4e00-\u9fa5]*)$/;

  function esc(s) {
    return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;')
      .replace(/>/g, '&gt;').replace(/"/g, '&quot;');
  }
  function projectId() {
    return (window.RuleEditorContext && window.RuleEditorContext.projectId)
      ? String(window.RuleEditorContext.projectId) : '';
  }
  function lineOf(v, pos) { return String(v).slice(0, pos).split('\n').length; }

  /* ---------------- 高亮 ---------------- */
  function highlight(text) {
    var out = '', last = 0, m;
    TOKEN_RE.lastIndex = 0;
    while ((m = TOKEN_RE.exec(text)) !== null) {
      out += esc(text.slice(last, m.index));
      var raw = m[0];
      if (m[1]) {
        var vm = VAR_RE.exec(raw); VAR_RE.lastIndex = 0;
        var id = vm ? vm[1] : '', path = vm ? vm[2] : raw;
        out += '<span class="re-var" title="字段 #' + esc(id) + ' · ' + esc(path) + '">' + esc(path) + '</span>';
      } else if (m[2]) out += '<span class="re-comment">' + esc(raw) + '</span>';
      else if (m[3]) out += '<span class="re-str">' + esc(raw) + '</span>';
      else if (m[4]) out += '<span class="re-num">' + esc(raw) + '</span>';
      else if (m[5]) out += KEYWORDS.indexOf(raw) >= 0 ? '<span class="re-kw">' + esc(raw) + '</span>'
        : BUILTINS.indexOf(raw) >= 0 ? '<span class="re-func">' + esc(raw) + '</span>' : esc(raw);
      else out += esc(raw);
      last = m.index + raw.length;
    }
    return out + esc(text.slice(last)) + '\n';
  }

  /* ---------------- 下拉 ---------------- */
  function fetchFields(keyword, cb) {
    var qs = 'page=1&page_size=50';
    var pid = projectId();
    if (pid) qs += '&project_id=' + encodeURIComponent(pid);
    if (keyword) qs += '&name=' + encodeURIComponent(keyword);
    var headers = { 'Accept': 'application/json' };
    var token = '';
    try { token = localStorage.getItem('admin_token') || ''; } catch (e) { /* noop */ }
    if (token) headers['Authorization'] = 'Bearer ' + token;
    fetch(API_FIELDS + '?' + qs, { headers: headers, credentials: 'same-origin' })
      .then(function (r) { return r.json(); })
      .then(function (d) {
        if (!d || d.code !== 0) { cb([], (d && d.msg) || '加载字段失败'); return; }
        cb((d.data && d.data.list) || [], '');
      })
      .catch(function () { cb([], '加载字段失败'); });
  }

  /* ---------------- 实例 ---------------- */
  function mount(ta, opts) {
    if (!ta || ta.getAttribute('data-re-mounted')) return null;
    opts = opts || {};

    var wrap = document.createElement('div');
    wrap.className = 're-wrap';
    var toolbar = document.createElement('div');
    toolbar.className = 're-toolbar';
    toolbar.innerHTML = '键入「<b>' + TRIGGER + '</b>」快捷插入变量，继续输入可筛选；Tab 缩进；Ctrl+Space 手动唤起';
    var main = document.createElement('div');
    main.className = 're-main';
    var gutter = document.createElement('div');
    gutter.className = 're-gutter';
    var layers = document.createElement('div');
    layers.className = 're-layers';
    var hl = document.createElement('pre');
    hl.className = 're-highlight';
    hl.setAttribute('aria-hidden', 'true');
    var code = document.createElement('code');
    hl.appendChild(code);
    var dd = document.createElement('div');
    dd.className = 're-dropdown';
    dd.hidden = true;
    var refs = document.createElement('div');
    refs.className = 're-refs';

    ta.parentNode.insertBefore(wrap, ta);
    wrap.appendChild(toolbar);
    wrap.appendChild(main);
    wrap.appendChild(refs);
    main.appendChild(gutter);
    main.appendChild(layers);
    layers.appendChild(hl);
    layers.appendChild(ta);
    layers.appendChild(dd);
    ta.setAttribute('data-re-mounted', '1');

    var st = {
      ta: ta, wrap: wrap, code: code, gutter: gutter, dd: dd, refs: refs,
      items: [], active: 0, triggerFrom: -1, triggerKeyword: '',
      composing: false, debTimer: null, syncTimer: null
    };

    function commit() {
      var setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set;
      setter.call(ta, ta.value);
      ta.dispatchEvent(new Event('input', { bubbles: true }));
    }
    function syncScroll() {
      hl.scrollTop = ta.scrollTop;
      hl.scrollLeft = ta.scrollLeft;
      gutter.scrollTop = ta.scrollTop;
    }
    function render() {
      var v = ta.value;
      code.innerHTML = highlight(v);
      var cur = lineOf(v, ta.selectionStart || 0);
      var lines = v.split('\n').length, g = '';
      for (var i = 1; i <= lines; i++) g += '<span' + (i === cur ? ' class="re-active"' : '') + '>' + i + '</span>';
      gutter.innerHTML = g;
      // 已引用字段面板
      var out = '', m2, n = 0;
      VAR_RE.lastIndex = 0;
      while ((m2 = VAR_RE.exec(v)) !== null) { n++; out += '<span class="re-ref" title="字段 #' + esc(m2[1]) + '">#' + esc(m2[1]) + ' ' + esc(m2[2]) + '</span>'; }
      refs.innerHTML = n ? '已引用 ' + n + ' 个字段：' + out : '尚未引用字段（键入 control 选择）';
      syncScroll();
    }

    function ddTip(msg, cls) {
      dd.innerHTML = '<div class="re-dd-tip"' + (cls ? ' style="color:#dc2626"' : '') + '>' + esc(msg) + '</div>';
      dd.hidden = false;
    }
    function ddRender(list) {
      if (!list.length) { ddTip('没有匹配的字段'); return; }
      st.items = list; st.active = 0;
      var h = '';
      for (var i = 0; i < list.length; i++) {
        var f = list[i];
        h += '<button type="button" class="re-item' + (i === 0 ? ' is-active' : '') + '" data-i="' + i + '">' +
          '<span class="re-item-name">' + esc(f.name) + '</span>' +
          '<small>#' + f.id + ' · ' + esc(f.parse_path) + (f.type_text ? ' · ' + esc(f.type_text) : '') + '</small></button>';
      }
      dd.innerHTML = h;
      dd.hidden = false;
    }
    function ddPosition() {
      var cur = lineOf(ta.value, ta.selectionStart || 0);
      var top = (cur - 1) * 20 - ta.scrollTop + 30;
      var max = layers.clientHeight - 120;
      dd.style.left = '10px';
      dd.style.top = (top < 0 || top > max ? Math.max(6, max) : top) + 'px';
    }
    function openDD(keyword) {
      ddPosition();
      st.triggerKeyword = keyword || '';
      ddTip('搜索字段中…');
      clearTimeout(st.debTimer);
      st.debTimer = setTimeout(function () {
        fetchFields(st.triggerKeyword, function (list, err) {
          if (dd.hidden && list.length === 0) return;
          if (err) ddTip(err, 1);
          else ddRender(list);
        });
      }, 180);
    }
    function closeDD() {
      st.items = []; clearTimeout(st.debTimer);
      dd.hidden = true; dd.innerHTML = '';
    }
    function selectItem(f) {
      var insert = '##' + f.id + '**' + (f.parse_path || f.name) + '##';
      var from = st.triggerFrom >= 0 ? st.triggerFrom : ta.selectionStart;
      var to = ta.selectionStart;
      if (typeof ta.setRangeText === 'function') {
        ta.setRangeText(insert, from, to, 'end');
      } else {
        ta.value = ta.value.slice(0, from) + insert + ta.value.slice(to);
        ta.selectionStart = ta.selectionEnd = from + insert.length;
      }
      st.triggerFrom = -1;
      closeDD();
      commit(); render(); ta.focus();
    }

    function onScroll() { syncScroll(); }
    function onFocus() { render(); }
    function onActive() { render(); }

    function onInput() {
      render();
      if (st.composing) { return; }
      var pos = ta.selectionStart;
      var before = ta.value.slice(0, pos);
      var m = SEARCH_RE.exec(before);
      if (m) {
        st.triggerFrom = pos - (TRIGGER.length + m[2].length);
        openDD(m[2]);
      } else {
        closeDD();
      }
    }

    function indentSelection(forward) {
      var s = ta.selectionStart, e = ta.selectionEnd;
      var startLine = s === 0 ? 0 : String(ta.value).lastIndexOf('\n', s - 1) + 1;
      var v = ta.value;
      var endLine = v.indexOf('\n', e); if (endLine < 0) endLine = v.length;
      var seg = v.slice(startLine, endLine);
      var lines = seg.split('\n');
      var ns = s - startLine, ne = e - startLine;
      for (var i = 0; i < lines.length; i++) {
        if (forward) { lines[i] = INDENT + lines[i]; ns += i === 0 ? 0 : 0; }
        else if (lines[i].indexOf(INDENT) === 0) lines[i] = lines[i].slice(4);
      }
      var newSeg = lines.join('\n');
      var newStart = startLine + ns;
      var newEnd = startLine + ns + (e - s);
      v = v.slice(0, startLine) + newSeg + v.slice(endLine);
      setValueRange(v, newStart, newEnd);
    }
    function setValueRange(v, selS, selE) {
      var setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set;
      setter.call(ta, v);
      ta.setSelectionRange(selS, selE == null ? selS : selE);
      commit(); render();
    }
    function handleEnter() {
      var v = ta.value, pos = ta.selectionStart;
      var ln = v.lastIndexOf('\n', pos - 1);
      var head = v.slice(0, pos);
      var line = head.slice(ln + 1);
      var ind = /^[ \t]*/.exec(line)[0];
      var add = /:\s*$/.test(line.replace(/^\s*/, '').replace(/#.*$/, '')) ? INDENT : '';
      var nl = '\n' + ind + add;
      ta.setRangeText(nl, pos, pos, 'end');
      commit(); render();
    }
    function handleTab(e) {
      if (!dd.hidden) {
        var f = st.items[st.active]; if (f) selectItem(f);
        e.preventDefault(); return true;
      }
      if (e.shiftKey) {
        if (ta.selectionStart !== ta.selectionEnd) { indentSelection(false); e.preventDefault(); return true; }
        // 单行无选区反缩进：移除行首缩进
        var pos2 = ta.selectionStart;
        var ln2 = pos2 === 0 ? 0 : ta.value.lastIndexOf('\n', pos2 - 1) + 1;
        if (ta.value.slice(ln2, ln2 + 4) === INDENT) {
          setValueRange(ta.value.slice(0, ln2) + ta.value.slice(ln2 + 4), Math.max(0, pos2 - 4), Math.max(0, ta.selectionEnd - 4));
        }
        e.preventDefault(); return true;
      }
      if (ta.selectionStart !== ta.selectionEnd) { indentSelection(true); e.preventDefault(); return true; }
      var pos = ta.selectionStart;
      ta.setRangeText(INDENT, pos, pos, 'end');
      commit(); render();
      e.preventDefault(); return true;
    }

    function openDDByKey() {
      st.triggerFrom = -1;
      ddPosition();
      openDD('');
    }
    function onKeyDown(e) {
      if (st.composing) return;
      // 系统组合键（复制/粘贴/剪切/全选/查找等）：收起下拉并放行默认行为
      if ((e.ctrlKey || e.metaKey) && !dd.hidden && e.key && /^(c|C|x|X|v|V|z|Z|y|Y|a|A|s|S|f|F)$/.test(e.key)) {
        closeDD();
        return;
      }
      // Ctrl+Space / Ctrl+I 手动唤起（不收起下拉时也允许直接触发）
      if ((e.ctrlKey || e.metaKey) && (e.key === ' ' || e.code === 'Space' || e.key === 'i' || e.key === 'I')) {
        e.preventDefault();
        openDDByKey();
        return;
      }
      if (!dd.hidden) {
        if (e.key === 'ArrowDown') { e.preventDefault(); st.active = (st.active + 1) % st.items.length; markActive(); return; }
        if (e.key === 'ArrowUp') { e.preventDefault(); st.active = (st.active - 1 + st.items.length) % st.items.length; markActive(); return; }
        if (e.key === 'Enter' || e.key === 'Tab') { e.preventDefault(); var f = st.items[st.active]; if (f) selectItem(f); return; }
        if (e.key === 'Escape') { closeDD(); e.preventDefault(); return; }
      }
      // 单独按下 Ctrl 键（无组合）直接唤起字段下拉
      if (e.key === 'Control' && !e.repeat) {
        e.preventDefault();
        openDDByKey();
        return;
      }
      if (e.key === 'Tab') { handleTab(e); return; }
      if (e.key === 'Enter') { handleEnter(); e.preventDefault(); return; }
      if (e.key === 'ArrowUp' || e.key === 'ArrowDown' || e.key === 'PageUp' || e.key === 'PageDown') {
        setTimeout(render, 0);
      }
    }
    function markActive() {
      var els = dd.querySelectorAll('.re-item');
      for (var i = 0; i < els.length; i++) els[i].classList.toggle('is-active', i === st.active);
    }

    function onDocDown(e) {
      if (dd.hidden) return;
      if (dd.contains(e.target) || ta.contains(e.target)) return;
      closeDD();
    }

    // 事件绑定
    ta.addEventListener('input', onInput);
    ta.addEventListener('scroll', onScroll, true);
    ta.addEventListener('focus', onFocus);
    ta.addEventListener('click', onActive);
    ta.addEventListener('keyup', onActive);
    ta.addEventListener('compositionstart', function () { st.composing = true; });
    ta.addEventListener('compositionend', function () { st.composing = false; onInput(); });
    // Tab/Enter/Ctrl 等在编辑器容器捕获阶段处理：焦点在编辑器内部任意位置都生效，
    // 且下拉项按钮获焦时 Tab 也不会误跳表单下一个输入框。
    st.onKeyDown = onKeyDown;
    wrap.addEventListener('keydown', st.onKeyDown, true);
    dd.addEventListener('mousedown', function (e) { e.preventDefault(); });
    dd.addEventListener('click', function (e) {
      var btn = e.target.closest ? e.target.closest('.re-item') : null;
      if (btn) selectItem(st.items[Number(btn.getAttribute('data-i'))]);
    });
    st.onDocDown = onDocDown;
    document.addEventListener('mousedown', st.onDocDown, true);

    render();
    // initApi 异步回显（React 直接改 value 不触发 input）→ 低频轮询兜底
    st.syncTimer = setInterval(function () {
      if (!document.contains(ta)) { unmount(st); return; }
      if (ta.value !== st.last) render();
    }, 300);

    var handle = { st: st, ta: ta };
    mounted.push(handle);
    return handle;
  }

  function unmount(h) {
    if (!h) return;
    var st = h.st;
    clearInterval(st.syncTimer); clearTimeout(st.debTimer);
    if (st.onDocDown) document.removeEventListener('mousedown', st.onDocDown, true);
    if (st.onKeyDown) st.wrap.removeEventListener('keydown', st.onKeyDown, true);
    // 还原 DOM
    var wrap = st.wrap;
    if (wrap && wrap.parentNode) wrap.parentNode.insertBefore(st.ta, wrap);
    if (wrap && wrap.parentNode) wrap.parentNode.removeChild(wrap);
    st.ta.removeAttribute('data-re-mounted');
    for (var i = 0; i < mounted.length; i++) if (mounted[i] === h) { mounted.splice(i, 1); break; }
  }

  /* ---------------- 自动挂载 ---------------- */
  // 全站规则内容统一用 name="rule" 的 textarea（新增/编辑/验证弹层），
  // 不依赖 amis 的 class 透传；rule-editor class 写法作为显式兜底。
  function autoload(root) {
    var scope = root || document;
    var list = scope.querySelectorAll('textarea[name="rule"]:not([data-re-mounted]), textarea.rule-editor:not([data-re-mounted])');
    for (var i = 0; i < list.length; i++) mount(list[i]);
  }
  function start() {
    autoload();
    if (typeof MutationObserver !== 'undefined') {
      var mo = new MutationObserver(function (muts) {
        var dirty = false;
        for (var i = 0; i < muts.length; i++) {
          if (muts[i].addedNodes && muts[i].addedNodes.length) { dirty = true; break; }
        }
        if (dirty) autoload();
      });
      mo.observe(document.body, { childList: true, subtree: true });
    }
  }
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', start);
  else start();

  window.RuleEditor = { mount: mount, unmount: unmount, autoload: autoload };
})();
