(() => {
  'use strict';

  const STORAGE_KEY = 'pet-shop-prototype-v2';
  const byId = id => document.getElementById(id);
  const all = selector => [...document.querySelectorAll(selector)];
  const money = cents => `¥${(cents / 100).toFixed(2)}`;
  const now = () => Date.now();

  const initialState = () => ({
    version: 2,
    pending: { status: 'pending', expiresAt: now() + 5 * 60 * 1000, paidCents: 0 },
    service: { status: 'confirmed', updatedAt: null, cancelReason: '', refundStatus: 'none' },
    review003: { status: 'pending' },
    review007: { status: 'pending' },
    refund: { status: 'failed', attempts: 1 },
    noShow: { marked: false, reason: '' },
    extraSlots: [],
    latePayment: { status: 'none' },
    events: [event('原型初始化', '已载入可操作演示数据')]
  });

  function event(action, detail) {
    return { at: new Date().toISOString(), action, detail };
  }

  function load() {
    try {
      const parsed = JSON.parse(localStorage.getItem(STORAGE_KEY));
      if (parsed && parsed.version === 2) return parsed;
    } catch (_) {}
    return initialState();
  }

  let state = load();
  localStorage.setItem(STORAGE_KEY, JSON.stringify(state));

  function save(action, detail) {
    if (action) state.events.unshift(event(action, detail));
    state.events = state.events.slice(0, 30);
    localStorage.setItem(STORAGE_KEY, JSON.stringify(state));
    render();
  }

  function showToast(text) {
    const el = byId('toast');
    el.textContent = text;
    el.classList.add('show');
    clearTimeout(window.prototypeToastTimer);
    window.prototypeToastTimer = setTimeout(() => el.classList.remove('show'), 2800);
  }

  function formatRemaining(ms) {
    if (ms <= 0) return '00:00';
    const total = Math.ceil(ms / 1000);
    return `${String(Math.floor(total / 60)).padStart(2, '0')}:${String(total % 60).padStart(2, '0')}`;
  }

  function ensureRuntimeControls() {
    if (!byId('prototypeReset')) {
      const tag = document.querySelector('.prototype-tag');
      const wrapper = document.createElement('div');
      wrapper.className = 'row';
      tag.parentNode.replaceChild(wrapper, tag);
      wrapper.appendChild(tag);
      const reset = document.createElement('button');
      reset.id = 'prototypeReset';
      reset.className = 'scenario';
      reset.textContent = '重置演示数据';
      reset.title = '清除浏览器本地原型数据并恢复初始状态';
      wrapper.appendChild(reset);
    }

    if (!byId('localSaveNotice')) {
      const el = document.createElement('span');
      el.id = 'localSaveNotice';
      el.textContent = '数据已保存在本浏览器';
      el.style.cssText = 'color:#d7eadf;font-size:12px;margin-left:8px';
      document.querySelector('header p').appendChild(el);
    }

    const ownerRows = document.querySelectorAll('#owner tbody tr');
    if (ownerRows[1] && !ownerRows[1].id) ownerRows[1].id = 'ownerServiceBooking';
    const reviewRows = document.querySelectorAll('#support .two-third tbody tr');
    if (reviewRows[1] && !reviewRows[1].id) reviewRows[1].id = 'reviewRow007';

    if (!byId('noShowModal')) {
      document.body.insertAdjacentHTML('beforeend', `
        <div id="noShowModal" class="modal" role="dialog" aria-modal="true">
          <div class="dialog">
            <h3>标记未到店</h3>
            <p class="alert">仅时段结束后仍为已确认且未核销的预约可以标记。未到店不自动退款。</p>
            <label>未到店原因<textarea id="noShowReason" placeholder="例如：时段结束后仍未到店，电话联系未接通"></textarea></label>
            <div class="row" style="justify-content:flex-end;margin-top:16px">
              <button class="btn secondary runtime-close-modal">返回</button>
              <button id="confirmNoShow" class="btn warn">确认标记</button>
            </div>
          </div>
        </div>`);
    }

    if (!byId('latePaymentCard')) {
      const supportGrid = document.querySelector('#support .grid');
      supportGrid.insertAdjacentHTML('beforeend', `
        <article id="latePaymentCard" class="card">
          <div class="row between"><h3>关闭后晚到支付异常</h3><span id="latePaymentBadge" class="chip gray">尚未触发</span></div>
          <p>先在用户端取消待支付预约，再模拟渠道送达支付成功通知。系统必须登记异常并安排原路退回，不能恢复预约。</p>
          <div class="row">
            <button id="simulateLatePayment" class="btn warn">模拟晚到支付成功通知</button>
            <span id="latePaymentResult" class="muted">等待演示</span>
          </div>
        </article>`);
    }

    if (!byId('eventLogCard')) {
      const managerGrid = document.querySelector('#manager .grid');
      managerGrid.insertAdjacentHTML('beforeend', `
        <article id="eventLogCard" class="card">
          <div class="row between"><h3>共享业务记录与操作轨迹</h3><span class="chip blue">浏览器本地持久化</span></div>
          <table><thead><tr><th>时间</th><th>操作</th><th>结果</th></tr></thead><tbody id="eventLogBody"></tbody></table>
        </article>`);
    }

    if (!byId('detailModal')) {
      document.body.insertAdjacentHTML('beforeend', `
        <div id="detailModal" class="modal" role="dialog" aria-modal="true">
          <div class="dialog">
            <div class="row between"><h3 id="detailTitle">业务详情</h3><button class="btn secondary runtime-close-modal">关闭</button></div>
            <div id="detailBody"></div>
          </div>
        </div>`);
    }
  }

  function renderPending() {
    const status = byId('pendingStatus');
    const pay = byId('payBtn');
    const cancel = byId('cancelUnpaid');
    const moneyCell = byId('pendingMoney');
    if (!status || !pay || !cancel) return;

    if (state.pending.status === 'pending' && state.pending.expiresAt <= now()) {
      state.pending.status = 'expired';
      state.events.unshift(event('占位自动超时', 'BK20261115001 已关闭，名额释放一次'));
      localStorage.setItem(STORAGE_KEY, JSON.stringify(state));
    }

    const map = {
      pending: ['chip orange', `待支付 ${formatRemaining(state.pending.expiresAt - now())}`, '待支付 ¥99.00'],
      paid: ['chip', '已确认', '已付款 ¥99.00'],
      cancelled: ['chip gray', '已关闭', '未付款'],
      expired: ['chip gray', '占位超时', '未付款'],
      late_paid: ['chip red', '异常支付待退回', '晚到收款 ¥99.00']
    };
    const current = map[state.pending.status];
    status.className = current[0];
    status.textContent = current[1];
    moneyCell.textContent = current[2];
    pay.disabled = state.pending.status !== 'pending';
    cancel.disabled = state.pending.status !== 'pending';
    pay.textContent = state.pending.status === 'paid' ? '支付成功' : '立即支付';
  }

  function renderService() {
    const deskStatus = byId('deskStatus');
    const checkin = byId('checkinBtn');
    const start = byId('startService');
    const finish = byId('finishService');
    const badge = byId('serviceStatus');
    const timeline = byId('serviceTimeline');
    const ownerRow = byId('ownerServiceBooking');
    if (!deskStatus) return;

    const labels = {
      confirmed: ['chip', '已确认'], cancelled: ['chip gray', '已取消'], checked_in: ['chip blue', '已核销'],
      in_service: ['chip blue', '服务中'], completed: ['chip blue', '已完成']
    };
    const item = labels[state.service.status];
    deskStatus.innerHTML = `<span class="${item[0]}">${item[1]}</span>`;
    badge.className = item[0];
    badge.textContent = item[1];
    checkin.disabled = state.service.status !== 'confirmed';
    checkin.textContent = state.service.status === 'confirmed' ? '核销到店' : (state.service.status === 'cancelled' ? '已取消，禁止核销' : '已核销');
    start.disabled = state.service.status !== 'checked_in';
    finish.disabled = state.service.status !== 'in_service';
    timeline.innerHTML = '<li><b>预约已确认</b><small>支付成功，等待到店</small></li>';
    if (['checked_in','in_service','completed'].includes(state.service.status)) timeline.insertAdjacentHTML('beforeend','<li><b>前台已核销</b><small>本店身份及预约状态校验通过</small></li>');
    if (['in_service','completed'].includes(state.service.status)) timeline.insertAdjacentHTML('beforeend','<li><b>服务已开始</b><small>服务人员：程安</small></li>');
    if (state.service.status === 'completed') timeline.insertAdjacentHTML('beforeend','<li><b>服务已完成</b><small>完成收入 ¥99.00，仅确认一次</small></li>');
    if (state.service.status === 'cancelled') timeline.insertAdjacentHTML('beforeend','<li><b>预约已取消</b><small>名额释放，前台不可核销</small></li>');
    if (ownerRow) {
      const cells = ownerRow.children;
      if (state.service.status === 'cancelled') {
        cells[1].innerHTML = '<span class="chip gray">已取消</span>';
        cells[2].textContent = state.service.refundStatus === 'processing' ? '退款处理中 ¥99.00' : '已付款 ¥99.00';
        cells[3].textContent = '禁止核销';
        cells[4].innerHTML = '<button class="btn secondary view-service-detail">查看取消结果</button>';
      } else if (state.service.status === 'confirmed') {
        cells[1].innerHTML = '<span class="chip">已确认</span>';
        cells[2].textContent = '已付款 ¥99.00';
        cells[3].textContent = '未到店';
      } else {
        cells[1].innerHTML = `<span class="chip blue">${item[1]}</span>`;
        cells[3].textContent = item[1];
        cells[4].innerHTML = '<button class="btn danger attempt-cancel-after-checkin">再次尝试取消</button> <button class="btn secondary view-service-detail">查看履约</button>';
      }
    }
  }

  function renderReview(id, review) {
    const row = byId(id === '003' ? 'reviewRow' : 'reviewRow007');
    if (!row || review.status === 'pending') return;
    const text = review.status === 'approved'
      ? '<span class="chip">已批准取消</span> 名额已释放，已发起全额退款；审核历史保留。'
      : '<span class="chip red">已拒绝</span> 预约恢复为已确认，可由前台核销；原因已记录。';
    row.innerHTML = `<td colspan="5">${text}</td>`;
  }

  function renderRefund() {
    const cell = byId('refundState');
    const button = byId('retryRefund');
    if (!cell || !button) return;
    if (state.refund.status === 'success') {
      cell.innerHTML = `<span class="chip">退款成功</span><br><span class="muted">第 ${state.refund.attempts} 次尝试，历史保留</span>`;
      button.textContent = '已退款';
      button.disabled = true;
    } else {
      cell.innerHTML = `<span class="chip red">退款失败</span><br><span class="muted">CHANNEL_BUSY · 已尝试 ${state.refund.attempts} 次</span>`;
      button.textContent = '重试退款';
      button.disabled = false;
    }
  }

  function renderNoShow() {
    const row = byId('noShowRow');
    if (!row) return;
    if (state.noShow.marked) {
      row.innerHTML = `<td>BK...008</td><td><span class="chip gray">已标记未到店</span><br><span class="muted">${escapeHtml(state.noShow.reason)}</span></td><td class="right">不自动退款</td>`;
    }
  }

  function renderLatePayment() {
    const badge = byId('latePaymentBadge');
    const result = byId('latePaymentResult');
    const button = byId('simulateLatePayment');
    if (!badge) return;
    if (state.latePayment.status === 'refund_required') {
      badge.className = 'chip red';
      badge.textContent = '异常成功待退回';
      result.textContent = '预约保持关闭；已创建 ¥99.00 异常退款任务。';
      button.disabled = true;
    } else {
      badge.className = 'chip gray';
      badge.textContent = '尚未触发';
      result.textContent = state.pending.status === 'pending' ? '请先取消用户端待支付预约' : '可模拟渠道通知';
      button.disabled = !['cancelled','expired'].includes(state.pending.status);
    }
  }

  function renderSlots() {
    const list = document.querySelector('.slot-list');
    all('.slot.runtime-slot').forEach(x => x.remove());
    state.extraSlots.forEach(slot => {
      const node = document.createElement('div');
      node.className = 'slot runtime-slot';
      node.dataset.slot = '';
      node.innerHTML = `<strong>${escapeHtml(slot.start)}–${escapeHtml(slot.end)}</strong><span class="muted">剩余 3 / 3</span><div class="capacity"><i style="width:0"></i></div><button class="btn secondary pick-slot">选择</button>`;
      list.appendChild(node);
    });
  }

  function renderEvents() {
    const body = byId('eventLogBody');
    if (!body) return;
    body.innerHTML = state.events.slice(0, 10).map(item => {
      const time = new Date(item.at).toLocaleString('zh-CN', { hour12: false });
      return `<tr><td>${escapeHtml(time)}</td><td>${escapeHtml(item.action)}</td><td>${escapeHtml(item.detail)}</td></tr>`;
    }).join('');
  }

  function renderMetrics() {
    const values = document.querySelectorAll('#manager .metric b');
    if (values.length < 4) return;
    let received = 59400;
    let refunded = state.refund.status === 'success' ? 19800 : 9900;
    let pendingRefund = state.refund.status === 'success' ? 0 : 9900;
    if (state.latePayment.status === 'refund_required') { received += 9900; pendingRefund += 9900; }
    const completed = state.service.status === 'completed' ? 39600 : 29700;
    values[0].textContent = money(received);
    values[1].textContent = money(refunded);
    values[2].textContent = money(pendingRefund);
    values[3].textContent = money(completed);
  }

  function escapeHtml(text) {
    const div = document.createElement('div');
    div.textContent = String(text ?? '');
    return div.innerHTML;
  }

  function showDetail(title, body) {
    byId('detailTitle').textContent = title;
    byId('detailBody').innerHTML = body;
    byId('detailModal').classList.add('show');
  }

  function serviceDetailHtml() {
    const statusNames = { confirmed: '已确认', cancelled: '已取消', checked_in: '已核销', in_service: '服务中', completed: '已完成' };
    const refund = state.service.refundStatus === 'processing' ? '退款处理中 ¥99.00' : '无退款';
    return `<dl>
      <dt>预约号</dt><dd>BK20261115002</dd>
      <dt>业务状态</dt><dd><span class="chip blue">${statusNames[state.service.status]}</span></dd>
      <dt>资金结果</dt><dd>${refund}</dd>
      <dt>取消原因</dt><dd>${state.service.cancelReason ? escapeHtml(state.service.cancelReason) : '—'}</dd>
    </dl><h3 style="margin-top:18px">相关操作</h3><ul class="timeline">${state.events.filter(x => /取消|核销|服务/.test(x.action)).slice(0,8).map(x => `<li><b>${escapeHtml(x.action)}</b><small>${escapeHtml(x.detail)}</small></li>`).join('') || '<li><b>暂无操作</b></li>'}</ul>`;
  }

  function exportClosingCsv() {
    const rows = [
      ['预约号','业务结果','已收金额(分)','已退金额(分)','未退金额(分)','完成收入(分)'],
      ['BK20261115002', state.service.status === 'completed' ? '已完成' : state.service.status, '9900', state.service.refundStatus === 'success' ? '9900' : '0', state.service.refundStatus === 'processing' ? '9900' : '0', state.service.status === 'completed' ? '9900' : '0'],
      ['BK20261115004','已取消','9900','9900','0','0'],
      ['BK20261115005','已取消','9900', state.refund.status === 'success' ? '9900' : '0', state.refund.status === 'success' ? '0' : '9900','0'],
      ['BK20261115008', state.noShow.marked ? '未到店' : '待收尾','9900','0','0','0']
    ];
    const csv = '\ufeff' + rows.map(row => row.map(value => `"${String(value).replaceAll('"','""')}"`).join(',')).join('\r\n');
    const url = URL.createObjectURL(new Blob([csv], { type: 'text/csv;charset=utf-8' }));
    const link = document.createElement('a');
    link.href = url;
    link.download = '门店日清核查清单.csv';
    document.body.appendChild(link);
    link.click();
    link.remove();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
    save('导出日清清单', '已生成门店日清核查清单.csv');
    showToast('日清核查清单已生成并下载。');
  }

  function render() {
    ensureRuntimeControls();
    renderPending();
    renderService();
    renderReview('003', state.review003);
    renderReview('007', state.review007);
    renderRefund();
    renderNoShow();
    renderLatePayment();
    renderSlots();
    renderEvents();
    renderMetrics();
  }

  document.addEventListener('click', eventObject => {
    const target = eventObject.target.closest('button');
    if (!target) return;

    const controlled = ['prototypeReset','createBooking','payBtn','cancelUnpaid','confirmCancel','checkinBtn','startService','finishService','retryRefund','markNoShow','confirmNoShow','confirmSlot','simulateLatePayment','viewCancelProgress','viewCompletedDetail','deskQueryBtn','viewIdempotency','exportClosing'];
    if (controlled.includes(target.id) || target.classList.contains('approve') || target.classList.contains('reject') || target.classList.contains('runtime-close-modal') || target.classList.contains('attempt-cancel-after-checkin')) {
      eventObject.preventDefault();
      eventObject.stopImmediatePropagation();
    }

    if (target.id === 'prototypeReset') {
      if (!confirm('确定清除当前原型操作记录并恢复初始演示数据吗？')) return;
      state = initialState();
      localStorage.setItem(STORAGE_KEY, JSON.stringify(state));
      location.reload();
    }

    if (target.id === 'createBooking') {
      if (state.pending.status === 'pending') return showToast('已有一条待支付预约，请先付款、取消或等待超时。');
      state.pending = { status: 'pending', expiresAt: now() + 5 * 60 * 1000, paidCents: 0 };
      save('创建预约', '为团子创建预约，名额占用 5 分钟');
      showToast('预约已创建，倒计时和名额状态已保存。');
    }

    if (target.id === 'payBtn' && state.pending.status === 'pending') {
      state.pending.status = 'paid';
      state.pending.paidCents = 9900;
      save('支付成功', 'BK20261115001 已确认，成功收款 ¥99.00');
      showToast('付款成功，刷新页面后结果仍保留。');
    }

    if (target.id === 'cancelUnpaid' && state.pending.status === 'pending') {
      state.pending.status = 'cancelled';
      save('取消待支付预约', '预约关闭，名额释放一次');
      showToast('预约已关闭，可到客服页演示晚到支付。');
    }

    if (target.id === 'confirmCancel') {
      const reason = byId('cancelReason').value.trim();
      if (!reason) return showToast('必须填写取消原因。');
      if (state.service.status !== 'confirmed') {
        byId('cancelModal').classList.remove('show');
        return showToast('预约已核销或进入服务阶段，不能再取消。');
      }
      state.service.status = 'cancelled';
      state.service.cancelReason = reason;
      state.service.refundStatus = 'processing';
      byId('cancelModal').classList.remove('show');
      save('提前取消预约', `BK20261115002：${reason}；名额释放并发起全额退款`);
      showToast('取消已生效，前台现已无法核销。');
    }

    if (target.id === 'checkinBtn') {
      if (state.service.status !== 'confirmed') return showToast('当前状态不允许核销。');
      state.service.status = 'checked_in';
      save('前台核销', 'BK20261115002 核销成功，后续取消将被拒绝');
      showToast('核销成功。');
    }

    if (target.id === 'startService') {
      if (state.service.status !== 'checked_in') return showToast('必须先完成核销。');
      state.service.status = 'in_service';
      save('服务开始', 'BK20261115002 进入服务中');
      showToast('服务开始已登记。');
    }

    if (target.id === 'finishService') {
      if (state.service.status !== 'in_service') return showToast('当前状态不能登记完成。');
      state.service.status = 'completed';
      save('服务完成', 'BK20261115002 完成收入确认 ¥99.00');
      showToast('服务完成，日清完成收入已联动。');
    }

    if (target.classList.contains('approve') || target.classList.contains('reject')) {
      const row = target.closest('tr');
      const key = row.id === 'reviewRow' ? 'review003' : 'review007';
      state[key].status = target.classList.contains('approve') ? 'approved' : 'rejected';
      save('客服审核取消', `${key === 'review003' ? 'BK...003' : 'BK...007'} ${state[key].status === 'approved' ? '批准' : '拒绝'}`);
      showToast(state[key].status === 'approved' ? '取消已生效并发起退款。' : '申请已拒绝，预约仍有效。');
    }

    if (target.id === 'retryRefund' && state.refund.status === 'failed') {
      state.refund.status = 'success';
      state.refund.attempts += 1;
      save('退款重试成功', `RF20261115001 第 ${state.refund.attempts} 次尝试成功`);
      showToast('退款成功，取消状态没有回滚。');
    }

    if (target.id === 'markNoShow') {
      byId('noShowReason').value = '';
      byId('noShowModal').classList.add('show');
    }

    if (target.classList.contains('runtime-close-modal')) target.closest('.modal').classList.remove('show');

    if (target.id === 'confirmNoShow') {
      const reason = byId('noShowReason').value.trim();
      if (!reason) return showToast('必须填写未到店原因。');
      state.noShow = { marked: true, reason };
      byId('noShowModal').classList.remove('show');
      save('标记未到店', `BK...008：${reason}`);
      showToast('未到店结果已保存，不自动退款。');
    }

    if (target.id === 'confirmSlot') {
      const inputs = all('#slotModal input');
      const start = new Date(inputs[0].value);
      const end = new Date(inputs[1].value);
      if (!inputs[0].value || !inputs[1].value || end <= start) return showToast('结束时间必须晚于开始时间。');
      const display = value => value.replace('T', ' ');
      state.extraSlots.push({ start: display(inputs[0].value), end: display(inputs[1].value) });
      byId('slotModal').classList.remove('show');
      save('发布时段', `${display(inputs[0].value)} 至 ${display(inputs[1].value)}，容量 3`);
      showToast('新时段已保存，并显示在用户预约页。');
    }

    if (target.id === 'simulateLatePayment') {
      if (!['cancelled','expired'].includes(state.pending.status)) return showToast('请先让待支付预约关闭或超时。');
      state.pending.status = 'late_paid';
      state.pending.paidCents = 9900;
      state.latePayment.status = 'refund_required';
      save('晚到支付异常', '关闭后收到支付成功：预约不恢复，登记 ¥99.00 异常退款');
      showToast('异常已登记，预约没有恢复。');
    }

    if (target.id === 'deskQueryBtn') {
      const keyword = byId('deskQuery').value.trim().toLowerCase();
      const rows = [...document.querySelectorAll('#desk .two-third tbody tr')];
      let matched = 0;
      rows.forEach(row => {
        const hit = !keyword || row.textContent.toLowerCase().includes(keyword) || (keyword.endsWith('002') && row === rows[0]);
        row.style.display = hit ? '' : 'none';
        if (hit) matched += 1;
      });
      const result = byId('deskQueryResult');
      result.className = matched ? 'alert green' : 'alert red';
      result.textContent = matched ? `找到 ${matched} 条本店接待记录，状态已显示在右侧列表。` : '未找到本店可查看的预约。';
    }

    if (target.id === 'viewCancelProgress') {
      const review = state.review003.status;
      const result = review === 'pending' ? '客服待审核，仍占名额且禁止核销' : (review === 'approved' ? '客服已批准，已发起全额退款' : '客服已拒绝，预约恢复有效');
      showDetail('BK20261115003 取消进度', `<ul class="timeline"><li><b>用户提交临近取消</b><small>原因：宠物临时不适</small></li><li><b>${result}</b><small>当前结果来自客服共享记录</small></li></ul>`);
    }

    if (target.id === 'viewCompletedDetail') {
      showDetail('BK20261114009 履约详情', '<ul class="timeline"><li><b>15:51 前台核销</b><small>操作人：周宁</small></li><li><b>15:55 服务开始</b><small>服务人员：程安</small></li><li><b>17:02 服务完成</b><small>完成收入 ¥99.00</small></li></ul>');
    }

    if (target.id === 'viewIdempotency') {
      showDetail('BK20261115002 操作记录', serviceDetailHtml());
    }

    if (target.classList.contains('view-service-detail')) showDetail('BK20261115002 业务详情', serviceDetailHtml());

    if (target.classList.contains('attempt-cancel-after-checkin')) {
      state.events.unshift(event('取消请求被拒绝', `BK20261115002 当前状态为 ${state.service.status}，不能进入取消退款流程`));
      localStorage.setItem(STORAGE_KEY, JSON.stringify(state));
      renderEvents();
      showDetail('取消请求已拒绝', `<div class="alert red">预约已经${state.service.status === 'checked_in' ? '核销到店' : '进入履约阶段'}，不能再走本题取消退款流程。</div><p>预约状态保持不变，资金和名额记录没有被修改；本次拒绝已写入操作轨迹。</p>`);
    }

    if (target.id === 'exportClosing') exportClosingCsv();
  }, true);

  document.addEventListener('click', eventObject => {
    const pick = eventObject.target.closest('.runtime-slot .pick-slot');
    if (!pick) return;
    all('[data-slot]').forEach(x => x.classList.remove('selected'));
    all('.pick-slot').forEach(x => x.textContent = '选择');
    pick.closest('[data-slot]').classList.add('selected');
    pick.textContent = '已选择';
  });

  render();
  setInterval(renderPending, 1000);
})();
