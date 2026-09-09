/* =========================================================
   Starlark 规则编辑器（零依赖，离线可用）— contenteditable 版
   用单一 contenteditable 层渲染：caret 与文字同层，浏览器原生对齐。
   字段变量以不可编辑 <span class="re-field"> 胶囊内嵌，提交时序列化成
   ##id**path## 写入隐藏的 name=rule textarea，与 amis 表单同步。

   字段交互约定：
   · 有且仅有「按下 Ctrl 键」时才会请求字段接口（翻页拉全量）；
   · 面板内置搜索框，输入即对已加载字段做本地过滤（不再触发接口）；
   · 选中字段插入到按下 Ctrl 那一刻的光标位置。

   用法：amis textarea 配 "className": "rule-editor" 自动增强；
        手动亦可 window.RuleEditor.mount(textareaEl, {projectId:'1'})。
   ========================================================= */
'use strict';
(function () {
  var API_FIELDS = '/api/v1/fields/list';
  var FIELD_RE = /##(\d+)\*\*([^#]+)##/g;
  var mounted = [];

  function esc(s) {
    return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;')
      .replace(/>/g, '&gt;').replace(/"/g, '&quot;');
  }
  function projectId() {
    return (window.RuleEditorContext && window.RuleEditorContext.projectId)
      ? String(window.RuleEditorContext.projectId) : '';
  }

  /* ---------------- 序列化 / 反序列化 ---------------- */
  function serialize(root) {
    var out = '';
    (function walk(node) {
      var kids = node.childNodes;
      for (var i = 0; i < kids.length; i++) {
        var c = kids[i];
        if (c.nodeType === 3) { out += c.nodeValue; }
        else if (c.nodeType === 1) {
          if (c.classList && c.classList.contains('re-field')) {
            out += '##' + c.getAttribute('data-id') + '**' + c.getAttribute('data-path') + '##';
          } else if (c.nodeName === 'BR') { out += '\n'; }
          else if (c.nodeName === 'DIV' || c.nodeName === 'P') {
            if (out && out.charAt(out.length - 1) !== '\n') out += '\n';
            walk(c);
            if (out && out.charAt(out.length - 1) !== '\n') out += '\n';
          } else { walk(c); }
        }
      }
    })(root);
    return out;
  }
  function deserialize(v) {
    var frag = document.createDocumentFragment();
    var last = 0, m;
    FIELD_RE.lastIndex = 0;
    while ((m = FIELD_RE.exec(v)) !== null) {
      if (m.index > last) frag.appendChild(document.createTextNode(v.slice(last, m.index)));
      frag.appendChild(makeField(m[1], m[2]));
      last = m.index + m[0].length;
    }
    if (last < v.length) frag.appendChild(document.createTextNode(v.slice(last)));
    return frag;
  }
  function makeField(id, path, name) {
    var span = document.createElement('span');
    span.className = 're-field';
    span.setAttribute('contenteditable', 'false');
    span.setAttribute('data-id', id);
    span.setAttribute('data-path', path);
    if (name && name !== path) span.setAttribute('data-name', name);
    var label = name || path;   // 优先显示字段名称；无名称（反序列化）时回退为路径
    span.setAttribute('title', '字段 #' + id + ' · ' + label);
    span.textContent = label;
    return span;
  }

  /* ---------------- 文本偏移 → DOM Range / caret ---------------- */
  function rangeForOffsets(root, start, end) {
    var range = document.createRange();
    var pos = 0;
    var startNode = null, startOffset = 0, endNode = null, endOffset = 0;
    (function walk(node) {
      if (endNode) return;
      if (node.nodeType === 3) {
        var len = node.nodeValue.length;
        if (startNode === null && pos + len >= start) { startNode = node; startOffset = start - pos; }
        if (pos + len >= end) { endNode = node; endOffset = end - pos; return; }
        pos += len;
      } else if (node.nodeType === 1) {
        var kids = node.childNodes;
        for (var i = 0; i < kids.length; i++) { walk(kids[i]); if (endNode) return; }
      }
    })(root);
    if (!startNode) { startNode = root; startOffset = 0; }
    if (!endNode) { endNode = root; endOffset = root.childNodes.length; }
    range.setStart(startNode, startOffset);
    range.setEnd(endNode, endOffset);
    return range;
  }
  function caretBeforeText(ed) {
    var sel = window.getSelection();
    if (!sel || !sel.rangeCount) return '';
    var range = sel.getRangeAt(0);
    var pre = document.createRange();
    pre.selectNodeContents(ed);
    try { pre.setEnd(range.startContainer, range.startOffset); } catch (e) { return ''; }
    return pre.toString();
  }
  function caretOffsetOf(ed) {
    return caretBeforeText(ed).length;
  }
  // ed 的总 DOM 文本长度（field span 的 textContent 算入）
  function totalLen(ed) {
    var pos = 0;
    (function walk(n) {
      if (n.nodeType === 3) pos += n.nodeValue.length;
      else if (n.nodeType === 1) for (var i = 0; i < n.childNodes.length; i++) walk(n.childNodes[i]);
    })(ed);
    return pos;
  }
  // 判断 offset 位置的左/右邻居类型
  function getNeighbor(ed, offset) {
    var total = totalLen(ed);
    var left = 'start', right = 'end';
    function kind(r) {
      if (!r || !r.startContainer) return 'space';
      // 先看宿主是否 re-field（text node 的父节点或容器本身），field 一律视为非空原子
      var host = (r.startContainer.nodeType === 3) ? r.startContainer.parentNode : r.startContainer;
      if (host && host.nodeType === 1 && host.classList && host.classList.contains('re-field')) return 'field';
      if (r.startContainer.nodeType === 3) {
        var ch = r.startContainer.nodeValue.charAt(r.startOffset);
        return (ch && !/\s/.test(ch)) ? 'char' : 'space';
      }
      return 'space';
    }
    if (offset > 0) { var r1 = rangeForOffsets(ed, offset - 1, offset); left = kind(r1); }
    if (offset < total) { var r2 = rangeForOffsets(ed, offset, offset + 1); right = kind(r2); }
    return { left: left, right: right };
  }

  /* ---------------- 字段加载（有且仅有 Ctrl 键触发时调用） ---------------- */
  function fetchFieldsPage(page, cb) {
    var qs = 'page=' + page + '&page_size=1000';
    var pid = projectId();
    if (pid) qs += '&project_id=' + encodeURIComponent(pid);
    var headers = { 'Accept': 'application/json' };
    var token = '';
    try { token = localStorage.getItem('admin_token') || ''; } catch (e) {}
    if (token) headers['Authorization'] = 'Bearer ' + token;
    fetch(API_FIELDS + '?' + qs, { headers: headers, credentials: 'same-origin' })
      .then(function (r) { return r.json(); })
      .then(function (d) {
        if (!d || d.code !== 0) { cb([], 0, (d && d.msg) || '加载字段失败'); return; }
        cb((d.data && d.data.list) || [], (d.data && d.data.total) || 0, '');
      })
      .catch(function () { cb([], 0, '加载字段失败'); });
  }
  // 翻页拉全量（每页 100）
  function fetchAllFields(cb) {
    var all = [], page = 1;
    (function next() {
      fetchFieldsPage(page, function (list, total, err) {
        if (err) { cb(all, err); return; }
        all = all.concat(list);
        if (all.length < total && list.length > 0) { page++; next(); }
        else cb(all, '');
      });
    })();
  }

  /* ---------------- 实例 ---------------- */
  function mount(ta, opts) {
    if (!ta || ta.getAttribute('data-re-mounted')) return null;
    opts = opts || {};

    var wrap = document.createElement('div');
    wrap.className = 're-wrap';
    var toolbar = document.createElement('div');
    toolbar.className = 're-toolbar';
    toolbar.innerHTML = '双击 <b>Ctrl</b> 获取字段列表，面板内可搜索过滤；Tab 缩进';
    var ed = document.createElement('div');
    ed.className = 're-editor';
    ed.setAttribute('contenteditable', 'plaintext-only');
    ed.setAttribute('spellcheck', 'false');
    ed.setAttribute('data-placeholder', '在此输入 Starlark 规则…');
    var refs = document.createElement('div');
    refs.className = 're-refs';

    // 下拉面板：搜索框 + 结果列表（挂到 body，避免被 .re-wrap 的 overflow:hidden 裁剪）
    var dd = document.createElement('div');
    dd.className = 're-dropdown';
    dd.hidden = true;
    dd.innerHTML = '<div class="re-dd-head"><input type="text" class="re-search" placeholder="搜索字段…"></div>' +
      '<div class="re-list"></div>';
    var searchEl = dd.querySelector('.re-search');
    var listEl = dd.querySelector('.re-list');

    ta.parentNode.insertBefore(wrap, ta);
    wrap.appendChild(toolbar);
    wrap.appendChild(ed);
    wrap.appendChild(refs);
    document.body.appendChild(dd);
    ta.style.display = 'none';
    ta.setAttribute('data-re-mounted', '1');

    var st = {
      ta: ta, ed: ed, dd: dd, refs: refs,
      items: [], allItems: [], active: 0,
      insertOffset: -1,
      composing: false, debTimer: null, syncTimer: null, last: null
    };

    ed.appendChild(deserialize(ta.value || ''));

    var lastCtrlDown = 0;   // 双击 Ctrl 判定（记录上次按下时刻 ms）
    function commit() {
      var v = serialize(ed);
      var setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set;
      setter.call(ta, v);
      ta.dispatchEvent(new Event('input', { bubbles: true }));
      st.last = v;
      updateRefs(v);
    }
    function updateRefs(v) {
      var out = '', m, n = 0;
      FIELD_RE.lastIndex = 0;
      while ((m = FIELD_RE.exec(v)) !== null) {
        n++;
        out += '<span class="re-ref" title="字段 #' + esc(m[1]) + '">#' + esc(m[1]) + ' ' + esc(m[2]) + '</span>';
      }
      refs.innerHTML = n ? '已引用 ' + n + ' 个字段：' + out : '尚未引用字段（按 Ctrl 选择）';
    }

    /* -------- 面板渲染 -------- */
    function renderList(list) {
      st.items = list || [];
      st.active = 0;
      if (!st.items.length) {
        listEl.innerHTML = '<div class="re-dd-empty">无匹配字段</div>';
        dd.hidden = false;
        return;
      }
      var h = '';
      for (var i = 0; i < st.items.length; i++) {
        var f = st.items[i];
        var type = (f.type_text || '').toLowerCase();
        h += '<button type="button" class="re-item' + (i === 0 ? ' is-active' : '') + '" data-i="' + i + '">' +
          '<span class="re-item-type" data-type="' + esc(type) + '">' + esc((f.type_text || '?').slice(0, 4)) + '</span>' +
          '<span class="re-item-main">' +
            '<span class="re-item-name">' + esc(f.name) + '</span>' +
            '<span class="re-item-path">' + esc(f.parse_path) + '</span>' +
          '</span>' +
          '<span class="re-item-id">#' + f.id + '</span>' +
          '</button>';
      }
      listEl.innerHTML = h;
      listEl.scrollTop = 0;
      dd.hidden = false;
    }
    function ddTip(msg, cls) {
      listEl.innerHTML = '<div class="re-dd-tip' + (cls ? ' is-error' : '') + '">' + esc(msg) + '</div>';
      dd.hidden = false;
    }
    function markActive() {
      var els = listEl.querySelectorAll('.re-item');
      for (var i = 0; i < els.length; i++) {
        els[i].classList.toggle('is-active', i === st.active);
      }
      var cur = els[st.active];
      if (cur && cur.scrollIntoView) { try { cur.scrollIntoView({ block: 'nearest' }); } catch (e) {} }
    }

    /* 测光标真实视口位置：contenteditable collapsed range 几何为 0，
       临时插入零宽 span 才能拿到像素坐标 */
    function getCaretRect() {
      var sel = window.getSelection();
      if (!sel || !sel.rangeCount) return null;
      var range = sel.getRangeAt(0);
      if (!range.collapsed) return range.getBoundingClientRect();
      var span = document.createElement('span');
      span.textContent = '\u200B';
      range.insertNode(span);
      var rect = span.getBoundingClientRect();
      span.parentNode.removeChild(span);
      return rect;
    }
    function ddPosition() {
      var left, top;
      var rect = getCaretRect();
      if (rect && (rect.left || rect.top)) {
        left = rect.left;
        top = rect.bottom + 6;
      } else {
        var edRect = ed.getBoundingClientRect();
        left = edRect.left;
        top = edRect.bottom + 6;
      }
      left = Math.min(Math.max(left, 8), Math.max(8, window.innerWidth - 340));
      top = Math.min(Math.max(top, 8), Math.max(8, window.innerHeight - 360));
      dd.style.left = left + 'px';
      dd.style.top = top + 'px';
    }

    /* 打开面板：唯一会请求字段接口的地方（有且仅有 Ctrl 键触发） */
    function openDD() {
      ddPosition();
      dd.hidden = false;
      if (searchEl) searchEl.value = '';
      ddTip('加载字段中…');
      fetchAllFields(function (list, err) {
        if (dd.hidden) return;               // 期间已被关闭则不渲染
        if (err) { ddTip(err, 1); return; }
        st.allItems = list;
        renderList(list);
        if (searchEl) searchEl.focus();      // 聚焦搜索框，用户直接输入过滤
      });
    }
    function closeDD() {
      st.items = []; st.allItems = [];
      clearTimeout(st.debTimer);
      dd.hidden = true;
    }

    /* re-field 是 contenteditable=false 的原子，插入点不能落在它内部；
       若 Range 起点落在某个 re-field 内部，把插入点移到该 field 之后（避免字段嵌套）。 */
    function clampToValid(range) {
      if (!range) return range;
      var sc = range.startContainer;
      var host = (sc && sc.nodeType === 3) ? sc.parentNode : sc;
      if (host && host.nodeType === 1 && host.classList && host.classList.contains('re-field')) {
        var r = document.createRange();
        r.setStartAfter(host);
        r.collapse(true);
        return r;
      }
      return range;
    }

    /* -------- 插入字段：插到按下 Ctrl 时的光标位置，紧贴字符时自动补空格分隔 -------- */
    function insertField(f) {
      var offset = (st.insertOffset != null && st.insertOffset >= 0) ? st.insertOffset : caretOffsetOf(ed);
      var nbr = getNeighbor(ed, offset);
      var needLeft  = (nbr.left  === 'char' || nbr.left  === 'field');
      var needRight = (nbr.right === 'char' || nbr.right === 'field');
      try {
        var insertAt = offset;
        if (needLeft) {
          var rL = clampToValid(rangeForOffsets(ed, insertAt, insertAt));
          rL.insertNode(document.createTextNode(' '));
          insertAt++;
        }
        var r = clampToValid(rangeForOffsets(ed, insertAt, insertAt));
        var span = makeField(f.id, f.parse_path || f.name, f.name);
        r.insertNode(span);
        var sel = window.getSelection();
        var caretRange = document.createRange();
        if (needRight) {
          var rSpace = document.createRange();
          rSpace.setStartAfter(span);
          rSpace.collapse(true);
          rSpace.insertNode(document.createTextNode(' '));
          caretRange.setStartAfter(span.nextSibling);
        } else {
          caretRange.setStartAfter(span);
        }
        caretRange.collapse(true);
        sel.removeAllRanges();
        sel.addRange(caretRange);
      } catch (e) {}
      closeDD();
      commit();
      ed.focus();
    }
    function insertText(str) {
      var sel = window.getSelection();
      if (!sel || !sel.rangeCount) return;
      var range = sel.getRangeAt(0);
      range.deleteContents();
      var node = document.createTextNode(str);
      range.insertNode(node);
      range.setStartAfter(node);
      range.collapse(true);
      sel.removeAllRanges();
      sel.addRange(range);
      commit();
    }

    /* -------- 事件 -------- */
    function onInput() {
      // 输入/编辑一律不触发字段接口、不筛选 —— 有且仅有 Ctrl 键
      commit();
    }
    function onKeyDown(e) {
      /* 只有「双击 Ctrl」才唤起字段列表。
         单击 Ctrl（配合 Ctrl+A/C/V 等常用组合）一律放行、不拦截、不触发。 */
      if (e.key === 'Control') {
        if (!e.repeat) {
          var now = Date.now();
          if (lastCtrlDown && now - lastCtrlDown < 300) {
            lastCtrlDown = 0;                        // 第二次（双击）→ 唤起
            e.preventDefault();
            st.insertOffset = caretOffsetOf(ed);
            openDD();
          } else {
            lastCtrlDown = now;                      // 第一次（单击）→ 仅记录，放行默认
          }
        }
        return;
      }
      if (!dd.hidden) {
        if (e.key === 'ArrowDown') { e.preventDefault(); st.active = (st.active + 1) % (st.items.length || 1); markActive(); return; }
        if (e.key === 'ArrowUp') { e.preventDefault(); st.active = (st.active - 1 + (st.items.length || 1)) % (st.items.length || 1); markActive(); return; }
        if (e.key === 'Enter' || e.key === 'Tab') { e.preventDefault(); var f = st.items[st.active]; if (f) insertField(f); return; }
        if (e.key === 'Escape') { closeDD(); e.preventDefault(); return; }
      }
      if (e.key === 'Tab') {
        e.preventDefault();
        insertText('    ');
        return;
      }
      if (e.key === 'Enter') {
        e.preventDefault();
        insertText('\n');
        return;
      }
    }

    // 搜索框：本地过滤（不发请求）
    if (searchEl) {
      searchEl.addEventListener('input', function () {
        if (!st.allItems.length) return;
        var kw = this.value.trim().toLowerCase();
        if (!kw) { renderList(st.allItems); return; }
        var filtered = [];
        for (var i = 0; i < st.allItems.length; i++) {
          var f = st.allItems[i];
          var hay = ((f.name || '') + ' ' + (f.parse_path || '')).toLowerCase();
          if (hay.indexOf(kw) >= 0) filtered.push(f);
        }
        renderList(filtered);
      });
      searchEl.addEventListener('keydown', function (e) {
        if (e.key === 'ArrowDown') { e.preventDefault(); st.active = (st.active + 1) % (st.items.length || 1); markActive(); }
        else if (e.key === 'ArrowUp') { e.preventDefault(); st.active = (st.active - 1 + (st.items.length || 1)) % (st.items.length || 1); markActive(); }
        else if (e.key === 'Enter' || e.key === 'Tab') { e.preventDefault(); var f = st.items[st.active]; if (f) insertField(f); }
        else if (e.key === 'Escape') { closeDD(); ed.focus(); }
      });
    }

    function onDocDown(e) {
      if (dd.hidden) return;
      if (dd.contains(e.target) || ed.contains(e.target)) return;
      closeDD();
    }

    ed.addEventListener('input', onInput);
    ed.addEventListener('keydown', onKeyDown);
    ed.addEventListener('compositionstart', function () { st.composing = true; });
    ed.addEventListener('compositionend', function () { st.composing = false; commit(); });
    dd.addEventListener('mousedown', function (e) {
      // 只有点按钮才阻止默认，保证点击搜索框能正常获得焦点输入
      if (e.target.closest && e.target.closest('.re-item')) e.preventDefault();
    });
    dd.addEventListener('click', function (e) {
      var btn = e.target.closest ? e.target.closest('.re-item') : null;
      if (btn) insertField(st.items[Number(btn.getAttribute('data-i'))]);
    });
    st.onDocDown = onDocDown;
    document.addEventListener('mousedown', st.onDocDown, true);

    updateRefs(ta.value || '');

    var handle = { st: st, ta: ta };
    st.syncTimer = setInterval(function () {
      if (!document.contains(ta)) { unmount(handle); return; }
      if (ta.value !== st.last) {
        ed.innerHTML = '';
        ed.appendChild(deserialize(ta.value || ''));
        st.last = ta.value;
        updateRefs(ta.value || '');
      }
    }, 300);

    mounted.push(handle);
    return handle;
  }

  function unmount(h) {
    if (!h || !h.st) return;
    var st = h.st;
    clearInterval(st.syncTimer); clearTimeout(st.debTimer);
    if (st.onDocDown) document.removeEventListener('mousedown', st.onDocDown, true);
    if (st.dd && st.dd.parentNode) st.dd.parentNode.removeChild(st.dd);
    var wrap = (st.ed && st.ed.parentNode) || st.wrap;
    if (wrap && wrap.parentNode) wrap.parentNode.removeChild(wrap);
    if (st.ta) {
      st.ta.removeAttribute('data-re-mounted');
      st.ta.style.display = '';
    }
    for (var i = 0; i < mounted.length; i++) if (mounted[i] === h) { mounted.splice(i, 1); break; }
  }

  /* ---------------- 自动挂载 ---------------- */
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
