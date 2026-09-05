/* =========================================================
   Gin Study 后台 · 公共脚本（index.html / login.html 共用）
   仅一个 JS：token、鉴权 fetcher、菜单、hash 路由、页面渲染、登出
   ========================================================= */
'use strict';
(function () {
  var TOKEN_KEY = 'admin_token';
  var USERNAME_KEY = 'admin_username';

  // ---------- 菜单配置（key = pages 下的 schema 文件名） ----------
  var MENU = [
    {
      section: '概览',
      items: [{ title: '首页', key: 'home.json', icon: 'fa-gauge-high' }]
    },
    {
      section: '系统管理',
      items: [
        { title: '用户列表', key: 'users.json', icon: 'fa-users' },
        { title: '新增用户', key: 'user-add.json', icon: 'fa-user-plus' }
      ]
    },
    {
      section: '日志审计',
      items: [
        { title: '访问日志', key: 'logs-access.json', icon: 'fa-file-lines' },
        { title: '操作日志', key: 'logs-op.json', icon: 'fa-pen-to-square' }
      ]
    },
    {
      section: '系统配置',
      items: [
        { title: '基础配置', key: 'config-base.json', icon: 'fa-gear' },
        { title: '邮箱配置', key: 'config-email.json', icon: 'fa-envelope' },
        { title: '短信配置', key: 'config-sms.json', icon: 'fa-message' }
      ]
    },
    {
      section: '监控报表',
      items: [
        { title: '系统监控', key: 'monitor.json', icon: 'fa-chart-line' },
        { title: '用户报表', key: 'report-users.json', icon: 'fa-chart-pie' },
        { title: '运营报表', key: 'report-ops.json', icon: 'fa-chart-column' }
      ]
    }
  ];

  var PAGES_BASE = '/admin/pages/';
  var LOGIN_URL = '/admin/login';
  var mount = null;

  // =========================================================
  // 鉴权 fetcher：amis.embed 的第 4 个参数 env 里传入。
  // amis 6.x 契约：resolve { ok, data, msg }，ok=false 时 amis 自动提示 msg。
  // =========================================================
  function authFetcher(arg1, arg2) {
    // 兼容两种调用：fetcher(config) 或 fetcher(url, options)
    var cfg;
    if (typeof arg1 === 'string') {
      cfg = { url: arg1, method: 'GET', data: undefined };
      if (arg2 && typeof arg2 === 'object') { cfg.method = arg2.method || cfg.method; cfg.data = arg2.data; cfg.headers = arg2.headers; }
    } else if (arg1 && typeof arg1 === 'object') {
      cfg = arg1;
      if (arg2 && typeof arg2 === 'object' && cfg.data === undefined) cfg = Object.assign({}, arg1, { data: arg2.data });
    } else {
      cfg = { url: '', method: 'GET' };
    }

    var url = cfg.url || '';
    var method = (cfg.method || 'GET').toUpperCase();
    var headers = new Headers(cfg.headers || {});
    headers.set('Accept', 'application/json');

    var token = localStorage.getItem(TOKEN_KEY);
    if (token) headers.set('Authorization', 'Bearer ' + token);

    var body;
    var data = cfg.data;
    if (data && !(data instanceof FormData)) {
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
        body = JSON.stringify(data);
      }
    } else if (data instanceof FormData) {
      body = data; // 交给浏览器设置 multipart boundary
    }

    return fetch(url, { method: method, headers: headers, body: body }).then(function (res) {
      return res.json().catch(function () { return {}; }).then(function (raw) {
        raw = raw || {};
        // 未登录 / token 失效：HTTP 401
        if (res.status === 401) {
          logout(true);
          return { ok: false, msg: raw.message || '登录已失效，请重新登录', data: null };
        }
        // 业务成功：code===0
        var code = raw.code;
        if (code === undefined) code = res.ok ? 0 : 1;
        var msg = raw.message || raw.msg || (res.ok ? '' : '请求失败（HTTP ' + res.status + '）');
        var d = raw.data;
        // 后端列表形如 {list,total}，转为 amis 需要的 {items,total}
        if (d && Array.isArray(d.list)) d = { items: d.list, total: d.total, count: d.total };
        return { ok: res.ok && code === 0, msg: msg, data: d };
      });
    }).catch(function (err) {
      return { ok: false, msg: '网络错误：' + err.message, data: null };
    });
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

  function renderPage(schema) {
    var el = document.getElementById('pageContent');
    if (!el || !schema) return;
    el.innerHTML = '';
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

  // ---------- 加载页面 schema ----------
  function loadSchema(key) {
    showLoading();
    fetch(PAGES_BASE + key).then(function (res) {
      if (!res.ok) throw new Error('页面加载失败 HTTP ' + res.status);
      return res.json();
    }).then(function (schema) {
      renderPage(schema);
      afterPage(key, schema);
    }).catch(function (err) {
      if (mount) mount.innerHTML = '<div class="content-hint">' + err.message + '</div>';
    });
  }

  // ---------- 路由：hash #/pages/xxx.json ----------
  function currentKey() {
    var h = location.hash;
    if (h && h.indexOf('#/pages/') === 0) return h.slice('#/pages/'.length);
    return 'home.json';
  }
  function go(key) {
    var target = '#/pages/' + key;
    if (location.hash === target) loadSchema(key);
    else location.hash = target;
  }
  function navigate() {
    var key = currentKey();
    setActiveMenu(key);
    loadSchema(key);
  }

  // ---------- 菜单 ----------
  function buildMenu() {
    var html = '';
    MENU.forEach(function (g) {
      html += '<div class="menu-section">' + g.section + '</div>';
      g.items.forEach(function (it) {
        html += '<a class="menu-item" data-key="' + it.key + '" href="#/pages/' + it.key + '">' +
          '<i class="fa-solid ' + it.icon + '"></i><span>' + it.title + '</span></a>';
      });
    });
    var el = document.getElementById('sideMenu');
    if (el) el.innerHTML = html;
  }
  function setActiveMenu(key) {
    var links = document.querySelectorAll('#sideMenu .menu-item');
    for (var i = 0; i < links.length; i++) {
      links[i].classList.toggle('active', links[i].getAttribute('data-key') === key);
    }
  }
  function findInMenu(key) {
    for (var i = 0; i < MENU.length; i++) {
      for (var j = 0; j < MENU[i].items.length; j++) {
        if (MENU[i].items[j].key === key) {
          return { section: MENU[i].section, title: MENU[i].items[j].title };
        }
      }
    }
    return null;
  }
  function afterPage(key, schema) {
    var m = findInMenu(key);
    var title = (schema && schema.title) || (m && m.title) || key;
    document.title = title + ' · Gin Study';
    var bc = document.getElementById('breadcrumb');
    if (bc) {
      bc.innerHTML = '<li class="breadcrumb-item"><a href="#/pages/home.json">首页</a></li>' +
        (m ? '<li class="breadcrumb-item">' + m.section + '</li>' : '') +
        '<li class="breadcrumb-item active">' + title + '</li>';
    }
    var pt = document.getElementById('pageTitle');
    if (pt) pt.textContent = title;
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

  // ---------- 后台主程序 ----------
  function initAdmin() {
    // 无 token 一律回登录页
    if (!localStorage.getItem(TOKEN_KEY)) { location.replace(LOGIN_URL); return; }
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
