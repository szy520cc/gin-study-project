/* =========================================================
   Gin Study 后台 · 公共脚本（index.html / login.html 共用）
   token、鉴权 fetcher、hash 路由、页面渲染、登出
   菜单配置已抽离到独立文件 app_menu.js（window.APP_MENU）
   ========================================================= */
'use strict';
(function () {
  var TOKEN_KEY = 'admin_token';
  var USERNAME_KEY = 'admin_username';

  // 菜单配置独立在 app_menu.js（window.APP_MENU），改动菜单只动那个文件；
  // 登录页未引入 app_menu.js 时为空数组，不影响登录流程。
  var MENU = window.APP_MENU || [];

  var PAGES_BASE = '/admin/pages/';
  var LOGIN_URL = '/admin/login';
  var mount = null;

  // =========================================================
  // 鉴权 fetcher：amis.embed 的第 4 个参数 env 里传入。
  // amis 6.x 契约：
  //  - 入参是 amis 构建好的单个配置对象 {url, method, data, headers, ...}
  //  - 返回值必须是 axios 风格 {data, status, headers}，
  //    data 为后端响应体；amis 会再用 responseAdaptor 读取
  //    data.status===0 → ok、data.msg/message → 提示、data.data → 业务数据。
  // 所以这里把后端 {code,message,data} 规整为带 status 的响应体后原样返回。
  function authFetcher(api) {
    if (typeof api === 'string') api = { url: api, method: 'GET' };
    if (!api || typeof api !== 'object') api = { url: '', method: 'GET' };

    var url = api.url || '';
    var method = (api.method || 'GET').toUpperCase();
    var headers = new Headers(api.headers || {});
    headers.set('Accept', 'application/json');

    var token = localStorage.getItem(TOKEN_KEY);
    if (token) headers.set('Authorization', 'Bearer ' + token);

    var body;
    var data = api.data;
    if (data instanceof FormData) {
      body = data; // multipart，交给浏览器设置 boundary
    } else if (data !== undefined && data !== null && data !== '') {
      if (method === 'GET' || method === 'HEAD') {
        // GET 参数拼到 url
        var sp = new URLSearchParams();
        Object.keys(data).forEach(function (k) {
          var v = data[k];
          if (v === undefined || v === null || k === '__undefined') return;
          if (typeof v === 'object') v = JSON.stringify(v);
          sp.append(k, v);
        });
        var qs = sp.toString();
        if (qs) url += (url.indexOf('?') >= 0 ? '&' : '?') + qs;
      } else {
        headers.set('Content-Type', 'application/json');
        // 已是字符串（amis 预序列化）则直接用，否则序列化对象
        body = typeof data === 'string' ? data : JSON.stringify(data);
      }
    }

    return fetch(url, { method: method, headers: headers, body: body }).then(function (res) {
      return res.json().catch(function () { return {}; }).then(function (raw) {
        raw = raw || {};

        // 未登录 / token 失效：HTTP 401
        if (res.status === 401) {
          logout(true);
        }

        // 统一业务码：优先后端 code，其次已带 status，最后按 HTTP 兜底
        var code = raw.code;
        if (code === undefined) code = raw.status;
        if (code === undefined) code = res.ok ? 0 : res.status;

        // 给响应体补上 amis responseAdaptor 需要的 status / ok / msg
        var d = raw.data;
        // 后端列表形如 {list,total} → amis 期望 {items,total}
        if (d && Array.isArray(d.list)) {
          d = { items: d.list, total: d.total, count: d.total };
        }
        // 写操作（增/改/删）成功时给出明确的全局提示，避免 amis 默认提示一闪而过
        if (code === 0 && (method === 'POST' || method === 'PUT' || method === 'DELETE')) {
          appToast('操作成功');
        }

        var body2 = {
          status: code,
          ok: code === 0,
          msg: code === 0 ? '' : (raw.message || raw.msg || '请求失败（HTTP ' + res.status + '）'),
          data: d
        };

        // 返回 axios 风格响应，amis responseAdaptor 会自动解出 {ok,status,msg,data}
        return { status: res.status, headers: {}, data: body2 };
      });
    }).catch(function (err) {
      return {
        status: 0,
        headers: {},
        data: { status: -1, ok: false, msg: '网络错误：' + err.message, data: null }
      };
    });
  }

  // ---------- 全局操作提示（自绘 DOM，时长/关闭可控，不依赖 amis toast） ----------
  function appToast(msg, type) {
    type = type || 'success';
    var isErr = type === 'error';
    var el = document.createElement('div');
    el.className = 'app-toast app-toast-' + type;
    el.innerHTML =
      '<i class="fa-solid ' + (isErr ? 'fa-triangle-exclamation' : 'fa-circle-check') + '"></i>' +
      '<span></span>';
    el.querySelector('span').textContent = msg;
    (document.body || document.documentElement).appendChild(el);
    // 触发过渡动画后进入可视态，5 秒后淡出移除
    setTimeout(function () { el.classList.add('show'); }, 20);
    setTimeout(function () {
      el.classList.remove('show');
      setTimeout(function () { if (el.parentNode) el.parentNode.removeChild(el); }, 300);
    }, 5000);
  }

  // ---------- 登出 ----------
  function logout(showTip) {
    localStorage.removeItem(TOKEN_KEY);
    localStorage.removeItem(USERNAME_KEY);
    localStorage.removeItem('admin_user_id');
    if (showTip && window.toastr && window.toastr.options) { /* SDK 自带弹层由 amis 处理，这里不重复 */ }
    location.href = LOGIN_URL;
  }

  // =========================================================
  // amis 渲染
  // =========================================================
  var embedApp = null;
  function getEmbed() {
    return new Promise(function (resolve, reject) {
      if (window.__amisEmbed) return resolve(window.__amisEmbed);
      var req = window.amisRequire || (window.amis && window.amis.require);
      if (typeof req !== 'function') return reject(new Error('amis SDK 未加载'));
      req(['amis/embed'], function (m) {
        var e = m && m.embed ? m.embed : m;
        if (typeof e === 'function') { window.__amisEmbed = e; resolve(e); }
        else reject(new Error('amis/embed 加载失败'));
      });
    });
  }

  // amis CRUD 默认 syncLocation=true：筛选/翻页时会改写地址栏 hash，
  // 进而触发应用层 hashchange 路由 → 整个页面被重建，表现为整页刷新。
  // 渲染前递归把页面里所有 CRUD 的 syncLocation 统一关掉，
  // 检索/翻页/增删改的数据刷新全部只走组件内部局部加载（静默）。
  function disableCrudLocationSync(node) {
    if (!node || typeof node !== 'object') return;
    if (Array.isArray(node)) {
      for (var i = 0; i < node.length; i++) disableCrudLocationSync(node[i]);
      return;
    }
    var t = node.type;
    if ((t === 'crud' || t === 'crud2') && node.syncLocation === undefined) node.syncLocation = false;
    for (var k in node) {
      if (Object.prototype.hasOwnProperty.call(node, k) && k !== 'syncLocation') {
        var v = node[k];
        if (v && typeof v === 'object') disableCrudLocationSync(v);
      }
    }
  }

  function renderPage(schema) {
    var el = document.getElementById('pageContent');
    if (!el || !schema) return;
    el.innerHTML = '';
    disableCrudLocationSync(schema); // 全局关闭检索/翻页的地址栏同步，保证纯局部静默加载
    getEmbed().then(function (embed) {
      try {
        embedApp = embed(
          el,
          schema,
          { theme: 'cxd', locale: 'zh-CN' },   // props
          {
            fetcher: authFetcher,              // env：所有请求带 token
            jumpTo: function (to) {             // env：schema 内跳转统一转 hash 路由
              if (!to || typeof to !== 'string') return;
              if (to.indexOf('http://') === 0 || to.indexOf('https://') === 0) { location.href = to; return; }
              var name = to;
              if (name.indexOf('#/pages/') === 0) name = name.slice('#/pages/'.length);
              else if (name.indexOf('/pages/') >= 0) name = name.split('/pages/').pop();
              else if (name.charAt(0) === '/') name = name.replace(/^\/+/, '');
              if (/\.json$/.test(name)) location.hash = '#/pages/' + name;
            }
          }
        );
      } catch (err) {
        el.innerHTML = '<div class="content-hint">页面渲染失败：' + err.message + '</div>';
      }
    }).catch(function (err) {
      el.innerHTML = '<div class="content-hint">' + err.message + '</div>';
    });
  }

  function showLoading() {
    if (mount) mount.innerHTML =
      '<div class="content-hint"><div class="spinner-border spinner-border-sm text-primary"></div> 加载中…</div>';
  }

  // ---------- 加载页面 schema（标签页切换复用） ----------
  var loadSeq = 0;
  function loadSchema(key) {
    showLoading();
    var seq = ++loadSeq; // 快速切换标签时丢弃过期响应，避免旧页面覆盖新页面
    fetch(PAGES_BASE + key).then(function (res) {
      if (!res.ok) throw new Error('页面加载失败 HTTP ' + res.status);
      return res.json();
    }).then(function (schema) {
      if (seq !== loadSeq) return;
      renderPage(schema);
    }).catch(function (err) {
      if (seq === loadSeq && mount) mount.innerHTML = '<div class="content-hint">' + err.message + '</div>';
    });
  }

  // ---------- 路由 / 标签页 / 面包屑 / 树形菜单 ----------

  var HOME_KEY = 'home.json';
  var MAX_TABS = 15;
  var tabs = []; // [{key,title}]，首页固定不可关闭，保证至少存在一个标签
  var activeKey = '';

  // pathOf：从完整 hash（可能带 amis 筛选/分页 query）里取出页面 key（纯文件名）。
  // 例如 '#/pages/field.json?status=1&page=1' → 'field.json'。
  function pathOf(hashStr) {
    var h = hashStr || location.hash;
    var q = h.indexOf('?');
    if (q >= 0) h = h.slice(0, q);
    if (h && h.indexOf('#/pages/') === 0) return h.slice('#/pages/'.length);
    return HOME_KEY;
  }
  function currentKey() { return pathOf(location.hash); }
  function leafTitle(key) {
    var chain = findChain(key);
    if (chain && chain.length) return chain[chain.length - 1].title;
    return key.replace(/\.json$/, '');
  }

  // findChain：返回 key 所在节点到根节点的链（含叶子），支持任意层级
  function findChain(key) {
    function walk(nodes, acc) {
      for (var i = 0; i < nodes.length; i++) {
        var n = nodes[i];
        var next = acc.concat([n]);
        if (n.key === key) return next;
        if (n.children && n.children.length) {
          var r = walk(n.children, next);
          if (r) return r;
        }
      }
      return null;
    }
    return walk(MENU, []);
  }
  // firstLeafKey：父容器点击时跳到该子树下第一个叶子页
  function firstLeafKey(node) {
    if (node.key) return node.key;
    if (node.children && node.children.length) {
      for (var i = 0; i < node.children.length; i++) {
        var k = firstLeafKey(node.children[i]);
        if (k) return k;
      }
    }
    return null;
  }

  // ---------- 树形菜单渲染（可折叠，父节点不可跳转） ----------
  function menuIconHtml(n) {
    var ic = n.icon || 'fa-circle';
    return '<i class="fa-solid ' + ic + '"></i>';
  }
  function treeHtml(nodes) {
    var html = '<ul class="menu-tree">';
    nodes.forEach(function (n) {
      var hasKids = n.children && n.children.length > 0;
      if (hasKids) {
        // 默认收起；点击展开（同一层级只保留一个），激活路径由 setActiveMenu 自动展开
        html += '<li class="menu-li menu-parent-li">' +
          '<a class="menu-item menu-parent" href="javascript:void(0)" title="' + n.title + '">' +
          menuIconHtml(n) + '<span class="menu-text">' + n.title + '</span>' +
          '<i class="fa-solid fa-angle-down menu-arrow"></i></a>' + treeHtml(n.children) + '</li>';
      } else {
        html += '<li class="menu-li">' +
          '<a class="menu-item menu-leaf" data-key="' + n.key + '" href="#/pages/' + n.key + '" title="' + n.title + '">' +
          menuIconHtml(n) + '<span class="menu-text">' + n.title + '</span></a></li>';
      }
    });
    return html + '</ul>';
  }
  function buildMenu() {
    var el = document.getElementById('sideMenu');
    if (!el) return;
    el.innerHTML = treeHtml(MENU);
    // 父节点点击：仅折叠/展开，不跳转；同一层级手风琴式，只展开点击的那个
    el.addEventListener('click', function (e) {
      var p = e.target.closest('.menu-parent');
      if (p) {
        e.preventDefault();
        var li = p.parentElement;
        if (!li) return;
        if (!li.classList.contains('open')) {
          var ul = li.parentElement;
          if (ul) {
            for (var i = 0; i < ul.children.length; i++) {
              var sib = ul.children[i];
              if (sib !== li && sib.classList && sib.classList.contains('menu-parent-li')) sib.classList.remove('open');
            }
          }
          li.classList.add('open');
        } else {
          li.classList.remove('open');
        }
      }
    });
  }
  function setActiveMenu(key) {
    var leaf = null;
    var links = document.querySelectorAll('#sideMenu .menu-leaf');
    for (var i = 0; i < links.length; i++) {
      var match = links[i].getAttribute('data-key') === key;
      links[i].classList.toggle('active', match);
      if (match) leaf = links[i];
    }
    if (!leaf) return;
    // 逐级展开祖先 li
    var li = leaf.closest('li');
    while (li) {
      li.classList.add('open');
      var ul = li.parentElement;
      li = ul && ul.tagName === 'UL' && ul.parentElement ? ul.parentElement.closest('li') : null;
    }
  }

  // ---------- 顶部标签页 ----------
  function upsertTab(key) {
    for (var i = 0; i < tabs.length; i++) if (tabs[i].key === key) return;
    if (tabs.length >= MAX_TABS) {
      // 超出上限：关闭最早且非首页、非当前页
      for (var j = 0; j < tabs.length; j++) {
        if (tabs[j].key !== HOME_KEY && tabs[j].key !== activeKey) { tabs.splice(j, 1); break; }
      }
    }
    tabs.push({ key: key, title: leafTitle(key) });
  }
  function renderTabs() {
    var bar = document.getElementById('pageTabs');
    if (!bar) return;
    bar.textContent = '';
    tabs.forEach(function (t) {
      var active = t.key === activeKey;
      var btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'page-tab' + (active ? ' active' : '');
      var sp = document.createElement('span');
      sp.className = 'tab-title';
      sp.textContent = t.title;
      btn.appendChild(sp);
      if (t.key !== HOME_KEY) {
        var x = document.createElement('span');
        x.className = 'tab-close';
        x.textContent = '×';
        x.title = '关闭';
        x.addEventListener('click', function (ev) { ev.stopPropagation(); closeTab(t.key); });
        btn.appendChild(x);
      }
      btn.addEventListener('click', function () { if (t.key !== activeKey) go(t.key); });
      bar.appendChild(btn);
    });
  }
  function closeTab(key) {
    if (key === HOME_KEY) return; // 首页固定保留
    var idx = -1;
    for (var i = 0; i < tabs.length; i++) if (tabs[i].key === key) { idx = i; break; }
    if (idx < 0) return;
    tabs.splice(idx, 1);
    if (key !== activeKey) { renderTabs(); return; }
    // 关闭的是当前页：切到相邻标签，没有则回首页
    var next = tabs.length ? tabs[Math.min(idx, tabs.length - 1)] : null;
    go(next ? next.key : HOME_KEY);
  }

  // ---------- 面包屑：首页 > 一级 > ... > 当前页（父级可点击回跳） ----------
  function crumbItem(text, href, active, iconCls) {
    var li = document.createElement('li');
    li.className = 'breadcrumb-item' + (active ? ' active' : '');
    if (iconCls) {
      var ic = document.createElement('i');
      ic.className = 'fa-solid ' + iconCls;
      li.appendChild(ic);
    }
    if (href && !active) {
      var a = document.createElement('a');
      a.href = href;
      a.textContent = text;
      li.appendChild(a);
    } else {
      li.appendChild(document.createTextNode(text));
    }
    return li;
  }
  function renderBreadcrumb(key, chain) {
    var bc = document.getElementById('breadcrumb');
    if (!bc) return;
    bc.textContent = '';
    var title = leafTitle(key);

    if (key === HOME_KEY || !chain) {
      bc.appendChild(crumbItem('首页', '', true, 'fa-house'));
      return;
    }

    // 纯展示模式：首页 + 全部祖先（不同层级即使同名也各自保留）+ 当前页。
    // 每级都不加超链接，只作当前位置指示。
    bc.appendChild(crumbItem('首页', null, false, 'fa-house'));

    for (var i = 0; i < chain.length - 1; i++) {
      var node = chain[i];
      if (!node.title) continue;
      bc.appendChild(crumbItem(node.title, null, false));
    }
    bc.appendChild(crumbItem(title, '', true));
  }

  // ---------- 路由主入口：侧栏叶子 / 标签页 / 面包屑 / hash 全部汇到这里 ----------
  function applyKey(key) {
    activeKey = key;
    if (tabs.length === 0) upsertTab(HOME_KEY); // 保证默认首页标签存在
    upsertTab(key);
    renderTabs();
    setActiveMenu(key);
    renderBreadcrumb(key, findChain(key));
    document.title = leafTitle(key) + ' · Gin Study';
    loadSchema(key);
  }
  function go(key) {
    var target = '#/pages/' + key;
    if (location.hash === target) applyKey(key);
    else location.hash = target; // 触发 hashchange → navigate
  }
  function navigate() {
    var key = pathOf(location.hash);
    // amis CRUD 默认 syncLocation：筛选/翻页时会在 hash 上追加查询串，但页面路径没变。
    // 此时只能“静默同步”，绝不能重建页面，否则每次检索都像整页跳转/刷新，
    // 标签页还会被拼成 field.json?status=1&page=1 之类、菜单高亮失效。
    if (key === activeKey && tabs.length) return;
    applyKey(key);
  }

  // ---------- 用户区 ----------
  function initUser() {
    var username = localStorage.getItem(USERNAME_KEY) || '管理员';
    var un = document.getElementById('topUserName');
    var av = document.getElementById('topAvatar');
    if (un) un.textContent = username;
    if (av) av.textContent = username.charAt(0).toUpperCase();
    var out = document.getElementById('logoutBtn');
    if (out) out.addEventListener('click', function (e) { e.preventDefault(); logout(false); });
  }

  // ---------- 主题切换（classic / light / grape） ----------
  var THEME_KEY = 'app_theme';
  var THEMES = ['classic', 'light', 'grape', 'ocean', 'sunset', 'forest'];
  function applyTheme(name) {
    if (THEMES.indexOf(name) < 0) name = 'classic';
    document.body.setAttribute('data-theme', name);
    localStorage.setItem(THEME_KEY, name);
    var items = document.querySelectorAll('#themeMenu [data-theme-val]');
    for (var i = 0; i < items.length; i++) {
      items[i].classList.toggle('active', items[i].getAttribute('data-theme-val') === name);
    }
  }
  function initTheme() {
    applyTheme(localStorage.getItem(THEME_KEY) || 'classic');
    var menu = document.getElementById('themeMenu');
    if (menu) {
      menu.addEventListener('click', function (e) {
        var it = e.target.closest('[data-theme-val]');
        if (it) { e.preventDefault(); applyTheme(it.getAttribute('data-theme-val')); }
      });
    }
  }

  // ---------- 后台主程序 ----------
  function initAdmin() {
    // 无 token 一律回登录页
    if (!localStorage.getItem(TOKEN_KEY)) { location.replace(LOGIN_URL); return; }
    initTheme();
    mount = document.getElementById('pageContent');
    buildMenu();
    initUser();
    window.addEventListener('hashchange', navigate);
    // 折叠侧栏
    var toggler = document.getElementById('sideToggle');
    if (toggler) toggler.addEventListener('click', function () {
      document.body.classList.toggle('sidebar-collapsed');
      localStorage.setItem('sidebar_collapsed', document.body.classList.contains('sidebar-collapsed') ? '1' : '0');
    });
    if (localStorage.getItem('sidebar_collapsed') === '1') document.body.classList.add('sidebar-collapsed');
    // 全屏
    var fsBtn = document.getElementById('fullscreenBtn');
    if (fsBtn) fsBtn.addEventListener('click', function () {
      if (!document.fullscreenElement) document.documentElement.requestFullscreen();
      else document.exitFullscreen();
    });
    navigate();
  }

  // ---------- 登录页程序 ----------
  function initLogin() {
    if (localStorage.getItem(TOKEN_KEY)) { location.replace('/admin/'); return; }
    var form = document.getElementById('loginForm');
    if (!form) return;
    var alertEl = document.getElementById('loginAlert');
    var alertText = document.getElementById('loginAlertText');
    var btn = document.getElementById('loginBtn');
    function showAlert(msg, type) {
      alertEl.className = 'alert show ' + (type === 'error' ? 'alert-danger' : 'alert-success');
      alertEl.classList.remove('d-none');
      alertText.textContent = msg;
    }
    form.addEventListener('submit', function (e) {
      e.preventDefault();
      alertEl.classList.add('d-none');
      var username = document.getElementById('username').value.trim();
      var password = document.getElementById('password').value;
      if (!username || !password) return showAlert('请输入用户名和密码', 'error');
      var remember = document.getElementById('remember') && document.getElementById('remember').checked;
      btn.disabled = true; btn.textContent = '登录中…';
      fetch('/api/v1/users/login', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
        body: JSON.stringify({ username: username, password: password })
      }).then(function (r) { return r.json(); }).then(function (d) {
        if (d.code === 0 && d.data && d.data.token) {
          localStorage.setItem(TOKEN_KEY, d.data.token);
          if (remember) localStorage.setItem('admin_remember', '1');
          var u = d.data.user || {};
          localStorage.setItem(USERNAME_KEY, u.username || username);
          if (u.id) localStorage.setItem('admin_user_id', u.id);
          showAlert('登录成功，正在跳转…', 'success');
          setTimeout(function () { location.href = '/admin/'; }, 300);
        } else {
          btn.disabled = false; btn.textContent = '登 录';
          showAlert(d.message || '用户名或密码错误', 'error');
        }
      }).catch(function () {
        btn.disabled = false; btn.textContent = '登 录';
        showAlert('网络错误，请稍后重试', 'error');
      });
    });
  }

  // ---------- 启动 ----------
  function boot() {
    if (document.getElementById('loginPage')) initLogin();
    else if (document.getElementById('appShell')) initAdmin();
  }
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', boot);
  else boot();

  window.App = { go: go };
})();
