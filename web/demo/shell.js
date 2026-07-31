// 共享外壳：渲染顶栏 + 侧栏，处理主题切换与导航高亮
// 用法：页面 body 里放 <div class="app" id="app"></div>，然后
//   renderShell(document.getElementById('app'), 'messages.html', contentEl)
(function () {
  // 主题
  const root = document.documentElement;
  function applyTheme(t) {
    root.dataset.theme = t;
    const b = document.getElementById('themeBtn');
    if (b) b.textContent = t === 'dark' ? '☀️' : '🌙';
  }
  window.__applyTheme = applyTheme;
  applyTheme(localStorage.getItem('tdl-theme') ||
    (matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'));

  window.renderShell = function (app, activePage, contentEl) {
    app.innerHTML = '';

    const topbar = document.createElement('header');
    topbar.className = 'topbar';
    topbar.innerHTML = `
      <div class="logo">⬇ tdl <span class="badge">web</span></div>
      <div class="spacer"></div>
      <div class="conn"><span class="dot"></span>已连接 · @eric_chan</div>
      <button class="iconbtn" id="themeBtn" title="切换主题">🌙</button>`;
    app.appendChild(topbar);

    const main = document.createElement('div');
    main.className = 'main';

    const sider = document.createElement('nav');
    sider.className = 'sider';
    const items = [
      { page: 'messages.html', ico: '💬', label: '消息' },
      { page: 'tasks.html', ico: '📥', label: '任务' },
      { page: '', ico: '⚙️', label: '设置' },
    ];
    items.forEach(it => {
      const b = document.createElement('button');
      b.className = 'nav-item' + (it.page === activePage ? ' active' : '');
      b.innerHTML = `<span class="ico">${it.ico}</span>${it.label}`;
      b.onclick = () => { if (it.page) location.href = it.page; };
      sider.appendChild(b);
    });

    const content = document.createElement('div');
    content.className = 'content';
    content.appendChild(contentEl);

    main.appendChild(sider);
    main.appendChild(content);
    app.appendChild(main);

    const tb = document.getElementById('themeBtn');
    tb.onclick = () => {
      const t = root.dataset.theme === 'dark' ? 'light' : 'dark';
      localStorage.setItem('tdl-theme', t);
      applyTheme(t);
    };
    applyTheme(root.dataset.theme);
  };
})();
