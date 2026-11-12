// 在页面开始渲染之前同步执行:标记"脚本可用",并应用已保存的主题,避免加载时闪一下默认样式。
// 其余交互都在 app.js 里(延迟加载)。
(function () {
  var root = document.documentElement;
  root.classList.add('js');
  try {
    if (window.localStorage.getItem('mb.theme') === 'light') root.dataset.theme = 'light';
  } catch (e) {
    // 浏览器禁用了本地存储,使用默认的深色主题
  }
})();
