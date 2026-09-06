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
    // 用 DOM + textContent 构建面包屑，避免 schema title 进入 innerHTML
    var bc = document.getElementById('breadcrumb');
    if (bc) {
      bc.textContent = '';
      var liHome = document.createElement('li');
      liHome.className = 'breadcrumb-item';
      var aHome = document.createElement('a');
      aHome.href = '#/pages/home.json';
      aHome.textContent = '首页';
      liHome.appendChild(aHome);
      bc.appendChild(liHome);
      if (m) {
        var liSection = document.createElement('li');
        liSection.className = 'breadcrumb-item';
        liSection.textContent = m.section;
        bc.appendChild(liSection);
      }
      var liTitle = document.createElement('li');
      liTitle.className = 'breadcrumb-item active';
      liTitle.textContent = title;
      bc.appendChild(liTitle);
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
