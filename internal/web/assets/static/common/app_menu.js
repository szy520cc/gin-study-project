/* =========================================================
   Gin Study 后台 · 菜单配置（树形结构，支持无限层级）
   使用：在 index.html 里先于 app.js 引入；app.js 通过 window.APP_MENU 读取。

   节点结构：
     {
       title:    '菜单名/分组名',
       icon:     'fa-users'（Font Awesome 6 类名，可选）
       key:      'home.json'（仅叶子节点有：pages 下的 schema 文件名，访问 #/pages/<key>）
       children: [ ...子节点，可继续嵌套... ]（父节点无 key，仅作为容器）
     }

   规则（app.js 已内置处理）：
     1. 父节点（有 children）只做折叠/展开，不可跳转；
     2. 只有叶子节点（无 children）可点击跳转并开新标签页；
     3. 面包屑由叶子向上递归父级生成：首页 > 分组 > ... > 当前页。
   ========================================================= */
window.APP_MENU = [
  {
    "title": "概览",
    "icon": "fa-gauge-high",
    "children": [
      {
        "title": "首页",
        "key": "home.json",
        "icon": "fa-gauge-high"
      }
    ]
  },
  {
    "title": "项目管理",
    "icon": "fa-diagram-project",
    "children": [
      {
        "title": "项目管理",
        "key": "project.json",
        "icon": "fa-diagram-project"
      },
      {
        "title": "字段管理",
        "key": "field.json",
        "icon": "fa-list-check"
      }
    ]
  },
  {
    "title": "配置管理",
    "icon": "fa-cubes",
    "children": [
      {
        "title": "配置包管理",
        "key": "config-pack.json",
        "icon": "fa-box-archive"
      },
      {
        "title": "配置管理",
        "key": "config.json",
        "icon": "fa-file-code"
      }
    ]
  }
];
