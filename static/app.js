// ============================================================
// State
// ============================================================
const adminKey = new URLSearchParams(location.search).get('key') || 'admin123';
const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
const wsUrl = `${protocol}//${location.host}/ws/dash`;

let ws = null;
let devices = [];
let activeDeviceId = null;
let canvas = document.getElementById('screen-canvas');
let ctx = canvas.getContext('2d');
let img = new Image();
let devW = 1080, devH = 1920;
let offsetX = 0, offsetY = 0;
let gridMode = false;
let deviceCanvases = {}; // device_id -> {canvas, ctx, img, devW, devH}

// ============================================================
// Init
// ============================================================
(async function() {
  try {
    var resp = await fetch('/api/auth-check');
    if (!resp.ok) { location.href = '/login.html'; return; }
  } catch(e) { location.href = '/login.html'; return; }
  connect();
})();

// ============================================================
// WebSocket
// ============================================================
function connect() {
  updateStatus('connecting', '连接中...');
  ws = new WebSocket(wsUrl);
  ws.binaryType = 'arraybuffer';

  ws.onopen = () => {
    updateStatus('connected', '已连接');
    document.getElementById('conn-dot').style.background = 'var(--online)';
  };

  let pendingFrameHeader = null;

  ws.onmessage = (e) => {
    if (typeof e.data === 'string') {
      const msg = JSON.parse(e.data);
      switch (msg.type) {
        case 'device_list':
          devices = msg.devices || [];
          renderDeviceList();
          updateDeviceStatus();
          if (gridMode) buildGrid();
          break;
        case 'frame':
          pendingFrameHeader = msg;
          break;
        case 'task_status':
          showTaskMsg(msg.text);
          break;
        case 'error':
          alert('错误: ' + msg.reason);
          break;
      }
    } else if (e.data instanceof ArrayBuffer) {
      if (pendingFrameHeader) {
        var fdevId = pendingFrameHeader.device_id;
        // Grid mode: render to device canvas
        if (gridMode && deviceCanvases[fdevId]) {
          var dc = deviceCanvases[fdevId];
          const blob = new Blob([e.data], {type: 'image/jpeg'});
          const url = URL.createObjectURL(blob);
          dc.img.onload = function() {
            dc.canvas.width = dc.img.naturalWidth;
            dc.canvas.height = dc.img.naturalHeight;
            dc.ctx.drawImage(dc.img, 0, 0);
            URL.revokeObjectURL(url);
          };
          dc.img.src = url;
        }
        // Single mode: render to main canvas
        if (!gridMode && fdevId === activeDeviceId) {
        const frameDevId = pendingFrameHeader.device_id;
        const blob = new Blob([e.data], {type: 'image/jpeg'});
        const url = URL.createObjectURL(blob);
        img.onload = () => {
          devW = img.naturalWidth;
          devH = img.naturalHeight;
          window._jpgW = devW; window._jpgH = devH;
          window._calSrc = 'jpg';
          // Canvas 内部坐标系对齐设备分辨率
          canvas.width = devW;
          canvas.height = devH;
          // 保持宽高比适配容器
          var container = document.getElementById('screen-area');
          var scale = Math.min(container.clientWidth / devW, container.clientHeight / devH);
          canvas.style.width = (devW * scale) + 'px';
          canvas.style.height = (devH * scale) + 'px';
          ctx.drawImage(img, 0, 0, devW, devH);
          URL.revokeObjectURL(url);
        };
        img.src = url;
        document.getElementById('screen-placeholder').classList.add('hidden');
        canvas.classList.remove('hidden');
      }
      } // close pendingFrameHeader if
      pendingFrameHeader = null;
    }
  };

  ws.onclose = () => {
    updateStatus('disconnected', '已断开，5s 后重连...');
    document.getElementById('conn-dot').style.background = 'var(--offline)';
    setTimeout(connect, 5000);
  };

  ws.onerror = () => {};
}

function send(msg) {
  if (ws && ws.readyState === WebSocket.OPEN) {
    ws.send(JSON.stringify(msg));
  }
}

function updateStatus(status, text) {
  if (!activeDeviceId) {
    document.getElementById('status-left').textContent = text;
  }
}

// ============================================================
// Device List
// ============================================================
var sortField = 'time', sortAsc = true;

function sortDevices(field) {
  if (sortField === field && field === 'id') {
    sortAsc = !sortAsc;
    document.getElementById('sort-id-dir').textContent = sortAsc ? '▲' : '▼';
  } else {
    sortField = field;
    sortAsc = true;
    document.getElementById('sort-id-dir').textContent = '';
  }
  if (field !== 'id') document.getElementById('sort-id-dir').textContent = '';
  renderDeviceList();
}

function renderDeviceList() {
  var sorted = devices.slice().sort(function(a, b) {
    var va, vb;
    if (sortField === 'id') { va = (a.name||a.id).toLowerCase(); vb = (b.name||b.id).toLowerCase(); }
    else if (sortField === 'model') { va = (a.model||'').toLowerCase(); vb = (b.model||'').toLowerCase(); }
    else { va = a.created_at||''; vb = b.created_at||''; }
    if (va < vb) return sortAsc ? -1 : 1;
    if (va > vb) return sortAsc ? 1 : -1;
    return 0;
  });
  const container = document.getElementById('device-list');
  if (devices.length === 0) {
    container.innerHTML = '<div style="padding:20px;text-align:center;color:var(--text2);font-size:13px;">暂无设备<br>点击下方按钮绑定</div>';
    return;
  }

  container.innerHTML = sorted.map(d => `
    <div class="device-item${d.id === activeDeviceId ? ' active' : ''}" onclick="selectDevice('${d.id}')">
      <div class="dot ${d.status === 'online' ? 'online' : 'offline'}"></div>
      <div class="info">
        <div class="name">${escHtml(d.name || d.id)}${d.status === 'offline' ? ' <span style="color:var(--offline);font-size:11px;">● 离线</span>' : ''}</div>
        <div class="meta">${escHtml(d.model || '')} · ${d.resolution || ''} · 电量 ${d.battery}%</div>
      </div>
      <span onclick="event.stopPropagation();showDeviceSettings('${d.id}')" style="cursor:pointer;opacity:.5;font-size:14px;padding:4px;" title="设备设置">⚙</span>
      <span onclick="event.stopPropagation();deleteDevice('${d.id}')" style="cursor:pointer;opacity:.3;font-size:16px;padding:4px;font-weight:bold;" title="删除设备">×</span>
    </div>
  `).join('');

  // Restore selection state
  if (activeDeviceId) {
    const dev = devices.find(d => d.id === activeDeviceId);
    if (!dev || dev.status === 'offline') {
      document.getElementById('task-msg').style.display = 'block';
      document.getElementById('task-msg').textContent = '⚠️ 设备离线，请在手机上打开 NFTouch 应用即可自动重连';
      document.getElementById('screen-placeholder').classList.remove('hidden');
      canvas.classList.add('hidden');
    } else {
      // 设备在线，隐藏离线提示（保留其他任务消息）
      var el = document.getElementById('task-msg');
      if (el.textContent.indexOf('离线') >= 0) {
        el.style.display = 'none';
      }
    }
  }
}

function escHtml(s) { const d=document.createElement('div'); d.textContent=s; return d.innerHTML; }

function selectDevice(id) {
  activeDeviceId = id;
  // 加载该设备的校准偏移
  var s = JSON.parse(localStorage.getItem('nftouch_cal_' + id) || '{}');
  offsetX = s.x || 0;
  offsetY = s.y || 0;
  send({type: 'watch', device_id: id});
  renderDeviceList();
  updateDeviceStatus();
}

function updateDeviceStatus() {
  const el = document.getElementById('status-left');
  if (!activeDeviceId) {
    el.textContent = document.getElementById('conn-dot').style.background === 'rgb(34, 197, 94)' ? '已连接' : '未连接';
    return;
  }
  const dev = devices.find(d => d.id === activeDeviceId);
  if (!dev) {
    el.textContent = '已连接';
    return;
  }
  if (dev.status === 'online') {
    el.textContent = '已连接 · 设备在线 🟢';
  } else {
    el.textContent = '已连接 · 设备离线 🔴';
  }
  var ci = document.getElementById('cal-info');
  if (offsetX || offsetY) {
    ci.textContent = '校准 ' + offsetX + ',' + offsetY;
  } else {
    ci.textContent = '';
  }
}

// ============================================================
// Screen Canvas Events
// ============================================================
let touchStart = null;
let touchStartTime = 0;

canvas.addEventListener('mousedown', (e) => {
  const rect = canvas.getBoundingClientRect();
  const scaleX = devW / rect.width;
  const scaleY = devH / rect.height;
  touchStart = { x: Math.round(e.clientX - rect.left), y: Math.round(e.clientY - rect.top) };
  touchStartTime = Date.now();
});

canvas.addEventListener('mouseup', (e) => {
  if (!touchStart || !activeDeviceId) return;
  const rect = canvas.getBoundingClientRect();
  const scaleX = devW / rect.width;
  const scaleY = devH / rect.height;
  const endX = Math.round(e.clientX - rect.left);
  const endY = Math.round(e.clientY - rect.top);
  const dx = Math.abs(endX - touchStart.x);
  const dy = Math.abs(endY - touchStart.y);
  const dt = Date.now() - touchStartTime;

  const devStartX = Math.round(touchStart.x * scaleX) - offsetX;
  const devStartY = Math.round(touchStart.y * scaleY) - offsetY;
  const devEndX = Math.round(endX * scaleX) - offsetX;
  const devEndY = Math.round(endY * scaleY) - offsetY;

  if (dx < 8 && dy < 8 && dt < 400) {
    send({type: 'cmd_tap', device_id: activeDeviceId, x: devStartX, y: devStartY});
  } else {
    send({type: 'cmd_swipe', device_id: activeDeviceId, x1: devStartX, y1: devStartY, x2: devEndX, y2: devEndY, duration: Math.min(dt * 2, 1000)});
  }
  touchStart = null;
});

canvas.addEventListener('touchstart', (e) => {
  e.preventDefault();
  const t = e.touches[0];
  const rect = canvas.getBoundingClientRect();
  touchStart = { x: Math.round(t.clientX - rect.left), y: Math.round(t.clientY - rect.top) };
  touchStartTime = Date.now();
});

canvas.addEventListener('touchend', (e) => {
  e.preventDefault();
  if (!touchStart || !activeDeviceId) return;
  const t = e.changedTouches[0];
  const rect = canvas.getBoundingClientRect();
  const scaleX = devW / rect.width;
  const scaleY = devH / rect.height;
  const endX = Math.round(t.clientX - rect.left);
  const endY = Math.round(t.clientY - rect.top);
  const dx = Math.abs(endX - touchStart.x);
  const dy = Math.abs(endY - touchStart.y);
  const dt = Date.now() - touchStartTime;

  const devStartX = Math.round(touchStart.x * scaleX) - offsetX;
  const devStartY = Math.round(touchStart.y * scaleY) - offsetY;
  const devEndX = Math.round(endX * scaleX) - offsetX;
  const devEndY = Math.round(endY * scaleY) - offsetY;

  if (dx < 8 && dy < 8 && dt < 400) {
    send({type: 'cmd_tap', device_id: activeDeviceId, x: devStartX, y: devStartY});
  } else {
    send({type: 'cmd_swipe', device_id: activeDeviceId, x1: devStartX, y1: devStartY, x2: devEndX, y2: devEndY, duration: Math.min(dt * 2, 1000)});
  }
  touchStart = null;
});

// ============================================================
// Control Bar
// ============================================================
function sendKey(key) {
  if (!activeDeviceId) return alert('请先选择一个设备');
  send({type: 'cmd_key', device_id: activeDeviceId, key: key});
}

function toggleGrid() {
  gridMode = !gridMode;
  var btn = document.getElementById('grid-toggle');
  var gv = document.getElementById('grid-view');
  var sa = document.getElementById('screen-area');
  var cb = document.getElementById('control-bar');
  if (gridMode) {
    btn.style.borderColor = 'var(--accent)'; btn.style.color = 'var(--accent)';
    sa.style.display = 'none'; cb.style.display = 'none';
    gv.style.display = 'block';
    send({type: 'watch_all'});
    buildGrid();
  } else {
    btn.style.borderColor = 'var(--border)'; btn.style.color = 'var(--text2)';
    sa.style.display = ''; cb.style.display = '';
    gv.style.display = 'none';
    if (activeDeviceId) send({type: 'watch_one', device_id: activeDeviceId});
  }
}

function buildGrid() {
  var cols = parseInt(document.getElementById('grid-cols').value) || 3;
  var gap = 12;
  var w = 'calc(' + (100/cols).toFixed(2) + '% - ' + (gap - gap/cols).toFixed(0) + 'px)';
  var container = document.getElementById('grid-container');
  var countEl = document.getElementById('grid-count');
  container.innerHTML = '';
  deviceCanvases = {};
  var sorted = devices.slice().sort(function(a, b) {
    var va, vb;
    if (sortField === 'id') { va = (a.name||a.id).toLowerCase(); vb = (b.name||b.id).toLowerCase(); }
    else if (sortField === 'model') { va = (a.model||'').toLowerCase(); vb = (b.model||'').toLowerCase(); }
    else { va = a.created_at||''; vb = b.created_at||''; }
    if (va < vb) return sortAsc ? -1 : 1;
    if (va > vb) return sortAsc ? 1 : -1;
    return 0;
  });
  sorted.forEach(function(d) {
    var card = document.createElement('div');
    card.style.cssText = 'width:' + w + ';min-width:150px;background:var(--surface);border-radius:10px;overflow:hidden;border:1px solid var(--border);';
    card.innerHTML = '<div style="padding:6px 10px;font-size:11px;display:flex;align-items:center;gap:6px;">' +
      '<span style="width:6px;height:6px;border-radius:50%;background:' + (d.status==='online'?'var(--online)':'var(--offline)') + ';"></span>' +
      escHtml(d.name||d.id) + ' <span style="color:var(--text2);">' + escHtml(d.model||'') + '</span></div>';
    var cvs = document.createElement('canvas');
    cvs.style.cssText = 'width:100%;aspect-ratio:' + (d.resolution||'720x1600').replace('x','/') + ';background:#000;';
    var btns = document.createElement('div');
    btns.style.cssText = 'display:flex;gap:4px;padding:6px 10px;';
    btns.innerHTML = '<button style="flex:1;padding:4px 0;background:var(--surface2);border:1px solid var(--border);color:var(--text);border-radius:4px;cursor:pointer;font-size:10px;">🏠 主页</button>' +
      '<button style="flex:1;padding:4px 0;background:var(--surface2);border:1px solid var(--border);color:var(--text);border-radius:4px;cursor:pointer;font-size:10px;">← 返回</button>';
    btns.children[0].onclick = function(e) { e.stopPropagation(); send({type:'cmd_key', device_id: d.id, key:'home'}); };
    btns.children[1].onclick = function(e) { e.stopPropagation(); send({type:'cmd_key', device_id: d.id, key:'back'}); };
    card.appendChild(cvs);
    card.appendChild(btns);
    
    // 文本输入行
    var txtRow = document.createElement('div');
    txtRow.style.cssText = 'display:flex;gap:3px;padding:0 10px 4px 10px;';
    var txtInp = document.createElement('input');
    txtInp.placeholder = '文字...';
    txtInp.style.cssText = 'flex:1;padding:3px 6px;background:var(--bg);border:1px solid var(--border);border-radius:4px;color:var(--text);font-size:10px;outline:none;min-width:0;';
    var txtBtn = document.createElement('button');
    txtBtn.textContent = '发';
    txtBtn.style.cssText = 'padding:3px 8px;background:var(--surface2);border:1px solid var(--border);color:var(--text);border-radius:4px;cursor:pointer;font-size:10px;';
    txtBtn.onclick = function(e) { e.stopPropagation(); var v=txtInp.value.trim(); if(v){ send({type:'cmd_input', device_id:d.id, text:v}); txtInp.value=''; } };
    txtInp.onkeydown = function(e) { if(e.key==='Enter'){ e.stopPropagation(); var v=txtInp.value.trim(); if(v){ send({type:'cmd_input', device_id:d.id, text:v}); txtInp.value=''; } } };
    txtRow.appendChild(txtInp); txtRow.appendChild(txtBtn);
    card.appendChild(txtRow);
    
    // AI 任务行
    var aiRow = document.createElement('div');
    aiRow.style.cssText = 'display:flex;gap:3px;padding:0 10px 6px 10px;';
    var aiInp = document.createElement('input');
    aiInp.placeholder = 'AI 任务...';
    aiInp.style.cssText = 'flex:1;padding:3px 6px;background:var(--bg);border:1px solid var(--border);border-radius:4px;color:var(--text);font-size:10px;outline:none;min-width:0;';
    var aiBtn = document.createElement('button');
    aiBtn.textContent = '▶';
    aiBtn.style.cssText = 'padding:3px 10px;background:var(--accent);color:#fff;border:none;border-radius:4px;cursor:pointer;font-size:10px;';
    aiBtn.onclick = function(e) { e.stopPropagation(); var v=aiInp.value.trim(); if(v){ send({type:'cmd_task', device_id:d.id, prompt:v}); aiInp.value=''; } };
    aiInp.onkeydown = function(e) { if(e.key==='Enter'){ e.stopPropagation(); var v=aiInp.value.trim(); if(v){ send({type:'cmd_task', device_id:d.id, prompt:v}); aiInp.value=''; } } };
    aiRow.appendChild(aiInp); aiRow.appendChild(aiBtn);
    card.appendChild(aiRow);
    
    container.appendChild(card);
    
    // 绑定点击事件
    (function(devId, cvsEl) {
      var touchStart = null, touchStartTime = 0;
      cvsEl.addEventListener('mousedown', function(e) {
        var rect = cvsEl.getBoundingClientRect();
        var sx = devW, sy = devH;
        if (d.resolution) {
          var parts = d.resolution.split('x');
          if (parts.length === 2) { sx = parseInt(parts[0])||720; sy = parseInt(parts[1])||1600; }
        }
        touchStart = { x: Math.round((e.clientX - rect.left) * (sx / rect.width)), y: Math.round((e.clientY - rect.top) * (sy / rect.height)) };
        touchStartTime = Date.now();
      });
      cvsEl.addEventListener('mouseup', function(e) {
        if (!touchStart) return;
        var rect = cvsEl.getBoundingClientRect();
        var sx = devW, sy = devH;
        if (d.resolution) {
          var parts = d.resolution.split('x');
          if (parts.length === 2) { sx = parseInt(parts[0])||720; sy = parseInt(parts[1])||1600; }
        }
        var ox = JSON.parse(localStorage.getItem('nftouch_cal_' + devId) || '{}');
        var endX = Math.round((e.clientX - rect.left) * (sx / rect.width));
        var endY = Math.round((e.clientY - rect.top) * (sy / rect.height));
        var dx = Math.abs(endX - touchStart.x);
        var dy = Math.abs(endY - touchStart.y);
        if (dx < 8 && dy < 8 && Date.now() - touchStartTime < 400) {
          send({type: 'cmd_tap', device_id: d.id, x: touchStart.x - (ox.x||0), y: touchStart.y - (ox.y||0)});
        } else {
          send({type: 'cmd_swipe', device_id: d.id, x1: touchStart.x - (ox.x||0), y1: touchStart.y - (ox.y||0), x2: endX - (ox.x||0), y2: endY - (ox.y||0), duration: Math.min(Date.now() - touchStartTime, 1000)});
        }
        touchStart = null;
      });
    })(d.id, cvs);
    deviceCanvases[d.id] = {canvas: cvs, ctx: cvs.getContext('2d'), img: new Image(), devW: 720, devH: 1600};
  });
  countEl.textContent = devices.length + ' 台设备';
}

function logout() {
  fetch('/api/logout', {method:'POST'}).finally(() => location.href = '/login.html');
}

function sendText() {
  if (!activeDeviceId) return alert('请先选择一个设备');
  const input = document.getElementById('text-input');
  const text = input.value;
  if (!text) return;
  send({type: 'cmd_input', device_id: activeDeviceId, text: text});
  showTaskMsg('已发送: ' + text.substring(0, 30));
  input.value = '';
}

function sendTask() {
  if (!activeDeviceId) return alert('请先选择一个设备');
  const input = document.getElementById('task-input');
  const prompt = input.value.trim();
  if (!prompt) return;
  showTaskMsg('⏳ 正在执行: ' + prompt);
  send({type: 'cmd_task', device_id: activeDeviceId, prompt: prompt});
  input.value = '';
  document.getElementById('execute-btn').disabled = true;
  document.getElementById('execute-btn').textContent = '执行中...';
  setTimeout(() => {
    document.getElementById('execute-btn').disabled = false;
    document.getElementById('execute-btn').textContent = '▶ 执行';
  }, 10000);
}

function showTaskMsg(text) {
  const el = document.getElementById('task-msg');
  el.style.display = 'block';
  el.textContent = text;
  // Auto-hide after 15s
  clearTimeout(el._timeout);
  el._timeout = setTimeout(() => { el.style.display = 'none'; }, 15000);
}

// ============================================================
// Bind Modal
// ============================================================
function showBindModal() {
  document.getElementById('bind-modal').classList.remove('hidden');
  document.getElementById('bind-step1').classList.remove('hidden');
  document.getElementById('bind-result').classList.add('hidden');
  document.getElementById('code-input').value = '';
  document.getElementById('bind-error').classList.add('hidden');
  document.getElementById('code-input').focus();
}

function hideBindModal() {
  document.getElementById('bind-modal').classList.add('hidden');
}

let lastBindToken = '';

async function doBind() {
  const code = document.getElementById('code-input').value.trim();
  if (code.length !== 6 || !/^\d{6}$/.test(code)) {
    document.getElementById('bind-error').textContent = '请输入 6 位数字配对码';
    document.getElementById('bind-error').classList.remove('hidden');
    return;
  }

  const apiBase = `${location.protocol}//${location.host}`;
  try {
    const resp = await fetch(`${apiBase}/api/bind`, {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({code})
    });
    const data = await resp.json();
    if (!resp.ok) {
      document.getElementById('bind-error').textContent = data.error || '绑定失败';
      document.getElementById('bind-error').classList.remove('hidden');
      return;
    }
    // Show result inline
    document.getElementById('bind-step1').classList.add('hidden');
    document.getElementById('bind-result').classList.remove('hidden');
    document.getElementById('bind-device-id').textContent = data.device_id;
    document.getElementById('bind-token').textContent = data.token;
    lastBindToken = data.token;
    send({type: 'refresh'});
  } catch (e) {
    document.getElementById('bind-error').textContent = '网络错误，请重试';
    document.getElementById('bind-error').classList.remove('hidden');
  }
}

function copyToken() {
  navigator.clipboard.writeText(lastBindToken).then(() => {
    alert('Token 已复制到剪贴板！');
  }).catch(() => {
    // Fallback: select text manually
    const el = document.getElementById('bind-token');
    const range = document.createRange();
    range.selectNode(el);
    window.getSelection().removeAllRanges();
    window.getSelection().addRange(range);
  });
}

function showDeviceSettings(id) {
  var dev = devices.find(d => d.id === id);
  if (!dev) return;
  calDeviceId = id;
  var s = JSON.parse(localStorage.getItem('nftouch_cal_' + id) || '{}');
  document.getElementById('dev-settings-title').textContent = '设备: ' + escHtml(dev.name || dev.id);
  document.getElementById('dev-settings-content').innerHTML = `
    <div style="font-size:12px;color:var(--text2);margin-bottom:8px;">
      型号: ${escHtml(dev.model||'-')} · 分辨率: ${escHtml(dev.resolution||'-')} · 电量: ${dev.battery}%<br>
      状态: ${dev.status==='online'?'🟢 在线':'🔴 离线'} · 当前校准: X=${s.x||0} Y=${s.y||0}
    </div>
    ${dev.status==='offline' ? '<div style="font-size:11px;color:var(--offline);margin-bottom:8px;padding:8px;background:var(--bg);border-radius:6px;">⚠ 请在手机上打开 NFTouch 应用即可自动重连。<br>如果仍无法连接，点 × 删掉后重新配对。</div>' : ''}
    <div style="font-size:11px;color:var(--accent);margin-bottom:8px;">💡 鼠标悬停在画面上可看到实时坐标</div>
    <div style="display:flex;gap:8px;align-items:center;margin-bottom:4px;">
      <button class="quick-btn" onclick="nudge(-1,0)" style="padding:4px 8px;">◀</button>
      <label style="font-size:13px;">X <input id="ds-x" type="number" value="${s.x||0}" onchange="nudge(0,0)" style="width:55px;background:var(--bg);border:1px solid var(--border);color:var(--text);padding:5px;border-radius:6px;text-align:center;"></label>
      <button class="quick-btn" onclick="nudge(1,0)" style="padding:4px 8px;">▶</button>
      <button class="quick-btn" onclick="nudge(0,-1)" style="padding:4px 8px;">▲</button>
      <label style="font-size:13px;">Y <input id="ds-y" type="number" value="${s.y||0}" onchange="nudge(0,0)" style="width:55px;background:var(--bg);border:1px solid var(--border);color:var(--text);padding:5px;border-radius:6px;text-align:center;"></label>
      <button class="quick-btn" onclick="nudge(0,1)" style="padding:4px 8px;">▼</button>
      <button class="quick-btn" onclick="testCal()">测试</button>
      <button class="quick-btn" onclick="saveDeviceCal2()">保存</button>
    </div>
  `;
  var btn = document.getElementById('cal-start-btn');
  if (btn) btn.style.display = dev.status === 'online' ? '' : 'none';
  document.getElementById('dev-settings-modal').classList.remove('hidden');
}

var calDeviceId = null, calStep = 0, calTargetX = 0, calTargetY = 0;

function nudge(dx, dy) {
  var elx = document.getElementById('ds-x');
  var ely = document.getElementById('ds-y');
  if (!elx || !ely) return;
  var ox = (parseInt(elx.value) || 0) + dx;
  var oy = (parseInt(ely.value) || 0) + dy;
  elx.value = ox; ely.value = oy;
  if (calDeviceId === activeDeviceId) {
    offsetX = ox; offsetY = oy;
    document.getElementById('cal-info').textContent = '校准 ' + ox + ',' + oy;
  }
}

function testCal() {
  var id = calDeviceId;
  var ox = parseInt(document.getElementById('ds-x').value) || 0;
  var oy = parseInt(document.getElementById('ds-y').value) || 0;
  // 发送两次点击：一次顶部中间（拉通知栏），一次左上固定点（精确定位）
  var tx1 = Math.round(devW / 2) - ox;
  var ty1 = 5 - oy;
  send({type: 'cmd_tap', device_id: id, x: tx1, y: ty1, duration: 100});
  setTimeout(function() {
    // 第二次点击：左上角 1/4 处（容易辨认的固定位置）
    var tx2 = Math.round(devW / 4) - ox;
    var ty2 = Math.round(devH / 4) - oy;
    send({type: 'cmd_tap', device_id: id, x: tx2, y: ty2, duration: 100});
  }, 600);
}

function saveDeviceCal2() {
  var id = calDeviceId;
  var x = parseInt(document.getElementById('ds-x').value) || 0;
  var y = parseInt(document.getElementById('ds-y').value) || 0;
  localStorage.setItem('nftouch_cal_' + id, JSON.stringify({x:x, y:y}));
  if (id === activeDeviceId) { offsetX = x; offsetY = y; }
  document.getElementById('dev-settings-modal').classList.add('hidden');
}

function startCalibration() {
  document.getElementById('dev-settings-modal').classList.add('hidden');
  calStep = 1;
  // 发送一个测试点击到手机屏幕中心
  calTargetX = Math.round(devW / 2);
  calTargetY = Math.round(devH / 2);
  send({type: 'cmd_tap', device_id: calDeviceId, x: calTargetX, y: calTargetY, duration: 80});
  document.getElementById('cal-bar').style.display = 'flex';
  document.getElementById('cal-bar-text').textContent = '已向手机屏幕中心发送一次点击';
  document.getElementById('cal-bar-step').textContent = '请观察手机屏幕上的触摸反馈位置，然后在下方画面上点击那个位置';
}

function showCalBar() {
  // 不再使用
}

function cancelCalibration() {
  calStep = 0;
  document.getElementById('cal-bar').style.display = 'none';
}

function calcAndSave() {
  if (calPoints.length < 1) { cancelCalibration(); return; }
  var ox = calPoints[0].x - calTargetX;
  var oy = calPoints[0].y - calTargetY;
  
  localStorage.setItem('nftouch_cal_' + calDeviceId, JSON.stringify({x: ox, y: oy}));
  if (calDeviceId === activeDeviceId) {
    offsetX = ox; offsetY = oy;
    document.getElementById('cal-info').textContent = (ox||oy) ? '校准 ' + ox + ',' + oy : '';
  }
  // 发送验证点击
  var vx = Math.round(devW * 0.3);
  var vy = Math.round(devH * 0.3);
  send({type: 'cmd_tap', device_id: calDeviceId, x: vx - ox, y: vy - oy, duration: 80});
  var bar = document.getElementById('cal-bar');
  document.getElementById('cal-bar-text').textContent = '校准完成！X=' + ox + ' Y=' + oy + '（已发送验证点击到左上区域）';
  document.getElementById('cal-bar-step').textContent = '';
  setTimeout(function() { bar.style.display = 'none'; }, 5000);
  calStep = 0; calPoints = [];
}

// 校准模式：点击画面反馈测试点击的位置
canvas.addEventListener('mousedown', function(e) {
  if (calStep !== 1) return;
  var rect = canvas.getBoundingClientRect();
  var x = Math.round((e.clientX - rect.left) * (devW / rect.width));
  var y = Math.round((e.clientY - rect.top) * (devH / rect.height));
  calPoints = [{x: x, y: y}];
  calStep = 2;
  calcAndSave();
  e.stopPropagation(); e.preventDefault();
}, true);

async function deleteDevice(id) {
  if (!confirm('确定要删除该设备吗？')) return;
  const apiBase = `${location.protocol}//${location.host}`;
  try {
    const resp = await fetch(`${apiBase}/api/devices/${encodeURIComponent(id)}`, {
      method: 'DELETE'
    });
    if (resp.ok) {
      if (activeDeviceId === id) {
        activeDeviceId = null;
        document.getElementById('screen-placeholder').classList.remove('hidden');
        canvas.classList.add('hidden');
      }
      send({type: 'refresh'});
    }
  } catch(e) {}
}


// 鼠标悬停显示屏幕坐标
canvas.addEventListener('mousemove', function(e) {
  if (calStep !== 0) return;
  var rect = canvas.getBoundingClientRect();
  var sx = Math.round((e.clientX - rect.left) * (devW / rect.width)) - offsetX;
  var sy = Math.round((e.clientY - rect.top) * (devH / rect.height)) - offsetY;
  document.getElementById('status-right').textContent = '坐标 ' + sx + ',' + sy + ' | 校准 ' + offsetX + ',' + offsetY;
});

// 点击屏幕区域后，电脑键盘输入直接发送到手机
let screenFocused = false;
canvas.addEventListener('click', (e) => {
  screenFocused = true;
  canvas.style.outline = '2px solid var(--accent)';
  canvas.style.outlineOffset = '-2px';
  e.stopPropagation();
});
document.addEventListener('click', (e) => {
  if (e.target !== canvas && !canvas.contains(e.target)) {
    screenFocused = false;
    canvas.style.outline = 'none';
  }
});
canvas.tabIndex = 0;

document.addEventListener('keydown', (e) => {
  // 系统快捷键
  if (e.target.tagName === 'INPUT') return;
  if (!activeDeviceId) return;
  if (e.key === 'Escape') { sendKey('lock'); return; }
  
  // 键盘输入模式：屏幕区域聚焦时发送按键
  if (!screenFocused) return;
  if (e.ctrlKey || e.metaKey || e.altKey) return;
  var ch = e.key;
  if (ch.length === 1) {
    send({type: 'cmd_input', device_id: activeDeviceId, text: ch});
    e.preventDefault();
  } else if (ch === 'Enter') {
    send({type: 'cmd_input', device_id: activeDeviceId, text: '\n'});
    e.preventDefault();
  } else if (ch === 'Backspace') {
    send({type: 'cmd_input', device_id: activeDeviceId, text: ''});
    e.preventDefault();
  }
});
