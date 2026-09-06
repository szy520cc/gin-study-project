/* =========================================================
   Gin Study 后台 · 菜单配置（独立文件，改动频繁便于维护）
   使用：在 index.html 里先于 app.js 引入本文件；
   app.js 通过 window.APP_MENU 读取。

   结构：
     {
       section: '分组名',
       items: [ { title: '菜单名', key: 'home.json', icon: 'fa-gauge-high' } ]
     }
   key = internal/web/assets/pages 下的 schema 文件名，访问地址为 #/pages/<key>
   icon = Font Awesome 6 free 类名（由 amis sdk.css 提供），如 fa-users
   ========================================================= */
window.APP_MENU = [
  {
    section: '概览',
    items: [{ title: '首页', key: 'home.json', icon: 'fa-gauge-high' }]
  },
  {
    section: '项目管理',
    items: [
      { title: '项目管理', key: 'project.json', icon: 'fa-diagram-project' },
      { title: '字段管理', key: 'field.json', icon: 'fa-list-check' },
      { title: '配置包管理', key: 'config-pack.json', icon: 'fa-box-archive' },
      { title: '配置管理', key: 'config.json', icon: 'fa-file-code' }
    ]
  }
];
