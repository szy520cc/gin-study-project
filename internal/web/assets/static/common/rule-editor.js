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

  /* 字段缓存：按 projectId 缓存字段列表，用于反序列化时把字段 ID 映射成名称显示。
     编辑页 initApi 已把 project_id 写到 RuleEditorContext.projectId，所以能命中。 */
  if (!window.RuleEditorContext) window.RuleEditorContext = {};
  if (!window.RuleEditorContext.fields) window.RuleEditorContext.fields = {};
  function cachedField(id) {
    var ctx = window.RuleEditorContext;
    return (ctx && ctx.fields && ctx.fields[id]) || null;
  }
  function cachedFieldName(id, fallback) {
    var f = cachedField(id);
    return (f && f.name) ? f.name : fallback;
  }
  function ensureFields(cb) {
    var pid = projectId();
    var ctx = window.RuleEditorContext;
    if (!pid) { if (cb) cb('未选择项目'); return; }
    if (ctx._fieldProjectId && ctx._fieldProjectId !== pid) { ctx.fields = {}; ctx.fieldList = []; }
    ctx._fieldProjectId = pid;
    if (ctx.fields && Object.keys(ctx.fields).length > 0) { if (cb) cb(''); return; }
    if (ctx._fieldLoading) { ctx._fieldLoadingCbs = ctx._fieldLoadingCbs || []; ctx._fieldLoadingCbs.push(cb); return; }
    ctx._fieldLoading = true;
    fetchAllFields(function (list, err) {
      ctx._fieldLoading = false;
      if (!err) {
        ctx.fields = {};
        ctx.fieldList = list || [];
        for (var i = 0; i < list.length; i++) { var f = list[i]; if (f && f.id) ctx.fields[f.id] = f; }
      }
      if (cb) cb(err || '');
      var cbs = ctx._fieldLoadingCbs || [];
      ctx._fieldLoadingCbs = [];
      for (var j = 0; j < cbs.length; j++) if (cbs[j]) cbs[j](err || '');
    });
  }
  function enrichFieldSpans(root) {
    var ctx = window.RuleEditorContext;
    if (!ctx || !ctx.fields) return;
    var spans = root.querySelectorAll('.re-field');
    for (var i = 0; i < spans.length; i++) {
      var span = spans[i];
      var id = span.getAttribute('data-id');
      var f = ctx.fields[id];
      if (f && f.name) {
        var path = span.getAttribute('data-path') || f.parse_path || '';
        if (f.name !== path && span.textContent !== f.name) {
          span.setAttribute('data-name', f.name);
          span.textContent = f.name;
          span.setAttribute('title', '字段 #' + id + ' · ' + f.name);
        }
      }
    }
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
    if (!name) name = cachedFieldName(id, '');
    if (!name) name = path;
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
    toolbar.innerHTML = '<span class="re-hint">双击 <b>Ctrl</b> 获取字段 · 搜索过滤 · Tab 缩进 · Ctrl+Z 撤回</span>' +
      '<button type="button" class="re-btn re-undo" title="撤回 (Ctrl+Z)">↶ 撤回</button>' +
      '<button type="button" class="re-btn re-redo" title="重做 (Ctrl+Shift+Z / Ctrl+Y)">↷ 重做</button>';
    var ed = document.createElement('div');
    ed.className = 're-editor';
    /* 必须用 "true" 而不是 "plaintext-only"：
       amis 的 Table 快速编辑在 document.body 上挂了全局 keydown，放行条件是
       target.tagName ∈ {INPUT,TEXTAREA} 或 target.contentEditable === "true"；
       "plaintext-only" 两者都不满足 → 上下左右方向键被 preventDefault，光标无法移动。
       富文本粘贴改由 paste 监听强制转纯文本，内容仍是纯文本。 */
    ed.setAttribute('contenteditable', 'true');
    ed.setAttribute('spellcheck', 'false');
    ed.setAttribute('data-placeholder', '在此输入 Starlark 规则…');
    var refs = document.createElement('div');
    refs.className = 're-refs';
    /* 行号列：与编辑区左右并排（不叠加在内容上，contenteditable 单层渲染不产生漂移） */
    var gutterEl = document.createElement('div');
    gutterEl.className = 're-gutter';
    gutterEl.setAttribute('aria-hidden', 'true');
    var gutterInner = document.createElement('div');
    gutterInner.className = 're-gutter-inner';
    gutterEl.appendChild(gutterInner);
    var mainEl = document.createElement('div');
    mainEl.className = 're-main';
    mainEl.appendChild(gutterEl);
    mainEl.appendChild(ed);
    /* 缩进参考虚线覆盖层：绝对定位盖在编辑区上，pointer-events:none，纯装饰不影响编辑 */
    var guidesEl = document.createElement('div');
    guidesEl.className = 're-guides';
    guidesEl.setAttribute('aria-hidden', 'true');
    var guidesInner = document.createElement('div');
    guidesInner.className = 're-guides-inner';
    guidesEl.appendChild(guidesInner);
    mainEl.appendChild(guidesEl);

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
    wrap.appendChild(mainEl);
    wrap.appendChild(refs);
    document.body.appendChild(dd);
    ta.style.display = 'none';
    ta.setAttribute('data-re-mounted', '1');

    var st = {
      ta: ta, ed: ed, gutterEl: gutterEl, gutterInner: gutterInner, dd: dd, refs: refs,
      items: [], allItems: [], active: 0,
      insertOffset: -1,
      syncTimer: null, last: null,
      undo: [], redo: [], histAt: 0, histCaret: -1, undoBtn: null, redoBtn: null
    };

    ed.appendChild(deserialize(ta.value || ''));
    enrichFieldSpans(ed);
    ensureFields(function () { enrichFieldSpans(ed); });

    var lastCtrlDown = 0;   // 双击 Ctrl 判定（记录上次按下时刻 ms）
    function commit() {
      var v = serialize(ed);
      var setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set;
      setter.call(ta, v);
      ta.dispatchEvent(new Event('input', { bubbles: true }));
      st.last = v;
      updateRefs(v);
      scheduleGutter();
    }
    function updateRefs(v) {
      var out = '', m, n = 0;
      FIELD_RE.lastIndex = 0;
      while ((m = FIELD_RE.exec(v)) !== null) {
        n++;
        out += '<span class="re-ref" title="字段 #' + esc(m[1]) + ' · ' + esc(m[2]) + '">#' + esc(m[1]) + ' ' + esc(m[2]) + '</span>';
      }
      refs.innerHTML = n ? '已引用 ' + n + ' 个字段：' + out : '尚未引用字段（按 Ctrl 选择）';
    }

    /* -------- 撤回 / 重做 --------
       编辑区是 contenteditable：浏览器原生的 undo 栈在我们「整段重写 DOM」
       （如反序列化回显、插入字段胶囊）时会被冲掉，只能自建历史。
       snapshot 存「可序列化文本 + 光标偏移(DOM 字符单位)」，回放时反序列化
       重建 DOM 再把光标放回去，与 JSON 编辑器的做法一致。 */
    function snapshot() {
      return { text: serialize(ed), caret: caretOffsetOf(ed) };
    }
    function pushHistory(soft) {
      var now = Date.now();
      var caret = caretOffsetOf(ed);
      /* 连续输入（打字/删除）：700ms 内且光标接得上就合并成一步，
         否则敲一个字就占一格撤回，很难用。 */
      if (soft && (now - st.histAt < 700) && caret === st.histCaret) {
        st.histAt = now;
        return;
      }
      st.undo.push(snapshot());
      if (st.undo.length > 300) st.undo.shift();
      st.redo.length = 0;
      st.histAt = now;
      st.histCaret = caret;
      updateHistButtons();
    }
    function applySnapshot(s) {
      ed.innerHTML = '';
      ed.appendChild(deserialize(s.text || ''));
      var max = totalLen(ed);
      var caret = (s.caret == null) ? 0 : Math.min(s.caret, max);
      try {
        var r = rangeForOffsets(ed, caret, caret);
        var sel = window.getSelection();
        sel.removeAllRanges();
        sel.addRange(r);
      } catch (e) {}
      st.histAt = 0;        // 撤回/重做之后不要并进后续输入
      st.histCaret = -1;
      commit();
      updateHistButtons();
    }
    function doUndo() {
      if (!st.undo.length) return;
      st.redo.push(snapshot());
      applySnapshot(st.undo.pop());
    }
    function doRedo() {
      if (!st.redo.length) return;
      st.undo.push(snapshot());
      applySnapshot(st.redo.pop());
    }
    function updateHistButtons() {
      if (st.undoBtn) st.undoBtn.disabled = !st.undo.length;
      if (st.redoBtn) st.redoBtn.disabled = !st.redo.length;
    }

    /* -------- 行号（contenteditable 版：按真实视觉行定位，不叠加、不漂移） -------- */
    /* 内容 offset 处的可视 y（相对编辑区顶部，含滚动）。
       注意：绝不能往编辑区里插临时节点来量位置——回车后光标常停在新行行首，
       插入/移除探针会被 Chrome 的实时选区一起带走（表现为「光标乱跑」）。
       collapsed range 的几何在 Chrome 可直接用；退化时按行高估算。 */
    function contentYAt(ed2, offset) {
      var edRect = ed2.getBoundingClientRect();
      var padTop = parseFloat(getComputedStyle(ed2).paddingTop) || 0;
      var lh = parseFloat(getComputedStyle(ed2).lineHeight) || 21;
      try {
        var rect = rangeForOffsets(ed2, offset, offset).getBoundingClientRect();
        if (rect && (rect.top || rect.height)) {
          return rect.top - edRect.top - padTop + ed2.scrollTop;
        }
      } catch (e) { /* 落到下面的估算 */ }
      var before = (ed2.textContent || '').slice(0, offset);
      return (before.split('\n').length - 1) * lh;
    }
    function updateGutter() {
      if (!gutterInner || !ed) return;
      var txt = ed.textContent || '';
      var starts = [0];
      for (var i = 0; i < txt.length; i++) {
        if (txt.charAt(i) === '\n') starts.push(i + 1);
      }
      var padTop = parseFloat(getComputedStyle(ed).paddingTop) || 0;
      var lh = parseFloat(getComputedStyle(ed).lineHeight) || 21;
      /* 先算每行行首的 y（含滚动），行号与缩进虚线共用 */
      var ys = [], k, y;
      for (k = 0; k < starts.length; k++) ys[k] = contentYAt(ed, starts[k]);
      var html = '', maxY = 0, ghtml = '';
      for (k = 0; k < starts.length; k++) {
        y = ys[k];
        if (y === null) continue;
        if (y > maxY) maxY = y;
        var top = (padTop + y).toFixed(1);
        html += '<span class="re-gutter-num" style="top:' + top + 'px">' + (k + 1) + '</span>';
        /* 缩进参考虚线：行首空白每 2 个空格（一级缩进）画一条竖虚线，与 JSON 编辑器一致 */
        var lineEnd = (k + 1 < starts.length) ? starts[k + 1] - 1 : txt.length;
        var lead = (/^[ \t]*/.exec(txt.slice(starts[k], lineEnd)) || [''])[0];
        var depth = Math.floor(lead.replace(/\t/g, '  ').length / 2);
        if (depth > 0) {
          var y2 = (k + 1 < ys.length && ys[k + 1] != null) ? ys[k + 1] : (y + lh);
          /* 每段只画「本行」的高度，并留出 5px 行距：不留缝时相邻行的虚线会首尾
             相接，连成一条上下贯通、看起来「超出首行/尾行」的长线（JSON 编辑器画在
             行内盒上天然带行距，所以没这问题）。同时对异常大的 y2-y 设上限，
             避免量位失真时画出一根长线。 */
          var seg = Math.max(lh, y2 - y);
          if (seg > lh * 8) seg = lh;
          var hh = Math.max(4, seg - 5).toFixed(1);
          for (var L = 1; L <= depth; L++) {
            ghtml += '<span class="re-guide" style="left:calc(14px + ' + (2 * (L - 1)) + 'ch);top:' + top + 'px;height:' + hh + 'px"></span>';
          }
        }
      }
      gutterInner.innerHTML = html;
      gutterInner.style.height = (maxY + 60) + 'px';
      gutterSyncScroll();
      if (guidesInner) guidesInner.innerHTML = ghtml;
      guidesSyncScroll();
    }
    function gutterSyncScroll() {
      if (gutterInner) gutterInner.style.transform = 'translateY(' + (-ed.scrollTop) + 'px)';
    }
    function guidesSyncScroll() {
      if (guidesInner) guidesInner.style.transform = 'translateY(' + (-ed.scrollTop) + 'px)';
    }
    var gutterRaf = 0;
    function scheduleGutter() {
      if (gutterRaf) return;
      gutterRaf = requestAnimationFrame(function () {
        gutterRaf = 0;
        updateGutter();
      });
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

    /* 测光标真实视口位置：直接取 range 几何，不往编辑区插零宽探针
       （插/删节点会扰动 Chrome 的实时选区，也是「回车后光标乱跑」的来源之一）。 */
    function getCaretRect() {
      var sel = window.getSelection();
      if (!sel || !sel.rangeCount) return null;
      var range = sel.getRangeAt(0);
      var rect = range.getBoundingClientRect();
      if (rect && (rect.left || rect.top)) return rect;
      var rects = range.getClientRects();   // 退化时取选区首个矩形
      return (rects && rects.length) ? rects[0] : rect;
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

    /* 打开面板：通过 ensureFields 统一加载并缓存字段（保持与反序列化名称回显同一份缓存） */
    function openDD() {
      ddPosition();
      dd.hidden = false;
      if (searchEl) searchEl.value = '';
      ddTip('加载字段中…');
      ensureFields(function (err) {
        if (dd.hidden) return;               // 期间已被关闭则不渲染
        if (err) { ddTip(err, 1); return; }
        var ctx = window.RuleEditorContext;
        var list = (ctx && ctx.fieldList) ? ctx.fieldList : [];
        st.allItems = list;
        renderList(list);
        if (searchEl) searchEl.focus();      // 聚焦搜索框，用户直接输入过滤
      });
    }
    function closeDD() {
      st.items = []; st.allItems = [];
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
      pushHistory(false);
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
      pushHistory(false);
      var sel = window.getSelection();
      if (!sel || !sel.rangeCount) return;
      var range = sel.getRangeAt(0);
      /* 光标可能落在字段胶囊内部（胶囊是普通 inline span）。胶囊是原子，
         往里插文本会把胶囊内容截断、光标也随之跑偏；先把落点挪到胶囊外。 */
      var chip = null, n = range.startContainer;
      while (n && n !== ed) {
        if (n.nodeType === 1 && n.classList && n.classList.contains('re-field')) { chip = n; break; }
        n = n.parentNode;
      }
      if (chip) {
        var nr = document.createRange();
        nr.setStartAfter(chip);
        nr.collapse(true);
        range = nr;
      }
      range.deleteContents();
      var node = document.createTextNode(str);
      range.insertNode(node);
      range.setStartAfter(node);
      range.collapse(true);
      sel.removeAllRanges();
      sel.addRange(range);
      commit();
    }

    /* 回车：沿用当前行的缩进（与 JSON 编辑器的 doEnter 一致）。
       只插裸 \n 的话，新行没有任何行首空白 → 缩进虚线整段消失，
       看起来就是「回车后虚线没画对」。 */
    function insertNewline() {
      var before = caretBeforeText(ed);
      var lineStart = before.lastIndexOf('\n') + 1;
      var indent = (/^[ \t]*/.exec(before.slice(lineStart)) || [''])[0];
      insertText('\n' + indent);
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
      /* Ctrl+Z 撤回 / Ctrl+Shift+Z、Ctrl+Y 重做（与 JSON 编辑器一致）。
         放 Control 单键判断之后：组合键的 e.key 是 'z' 不是 'Control'，不会被上面的分支吃掉。 */
      if ((e.ctrlKey || e.metaKey) && !e.altKey) {
        var hk = String(e.key || '').toLowerCase();
        if (hk === 'z') { e.preventDefault(); if (e.shiftKey) doRedo(); else doUndo(); return; }
        if (hk === 'y') { e.preventDefault(); doRedo(); return; }
      }
      if (!dd.hidden) {
        if (e.key === 'ArrowDown') { e.preventDefault(); st.active = (st.active + 1) % (st.items.length || 1); markActive(); return; }
        if (e.key === 'ArrowUp') { e.preventDefault(); st.active = (st.active - 1 + (st.items.length || 1)) % (st.items.length || 1); markActive(); return; }
        if (e.key === 'Enter' || e.key === 'Tab') { e.preventDefault(); var f = st.items[st.active]; if (f) insertField(f); return; }
        if (e.key === 'Escape') { closeDD(); e.preventDefault(); return; }
      }
      if (e.key === 'Tab') {
        e.preventDefault();
        insertText('  ');
        return;
      }
      if (e.key === 'Enter') {
        e.preventDefault();
        insertNewline();
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
    /* 原生 undo 栈会被「整段重写 DOM」冲掉，用 beforeinput 在浏览器改 DOM 前
       快照当前状态（与 JSON 编辑器一致）。insertText/insertField 这类程序化改动
       不触发 beforeinput，故在那些函数里显式 pushHistory。 */
    ed.addEventListener('beforeinput', function (e) {
      if (ed.getAttribute('contenteditable') === 'false') return;
      pushHistory(/^(insert|delete)/i.test(String(e.inputType || '')));
    });
    /* 工具栏撤回/重做按钮 */
    st.undoBtn = toolbar.querySelector('.re-undo');
    st.redoBtn = toolbar.querySelector('.re-redo');
    if (st.undoBtn) st.undoBtn.addEventListener('click', function () { doUndo(); });
    if (st.redoBtn) st.redoBtn.addEventListener('click', function () { doRedo(); });
    updateHistButtons();
    ed.addEventListener('scroll', function () { gutterSyncScroll(); guidesSyncScroll(); });
    ed.addEventListener('compositionend', function () { commit(); });
    /* 编辑区为 contenteditable="true"（为绕开 amis 的方向键拦截），
       粘贴必须强制转纯文本，否则网页富文本会带样式进 DOM。 */
    ed.addEventListener('paste', function (e) {
      e.preventDefault();
      var cd = e.clipboardData || window.clipboardData;
      var text = cd ? cd.getData('text') : '';
      if (text == null) return;
      insertText(String(text).replace(/\r\n?/g, '\n'));
    });
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
    scheduleGutter();

    /* -------- 跟随 amis 弹层主题色（amis 6 用 JS-in-JS 注入，不暴露 CSS 变量） --------
       读弹层里 .cxd-Button--primary 按钮的背景作为强调色，动态设到 wrap 的 --re-accent。
       找不到时沿用 :root 的 fallback 颜色。 */
    function rgbWithAlpha(rgb, a) {
      var m = /rgba?\((\d+)\s*,\s*(\d+)\s*,\s*(\d+)/.exec(rgb);
      return m ? ('rgba(' + m[1] + ',' + m[2] + ',' + m[3] + ',' + a + ')') : rgb;
    }
    function pickAmisAccent() {
      var btn = document.querySelector('.cxd-Button--primary, .cxd-Button.cxd-Button--primary');
      if (btn) {
        var bg = getComputedStyle(btn).backgroundColor;
        if (bg && bg.indexOf('rgba(0, 0, 0, 0)') < 0 && bg !== 'transparent') return bg;
      }
      return null;
    }
    function applyAmisAccent() {
      var c = pickAmisAccent();
      if (!c) return;
      wrap.style.setProperty('--re-accent', c);
      wrap.style.setProperty('--re-accent-soft', rgbWithAlpha(c, 0.12));
      wrap.style.setProperty('--re-accent-ring', rgbWithAlpha(c, 0.22));
    }
    var amisAccentTimer = 0;
    function scheduleAmisAccent() {
      clearTimeout(amisAccentTimer);
      amisAccentTimer = setTimeout(applyAmisAccent, 120);
    }
    applyAmisAccent();
    setTimeout(applyAmisAccent, 500);
    setTimeout(applyAmisAccent, 1500);
    if (window.MutationObserver) {
      try {
        var moA = new MutationObserver(scheduleAmisAccent);
        moA.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] });
        moA.observe(document.body, { childList: true, subtree: true, attributes: true, attributeFilter: ['class','style'] });
      } catch (e) {}
    }

    var handle = { st: st, ta: ta };
    st.syncTimer = setInterval(function () {
      if (!document.contains(ta)) { unmount(handle); return; }
      if (ta.value !== st.last) {
        ed.innerHTML = '';
        ed.appendChild(deserialize(ta.value || ''));
        st.last = ta.value;
        updateRefs(ta.value || '');
        scheduleGutter();
        ensureFields(function () { enrichFieldSpans(ed); });
        /* 外部换了一篇内容（回显 / 重置 / 切换配置），旧历史已无意义 */
        st.undo.length = 0;
        st.redo.length = 0;
        st.histAt = 0;
        st.histCaret = -1;
        updateHistButtons();
      }
    }, 300);

    mounted.push(handle);
    return handle;
  }

  function unmount(h) {
    if (!h || !h.st) return;
    var st = h.st;
    clearInterval(st.syncTimer);
    if (st.onDocDown) document.removeEventListener('mousedown', st.onDocDown, true);
    if (st.dd && st.dd.parentNode) st.dd.parentNode.removeChild(st.dd);
    var wrap = st.ed && st.ed.parentNode;
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
    /* 只增强「可编辑」的规则框：disabled 的 textarea 是只读展示（如试运行弹窗里的
       「本次运行规则」），若也挂上增强器会变成 contenteditable="true" 的可编辑区，
       与只读语义冲突。 */
    var list = scope.querySelectorAll('textarea[name="rule"]:not([data-re-mounted]):not([disabled]), textarea.rule-editor:not([data-re-mounted]):not([disabled])');
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
