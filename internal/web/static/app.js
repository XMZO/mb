// MB 资产面板的前端脚本,不依赖任何第三方库。
//
// 页面由服务端渲染,这里只负责交互:
//   - 通用:主题、轻提示、确认对话框、复制、带确认的表单
//   - 面板:到期筛选 / 搜索 / 排序,点击小块弹出管理面板,批量模式,拖拽排序,续费与导出对话框
//   - 表单页:永久开关、硬盘行的增删、删除按钮
//   - 设置页:分类切换
//
// 页面的 CSP 禁止内联脚本,所有行为都通过 data-* 属性在这里绑定。
(() => {
  'use strict';

  const PERMANENT = '9999-12-31';

  const $ = (selector, root = document) => root.querySelector(selector);
  const $$ = (selector, root = document) => Array.from(root.querySelectorAll(selector));

  // ---------- 基础工具 ----------

  // 对 localStorage / sessionStorage 的包装:浏览器禁用存储时静默降级,不影响页面使用。
  function storage(name) {
    let area = null;
    try {
      area = window[name];
    } catch {
      // 访问被拒绝(例如禁用了 Cookie)
    }
    return {
      get(key, fallback = '') {
        try {
          const value = area.getItem(key);
          return value === null ? fallback : value;
        } catch {
          return fallback;
        }
      },
      set(key, value) {
        try {
          area.setItem(key, value);
        } catch {
          // 忽略
        }
      },
      remove(key) {
        try {
          area.removeItem(key);
        } catch {
          // 忽略
        }
      },
    };
  }

  const prefs = storage('localStorage'); // 长期偏好:主题
  const session = storage('sessionStorage'); // 本次会话:筛选条件、滚动位置、设置页停留的分类

  let toastTimer = 0;

  function toast(message, kind = 'info') {
    const el = $('#toast');
    if (!el) return;
    el.textContent = message;
    el.dataset.kind = kind;
    el.classList.add('is-visible');
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => el.classList.remove('is-visible'), kind === 'error' ? 5000 : 2200);
  }

  // 以模态方式打开对话框;用户提交了其中的表单(点"确定"或按回车)时结果为 true,
  // 点取消、按 Esc 时为 false。表单自己的校验可以用 preventDefault 阻止关闭。
  function openDialog(dialog) {
    const form = $('form', dialog);
    let confirmed = false;
    const onSubmit = (event) => {
      if (!event.defaultPrevented) confirmed = true;
    };
    form.addEventListener('submit', onSubmit);
    dialog.showModal();
    return new Promise((resolve) => {
      dialog.addEventListener(
        'close',
        () => {
          form.removeEventListener('submit', onSubmit);
          resolve(confirmed);
        },
        { once: true },
      );
    });
  }

  function confirmDialog(message, { okText = '确定', danger = false } = {}) {
    const dialog = $('#confirmDialog');
    $('[data-role="message"]', dialog).textContent = message;
    const ok = $('[data-role="ok"]', dialog);
    ok.textContent = okText;
    ok.classList.toggle('btn-danger', danger);
    ok.classList.toggle('btn-primary', !danger);
    return openDialog(dialog);
  }

  // 调用后端的 JSON 接口;失败时抛出带有可读信息的 Error。
  async function api(path, payload) {
    const token = $('meta[name="csrf-token"]');
    let response;
    try {
      response = await fetch(path, {
        method: 'POST',
        credentials: 'same-origin',
        headers: {
          'Content-Type': 'application/json',
          'X-CSRF-Token': token ? token.content : '',
        },
        body: JSON.stringify(payload),
      });
    } catch {
      throw new Error('网络请求失败，请检查连接后重试');
    }
    if (response.ok) return;

    let message = `请求失败（HTTP ${response.status}）`;
    try {
      const data = await response.json();
      if (data && data.error) message = data.error;
    } catch {
      // 响应不是 JSON,保留默认提示
    }
    throw new Error(message);
  }

  async function copyText(text) {
    try {
      if (navigator.clipboard && window.isSecureContext) {
        await navigator.clipboard.writeText(text);
        return true;
      }
    } catch {
      // 落到下面的兼容方案
    }
    // 非 HTTPS 页面(例如直接用 IP 访问)没有 Clipboard API,退回到 execCommand。
    const helper = document.createElement('textarea');
    helper.value = text;
    helper.readOnly = true;
    helper.className = 'clipboard-helper';
    // 模态对话框打开时,只有对话框内部的元素才能获得焦点和选区。
    (document.querySelector('dialog[open]') || document.body).appendChild(helper);
    helper.select();
    let copied = false;
    try {
      copied = document.execCommand('copy');
    } catch {
      copied = false;
    }
    helper.remove();
    return copied;
  }

  // 把 YYYY-MM-DD 顺延若干个月;目标月份没有这一天时取月末。与后端 asset.AddMonths 的规则一致。
  function addMonths(iso, months) {
    const [year, month, day] = iso.split('-').map(Number);
    if (!year || !month || !day) return '';
    const first = new Date(Date.UTC(year, month - 1 + months, 1));
    const lastDay = new Date(Date.UTC(first.getUTCFullYear(), first.getUTCMonth() + 1, 0)).getUTCDate();
    const pad = (n) => String(n).padStart(2, '0');
    return `${first.getUTCFullYear()}-${pad(first.getUTCMonth() + 1)}-${pad(Math.min(day, lastDay))}`;
  }

  // "12" -> "1 年","3" -> "3 个月"
  function periodLabel(months) {
    return months % 12 === 0 ? `${months / 12} 年` : `${months} 个月`;
  }

  // 执行操作后刷新页面,并回到原来的滚动位置。
  function reloadKeepingPosition() {
    session.set('mb.scroll', String(window.scrollY));
    window.location.reload();
  }

  function restoreScroll() {
    const saved = session.get('mb.scroll');
    if (saved === '') return;
    session.remove('mb.scroll');
    window.scrollTo({ top: Number(saved) || 0, behavior: 'instant' });
  }

  // ---------- 通用交互 ----------

  function initTheme() {
    const button = $('[data-action="toggle-theme"]');
    if (!button) return;
    const root = document.documentElement;
    const showLabel = () => {
      button.textContent = root.dataset.theme === 'light' ? '深色' : '浅色'; // 显示的是"切换到"的主题
    };
    showLabel();
    button.addEventListener('click', () => {
      root.dataset.theme = root.dataset.theme === 'light' ? 'dark' : 'light';
      prefs.set('mb.theme', root.dataset.theme);
      showLabel();
    });
  }

  function initGlobalActions() {
    document.addEventListener('click', async (event) => {
      const copy = event.target.closest('button[data-copy]');
      if (copy) {
        const text = copy.dataset.copy;
        toast((await copyText(text)) ? `已复制：${text}` : '复制失败，请手动选择文本复制');
        return;
      }
      const close = event.target.closest('[data-action="close-dialog"]');
      if (close) close.closest('dialog').close();
    });

    // 带 data-confirm 的表单(或提交按钮)在提交前先弹出确认。
    document.addEventListener('submit', async (event) => {
      const form = event.target;
      const submitter = event.submitter;
      const message = (submitter && submitter.dataset.confirm) || form.dataset.confirm;
      if (!message) return;
      if (form.dataset.confirmed === '1') {
        delete form.dataset.confirmed;
        return;
      }
      event.preventDefault();
      if (!(await confirmDialog(message, { okText: '继续', danger: true }))) return;
      form.dataset.confirmed = '1';
      form.requestSubmit(submitter || undefined); // 带上原来的提交按钮,保留它的 name / value
    });
  }

  // ---------- 面板:续费与付费周期对话框 ----------

  // 续费对话框(自定义时长 / 指定日期 / 永久)。
  // ask() 返回要提交给批量接口的 { action, value },用户取消时返回 null。
  function createRenewalDialog() {
    const dialog = $('#renewDialog');
    if (!dialog) return null;
    const form = $('form', dialog);
    const fields = form.elements;
    const target = $('[data-role="target"]', dialog);
    const preview = $('[data-role="preview"]', dialog);
    const error = $('[data-role="error"]', dialog);
    let currentExpiry = ''; // 单条续费时为该记录的到期日,用于预览;批量时为空

    const hasDate = () => currentExpiry !== '' && currentExpiry !== PERMANENT;
    const monthsValue = () => {
      const months = Number(fields.months.value);
      return Number.isInteger(months) && months >= 1 && months <= 1200 ? months : 0;
    };
    const updatePreview = () => {
      const months = monthsValue();
      const next = hasDate() && months ? addMonths(currentExpiry, months) : '';
      preview.textContent = next ? `新的到期日：${next}` : '';
    };

    fields.months.addEventListener('input', () => {
      fields.mode.value = 'extend';
      updatePreview();
    });
    fields.date.addEventListener('input', () => {
      fields.mode.value = 'date';
    });

    // 在对话框关闭之前校验,出错时留在对话框里。
    form.addEventListener('submit', (event) => {
      let message = '';
      if (fields.mode.value === 'extend' && !monthsValue()) message = '请输入 1-1200 之间的月数';
      if (fields.mode.value === 'date' && !fields.date.value) message = '请选择新的到期日';
      error.textContent = message;
      error.hidden = message === '';
      if (message) event.preventDefault();
    });

    return {
      async ask({ title, expires = '', months = 12 }) {
        currentExpiry = expires;
        target.textContent = hasDate()
          ? `${title}，当前到期日 ${expires}`
          : expires === PERMANENT
            ? `${title}，当前为永久有效`
            : title;
        fields.months.value = months;
        fields.date.value = hasDate() ? expires : '';
        // 永久的记录没法"顺延",默认选中指定日期。
        fields.mode.value = expires === PERMANENT ? 'date' : 'extend';
        error.hidden = true;
        updatePreview();

        if (!(await openDialog(dialog))) return null;
        switch (fields.mode.value) {
          case 'date':
            return { action: 'renew', value: fields.date.value };
          case 'permanent':
            return { action: 'renew', value: PERMANENT };
          default:
            return { action: 'extend', value: String(monthsValue()) };
        }
      },
    };
  }

  // 询问付费周期(月);用户取消时返回 0。
  async function askBillingMonths() {
    const dialog = $('#monthsDialog');
    const input = $('[name="months"]', dialog);
    input.value = 12;
    return (await openDialog(dialog)) ? Number(input.value) : 0;
  }

  // ---------- 面板 ----------

  const collator = new Intl.Collator('zh-CN', { numeric: true, sensitivity: 'base' });

  // 空值排在最后的字符串比较。
  function compareText(a, b) {
    if (a === b) return 0;
    if (a === '') return 1;
    if (b === '') return -1;
    return collator.compare(a, b);
  }

  // 各排序方式的比较函数,读取的都是小块上的 data-* 属性。
  const sorters = {
    name: (a, b) => compareText(a.dataset.name, b.dataset.name),
    suffix: (a, b) => compareText(a.dataset.suffix, b.dataset.suffix) || compareText(a.dataset.name, b.dataset.name),
    group: (a, b) => compareText(a.dataset.group, b.dataset.group) || compareText(a.dataset.name, b.dataset.name),
    expiry: (a, b) => compareText(a.dataset.expires, b.dataset.expires), // ISO 日期可以直接按字符串比较
    price: (a, b) => (Number(b.dataset.price) || 0) - (Number(a.dataset.price) || 0),
  };

  // 到期状态的筛选条件;参数是距到期的天数,永久或未知时为 null。
  const dueFilters = {
    expired: (days) => days !== null && days < 0,
    d30: (days) => days !== null && days >= 0 && days <= 30,
    d90: (days) => days !== null && days >= 0 && days <= 90,
  };

  const daysOf = (tile) => (tile.dataset.days === '' ? null : Number(tile.dataset.days));
  const idsOf = (tiles) => tiles.map((tile) => Number(tile.dataset.id));

  // 决定小块网格的列数。网格里同一排的小块是等高的,只要有一个小块的内容多出一行,
  // 整排都会被撑高、留出空白。所以这里从能排下的最多列数开始,逐个减少,直到所有小块
  // 都一样高为止:列宽由最长的那条内容决定,每个小块都一样宽、一样高,内容完整显示。
  // 测量包括被搜索、筛选隐藏的小块,列数不会随筛选来回变化。
  function fitColumns(list) {
    const tiles = $$('.tile', list);
    const minWidth = 240; // 每列至少这么宽;实际宽度由内容决定
    const gap = parseFloat(getComputedStyle(list).columnGap) || 0;
    const most = Math.max(1, Math.floor((list.clientWidth + gap) / (minWidth + gap)));

    list.classList.add('is-measuring');
    let columns = 1;
    for (let n = most; n >= 2; n -= 1) { // 小块比列数少时也按这个列数排,不让单个小块被拉满整排
      list.dataset.cols = String(n);
      const heights = tiles.map((tile) => tile.getBoundingClientRect().height);
      if (Math.max(...heights) - Math.min(...heights) < 1.5) {
        columns = n;
        break;
      }
    }
    list.dataset.cols = String(columns);
    list.classList.remove('is-measuring');
  }

  function initBoard() {
    const board = $('[data-role="board"]');
    if (!board) return;
    const admin = board.hasAttribute('data-admin');

    // 域名、VPS 各是一组;搜索、筛选、排序同时作用于两组。
    const groups = $$('.group[data-kind]', board).map((section) => ({
      kind: section.dataset.kind,
      list: $('[data-role="tiles"]', section),
      tiles: [],
      ordered: [], // 按当前排序排列
      matched: [], // 其中符合搜索与筛选条件的
      count: $('[data-role="count"]', section),
      empty: $('[data-role="empty"]', section),
    }));
    let allTiles = [];

    // 从页面上读出各组的小块。首次加载和局部刷新之后都会调用。
    function collectTiles() {
      groups.forEach((group) => {
        group.tiles = group.list ? $$('.tile', group.list) : [];
        group.tiles.forEach((tile, index) => {
          tile.kind = group.kind;
          tile.manualIndex = index; // 在"默认顺序"(服务端给出的顺序)中的位置
        });
        group.ordered = group.tiles;
        group.matched = group.tiles;
      });
      allTiles = groups.flatMap((group) => group.tiles);
    }
    collectTiles();

    const el = {
      search: $('[data-role="search"]', board),
      sort: $('[data-role="sort"]', board),
      filters: $$('.filter[data-filter]', board),
      saveOrder: $('[data-role="save-order"]', board),
      selectToggle: $('[data-action="select-mode"]', board),
      selectionBar: $('[data-role="selection-bar"]'),
    };

    // ---- 状态:本次会话内保留,执行操作刷新页面后能回到原来的视图 ----

    const state = { query: '', filter: 'all', sort: 'manual' };
    try {
      Object.assign(state, JSON.parse(session.get('mb.board', '{}')));
    } catch {
      // 存储的内容损坏,使用默认值
    }
    if (!$$('option', el.sort).some((option) => option.value === state.sort)) state.sort = 'manual';

    // 到期状态的数量:数字既是统计,也是筛选按钮。数量为 0 的状态不显示。
    function updateCounts() {
      const counts = { all: allTiles.length, expired: 0, d30: 0, d90: 0 };
      allTiles.forEach((tile) => {
        Object.keys(dueFilters).forEach((key) => {
          if (dueFilters[key](daysOf(tile))) counts[key] += 1;
        });
      });
      el.filters.forEach((button) => {
        const key = button.dataset.filter;
        $('b', button).textContent = counts[key];
        button.hidden = key !== 'all' && counts[key] === 0;
      });
      // 当前选中的状态已经没有记录了(例如最后一个过期的刚续了费),回到"全部"
      if (!el.filters.some((button) => button.dataset.filter === state.filter && !button.hidden)) state.filter = 'all';
    }
    updateCounts();

    let selecting = false; // 是否处于批量模式
    const selected = new Set();

    function compare(a, b) {
      // 置顶的始终排在最前;其余按所选方式排序,相同时保持默认顺序。
      const pinned = (b.dataset.pinned === '1') - (a.dataset.pinned === '1');
      if (pinned) return pinned;
      const sorter = sorters[state.sort];
      return (sorter ? sorter(a, b) : 0) || a.manualIndex - b.manualIndex;
    }

    function matches(tile) {
      if (state.query && !tile.dataset.search.includes(state.query)) return false;
      const filter = dueFilters[state.filter];
      return filter ? filter(daysOf(tile)) : true;
    }

    // 重新排序并调整 DOM 顺序。只在排序方式或默认顺序变化时调用。
    function reorder() {
      groups.forEach((group) => {
        group.ordered = group.tiles.slice().sort(compare);
        group.ordered.forEach((tile) => group.list.appendChild(tile));
      });
    }

    // 根据搜索与筛选决定显示哪些小块,并刷新周边的控件。
    function render() {
      const draggable = admin && state.sort === 'manual' && !selecting; // 只有默认顺序下才能拖拽排序
      groups.forEach((group) => {
        group.matched = group.ordered.filter(matches);
        const visible = new Set(group.matched);
        group.tiles.forEach((tile) => {
          tile.hidden = !visible.has(tile);
          tile.draggable = draggable;
        });
        if (group.count) {
          group.count.textContent =
            group.matched.length === group.tiles.length
              ? String(group.tiles.length)
              : `${group.matched.length} / ${group.tiles.length}`;
        }
        if (group.empty) group.empty.hidden = group.matched.length > 0;
      });

      el.filters.forEach((button) => {
        const active = button.dataset.filter === state.filter;
        button.classList.toggle('is-active', active);
        button.setAttribute('aria-pressed', String(active));
      });
      if (el.saveOrder) el.saveOrder.hidden = state.sort === 'manual';

      session.set('mb.board', JSON.stringify(state));
      renderSelection();
    }

    function renderSelection() {
      if (!admin) return;
      allTiles.forEach((tile) => tile.classList.toggle('is-selected', selected.has(tile)));
      $('[data-role="selected-count"]', el.selectionBar).textContent = `已选 ${selected.size} 项`;
      const hasServers = allTiles.some((tile) => selected.has(tile) && tile.kind === 'vps');
      $$('[data-batch]', el.selectionBar).forEach((button) => {
        button.disabled = selected.size === 0 || (button.dataset.batch === 'billing_months' && !hasServers);
      });
    }

    // ---- 搜索 / 筛选 / 排序 ----

    el.search.value = state.query;
    el.search.addEventListener('input', () => {
      state.query = el.search.value.trim().toLowerCase();
      render();
    });
    el.filters.forEach((button) => {
      button.addEventListener('click', () => {
        state.filter = button.dataset.filter;
        render();
      });
    });
    el.sort.value = state.sort;
    el.sort.addEventListener('change', () => {
      state.sort = el.sort.value;
      reorder();
      render();
    });

    // 统计币种的下拉框在概览里;概览会被局部刷新整块替换,所以用事件委托。
    board.addEventListener('change', (event) => {
      if (!event.target.matches('[data-role="base-currency"]')) return;
      window.location.href = '/?base=' + encodeURIComponent(event.target.value);
    });

    if (admin) bindAdminActions();

    reorder();
    render();
    restoreScroll();

    // 决定列数。宽度变化(旋转屏幕、调整窗口)和字体加载完成都会改变排版,两种情况都重新算一次。
    const refit = () => groups.forEach((group) => group.list && fitColumns(group.list));
    refit();
    let lastWidth = board.clientWidth;
    new ResizeObserver(() => {
      if (board.clientWidth === lastWidth) return; // 只关心宽度;列数变化引起的高度变化不用理会
      lastWidth = board.clientWidth;
      refit();
    }).observe(board);
    if (document.fonts) document.fonts.ready.then(refit);

    // 操作成功后的局部刷新:在后台重新取一次面板,只替换概览和小块。
    // 剩余天数、折算金额、年度总额、排序位置都以服务端算出的为准,前端不重复实现这些规则;
    // 当前的搜索、筛选、排序、批量选择和滚动位置都保持不变。changed 里的小块会短暂高亮。
    // 页面结构对不上(例如登录已失效)时抛出错误,由调用方退回整页刷新。
    const keyOf = (tile) => `${tile.kind}:${tile.dataset.id}`;

    async function refreshBoard(changed = new Set()) {
      const response = await fetch(window.location.pathname, {
        credentials: 'same-origin',
        headers: { Accept: 'text/html' },
      });
      if (!response.ok || response.redirected) throw new Error('refresh failed');
      const doc = new DOMParser().parseFromString(await response.text(), 'text/html');
      const fresh = $('[data-role="board"]', doc);
      if (!fresh || fresh.hasAttribute('data-admin') !== admin) throw new Error('unexpected page');

      const lists = groups.map((group) => $(`.group[data-kind="${group.kind}"] [data-role="tiles"]`, fresh));
      if (groups.some((group, i) => !group.list !== !lists[i])) throw new Error('layout changed');

      const keepSelected = new Set([...selected].map(keyOf));
      $('.summary', board).replaceWith(document.adoptNode($('.summary', fresh)));
      groups.forEach((group, i) => {
        // 先复制成普通数组:adoptNode 会把节点从原来的列表里移走,直接遍历 children 会跳过一半
        if (group.list) group.list.replaceChildren(...[...lists[i].children].map((node) => document.adoptNode(node)));
      });

      collectTiles();
      selected.clear();
      allTiles.forEach((tile) => {
        if (keepSelected.has(keyOf(tile))) selected.add(tile);
      });
      updateCounts();
      reorder();
      render();
      refit();

      allTiles.forEach((tile) => {
        if (!changed.has(keyOf(tile))) return;
        tile.classList.add('is-updated');
        tile.addEventListener('animationend', () => tile.classList.remove('is-updated'), { once: true });
      });
    }

    // ---- 以下仅管理员可用 ----

    function bindAdminActions() {
      const renewal = createRenewalDialog();
      const kindLabel = (tile) => (tile.kind === 'domain' ? '域名' : 'VPS');

      const doneMessages = {
        extend: '已续费',
        renew: '已更新到期时间',
        pin: '已置顶',
        unpin: '已取消置顶',
        hide: '已对访客隐藏',
        unhide: '已取消隐藏',
        exclude_cost: '已设为不计入成本',
        include_cost: '已计入成本',
        billing_months: '已更新付费周期',
        delete: '已删除',
      };

      // 对若干小块执行同一个操作(域名与 VPS 分别调用各自的接口),然后局部刷新面板。
      async function run(tiles, action, value = '') {
        let failure = null;
        let succeeded = 0;
        try {
          for (const group of groups) {
            const ids = idsOf(tiles.filter((tile) => tile.kind === group.kind));
            if (!ids.length || (action === 'billing_months' && group.kind !== 'vps')) continue;
            await api(`/api/${group.kind}/batch`, { ids, action, value });
            succeeded += 1;
          }
        } catch (error) {
          failure = error;
        }
        if (failure && succeeded === 0) {
          toast(failure.message, 'error'); // 什么都没改(例如登录已失效),不必刷新,保留页面让用户看到提示
          return;
        }
        // 走到这里说明至少有一组成功了(域名成功、VPS 失败也算),刷新让页面反映真实状态
        try {
          await refreshBoard(new Set(tiles.map(keyOf)));
        } catch {
          reloadKeepingPosition(); // 局部刷新不成功时退回整页刷新
          return;
        }
        if (failure) {
          toast(failure.message, 'error');
        } else {
          toast(tiles.length > 1 ? `${doneMessages[action]} ${tiles.length} 项` : doneMessages[action]);
        }
      }

      async function confirmDelete(tiles) {
        const what =
          tiles.length === 1 ? `${kindLabel(tiles[0])}“${tiles[0].dataset.title}”` : `选中的 ${tiles.length} 项`;
        return confirmDialog(`确定删除${what}吗？此操作无法撤销。`, { okText: '删除', danger: true });
      }

      // --- 点击小块:普通模式弹出管理面板,批量模式切换选中 ---

      board.addEventListener('click', (event) => {
        const tile = event.target.closest('.tile[role="button"]');
        if (!tile) return;
        if (!selecting) {
          openSheet(tile);
          return;
        }
        if (selected.has(tile)) selected.delete(tile);
        else selected.add(tile);
        renderSelection();
      });
      board.addEventListener('keydown', (event) => {
        if ((event.key === 'Enter' || event.key === ' ') && event.target.matches('.tile[role="button"]')) {
          event.preventDefault();
          event.target.click();
        }
      });

      // --- 管理面板 ---

      const sheet = $('#itemSheet');
      let current = null; // 面板当前对应的小块

      function openSheet(tile) {
        current = tile;
        const data = tile.dataset;

        // 上半部分直接复制小块的内容,保证与列表里看到的一致。
        $('[data-role="info"]', sheet).replaceChildren(...Array.from(tile.children, (node) => node.cloneNode(true)));

        // 续费:常用时长一键完成,其余走自定义对话框。
        const permanent = data.expires === PERMANENT;
        const cycle = Number(data.months) || 12;
        const choices = permanent ? [] : tile.kind === 'domain' ? [12, 24, 36, 60] : [...new Set([cycle, 12])];
        const buttons = choices.map((months) => {
          const button = document.createElement('button');
          button.type = 'button';
          button.className = 'btn btn-sm';
          button.dataset.extend = String(months);
          button.textContent = `+${periodLabel(months)}`;
          return button;
        });
        const custom = document.createElement('button');
        custom.type = 'button';
        custom.className = 'btn btn-sm';
        custom.dataset.role = 'custom-renew';
        custom.textContent = permanent ? '设置到期日…' : '其他…';
        $('[data-role="renew-options"]', sheet).replaceChildren(...buttons, custom);

        $('[data-role="edit"]', sheet).href = `/${tile.kind === 'domain' ? 'domains' : 'vps'}/${data.id}/edit`;
        const visit = $('[data-role="visit"]', sheet);
        visit.hidden = !data.url;
        visit.href = data.url || '#';
        const copy = $('[data-role="copy"]', sheet);
        const copyValue = tile.kind === 'domain' ? data.title : data.copy;
        copy.hidden = !copyValue;
        copy.dataset.copy = copyValue || '';
        copy.textContent = tile.kind === 'domain' ? '复制域名' : '复制 IP';

        $('[data-item-action="pin"]', sheet).textContent = data.pinned ? '取消置顶' : '置顶';
        $('[data-item-action="hide"]', sheet).textContent = data.hidden ? '取消隐藏' : '对访客隐藏';
        $('[data-item-action="cost"]', sheet).textContent = data.excluded ? '计入成本' : '不计成本';

        sheet.showModal();
      }

      sheet.addEventListener('click', async (event) => {
        const button = event.target.closest('button');
        const tile = current;
        if (!button || !tile) return;
        const data = tile.dataset;

        if (button.dataset.extend) {
          sheet.close();
          run([tile], 'extend', button.dataset.extend);
          return;
        }
        if (button.dataset.role === 'custom-renew') {
          sheet.close();
          const choice = await renewal.ask({
            title: `${kindLabel(tile)} ${data.title}`,
            expires: data.expires,
            months: Number(data.months) || 12,
          });
          if (choice) run([tile], choice.action, choice.value);
          return;
        }
        switch (button.dataset.itemAction) {
          case 'pin':
            sheet.close();
            run([tile], data.pinned ? 'unpin' : 'pin');
            break;
          case 'hide':
            sheet.close();
            run([tile], data.hidden ? 'unhide' : 'hide');
            break;
          case 'cost':
            sheet.close();
            run([tile], data.excluded ? 'include_cost' : 'exclude_cost');
            break;
          case 'delete':
            sheet.close();
            if (await confirmDelete([tile])) run([tile], 'delete');
            break;
        }
      });

      // --- 批量模式 ---

      function setSelecting(on) {
        selecting = on;
        if (!on) selected.clear();
        document.documentElement.toggleAttribute('data-selecting', on);
        el.selectToggle.setAttribute('aria-pressed', String(on));
        el.selectToggle.textContent = on ? '退出批量' : '批量';
        el.selectionBar.hidden = !on;
        render();
      }

      el.selectToggle.addEventListener('click', () => setSelecting(!selecting));

      el.selectionBar.addEventListener('click', async (event) => {
        const button = event.target.closest('button');
        if (!button) return;

        if (button.dataset.select === 'done') {
          setSelecting(false);
          return;
        }
        if (button.dataset.select === 'all') {
          groups.forEach((group) => group.matched.forEach((tile) => selected.add(tile)));
          renderSelection();
          return;
        }

        const action = button.dataset.batch;
        const targets = allTiles.filter((tile) => selected.has(tile));
        if (!action || !targets.length) return;

        if (action === 'renew') {
          const choice = await renewal.ask({ title: `已选中的 ${targets.length} 项` });
          if (choice) run(targets, choice.action, choice.value);
          return;
        }
        if (action === 'billing_months') {
          const months = await askBillingMonths();
          if (months) run(targets, action, String(months));
          return;
        }
        if (action === 'delete' && !(await confirmDelete(targets))) return;
        run(targets, action);
      });

      // --- 保存顺序 ---

      async function saveOrder(successMessage) {
        try {
          for (const group of groups) {
            if (group.ordered.length) await api(`/api/${group.kind}/order`, { ids: idsOf(group.ordered) });
          }
          toast(successMessage);
          return true;
        } catch (error) {
          toast(`保存顺序失败：${error.message}`, 'error');
          return false;
        }
      }

      // "设为默认顺序":把当前的排序结果保存下来。
      el.saveOrder.addEventListener('click', async () => {
        const question = '把当前的排序结果保存为默认顺序吗？之前手动拖拽出的顺序会被覆盖。';
        if (!(await confirmDialog(question))) return;
        if (!(await saveOrder('已设为默认顺序'))) return;
        groups.forEach((group) => {
          group.ordered.forEach((tile, index) => {
            tile.manualIndex = index;
          });
        });
        state.sort = 'manual';
        el.sort.value = 'manual';
        reorder();
        render();
      });

      // 拖拽排序(仅在默认顺序下可用;置顶与未置顶的记录各自排序,不能互相穿插)。
      groups.forEach((group) => {
        if (!group.list) return;
        let dragging = null;
        let orderBefore = '';
        const domTiles = () => $$('.tile', group.list);

        group.list.addEventListener('dragstart', (event) => {
          const tile = event.target.closest('.tile');
          if (!tile || !tile.draggable) return;
          dragging = tile;
          orderBefore = idsOf(domTiles()).join(',');
          tile.classList.add('is-dragging');
          event.dataTransfer.effectAllowed = 'move';
          event.dataTransfer.setData('text/plain', tile.dataset.title || '');
        });

        group.list.addEventListener('dragover', (event) => {
          if (!dragging) return;
          const target = event.target.closest('.tile');
          if (!target || target === dragging || target.dataset.pinned !== dragging.dataset.pinned) return;
          event.preventDefault();
          // 同一排的小块按左右半区判断,不同排的按上下半区判断。
          const box = target.getBoundingClientRect();
          const sameRow = Math.abs(box.top - dragging.getBoundingClientRect().top) < box.height / 2;
          const after = sameRow
            ? event.clientX > box.left + box.width / 2
            : event.clientY > box.top + box.height / 2;
          group.list.insertBefore(dragging, after ? target.nextSibling : target);
        });

        group.list.addEventListener('drop', (event) => {
          if (dragging) event.preventDefault();
        });

        group.list.addEventListener('dragend', async () => {
          if (!dragging) return;
          dragging.classList.remove('is-dragging');
          dragging = null;

          const current = domTiles();
          if (idsOf(current).join(',') === orderBefore) return;
          current.forEach((tile, index) => {
            tile.manualIndex = index;
          });
          reorder();
          render();
          await saveOrder('顺序已保存');
        });
      });

      // --- 导出域名:按所选范围把 ID 列表填进表单,再由浏览器正常提交并下载文件 ---

      const exportDialog = $('#exportDialog');
      const exportForm = $('form', exportDialog);
      $('[data-action="export"]', board).addEventListener('click', () => exportDialog.showModal());
      exportForm.addEventListener('submit', () => {
        const fields = exportForm.elements;
        const domains = groups.find((group) => group.kind === 'domain');
        const range = fields.range.value;
        if (range === 'all' || !domains) {
          fields.scope.value = 'all';
          fields.ids.value = '';
        } else {
          const tiles = range === 'selected' ? domains.ordered.filter((tile) => selected.has(tile)) : domains.matched;
          fields.scope.value = 'ids';
          fields.ids.value = idsOf(tiles).join(',');
        }
        exportDialog.close();
      });
    }
  }

  // ---------- 表单页 ----------

  function initAssetForm() {
    // "永久有效"勾选后不需要日期
    const permanent = $('[data-role="permanent"]');
    const date = $('#expires_at');
    if (permanent && date) {
      const sync = () => {
        date.disabled = permanent.checked;
      };
      permanent.addEventListener('change', sync);
      sync();
    }

    // 硬盘行的增删
    const diskRows = $('[data-role="disk-rows"]');
    const template = $('#diskRowTemplate');
    if (diskRows && template) {
      const maxDisks = 16;
      document.addEventListener('click', (event) => {
        if (event.target.closest('[data-action="add-disk"]')) {
          if (diskRows.children.length >= maxDisks) {
            toast(`最多支持 ${maxDisks} 块硬盘`, 'error');
            return;
          }
          diskRows.appendChild(template.content.cloneNode(true));
          $('input', diskRows.lastElementChild).focus();
          return;
        }
        const remove = event.target.closest('[data-action="remove-disk"]');
        if (!remove) return;
        const row = remove.closest('.disk-row');
        if (diskRows.children.length > 1) {
          row.remove();
        } else {
          // 只剩一行时清空内容,保留输入框
          $$('input, select', row).forEach((field) => {
            field.value = '';
          });
        }
      });
    }

    // 编辑页上的"删除"按钮
    const deleteButton = $('[data-delete-asset]');
    if (deleteButton) {
      deleteButton.addEventListener('click', async () => {
        const kind = deleteButton.dataset.deleteAsset;
        const message = `确定删除“${deleteButton.dataset.title}”吗？此操作无法撤销。`;
        if (!(await confirmDialog(message, { okText: '删除', danger: true }))) return;
        try {
          await api(`/api/${kind}/batch`, { ids: [Number(deleteButton.dataset.id)], action: 'delete', value: '' });
          window.location.href = `/#${kind}`;
        } catch (error) {
          toast(error.message, 'error');
        }
      });
    }
  }

  // ---------- 设置页 ----------

  function initSettings() {
    const root = $('[data-role="settings"]');
    if (!root) return;
    const tabs = $$('[data-settings-tab]', root);
    const panels = $$('[data-settings-panel]', root);
    const names = panels.map((panel) => panel.dataset.settingsPanel);

    const activate = (name) => {
      tabs.forEach((tab) => tab.classList.toggle('is-active', tab.dataset.settingsTab === name));
      panels.forEach((panel) => panel.classList.toggle('is-active', panel.dataset.settingsPanel === name));
      session.set('mb.settings', name);
    };
    tabs.forEach((tab) => {
      tab.addEventListener('click', (event) => {
        event.preventDefault();
        activate(tab.dataset.settingsTab);
        history.replaceState(null, '', `#${tab.dataset.settingsTab}`);
      });
    });
    window.addEventListener('hashchange', () => {
      const name = window.location.hash.slice(1);
      if (names.includes(name)) activate(name);
    });

    // 初始分类:服务端指定的(表单出错时)> 地址里的锚点 > 上次停留的 > 第一个。
    const candidates = [root.dataset.initialPanel, window.location.hash.slice(1), session.get('mb.settings'), names[0]];
    activate(candidates.find((name) => names.includes(name)));
  }

  // ---------- 启动 ----------

  initTheme();
  initGlobalActions();
  initBoard();
  initAssetForm();
  initSettings();
})();
