// State, Init, WebSocket, Toast
// ============================================================
// State
// ============================================================
const adminKey = new URLSearchParams(location.search).get('key') || 'admin123';
const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
const wsUrl = `${protocol}//${location.host}/ws/dash?token=${encodeURIComponent(localStorage.getItem("nftouch_token")||"")}`;

let ws = null;
let devices = [];
let activeDeviceId = null;
let canvas = document.getElementById('screen-canvas');
let ctx = canvas.getContext('2d');
let img = new Image();
let devW = 1080, devH = 1920;
let gridMode = false;
let deviceCanvases = {}; // device_id -> {canvas, ctx, img, devW, devH}

// ============================================================
// Init
// ============================================================
(async function() {
  try {
    var token = localStorage.getItem('nftouch_token');
    var resp = await fetch('/api/auth/check', {
      headers: token ? {'Authorization': 'Bearer ' + token} : {}
    });
    if (!resp.ok) { localStorage.removeItem('nftouch_token'); location.href = '/login.html'; return; }
    var data = await resp.json();
    if (data.role === 'admin') {
      document.getElementById('sidebar-footer').innerHTML += '<button class="sidebar-btn" onclick="showAdminPanel()">用户管理</button>';
    }
    var ui = document.getElementById('user-info');
  ui.style.display = 'block';
  var txt = (data.nickname || data.email);
  if (data.role !== 'admin' && data.max_devices) txt += ' · ' + data.max_devices + '台配额';
  if (data.role === 'admin') txt += ' · 管理员';
  ui.textContent = txt;
  } catch(e) { location.href = '/login.html'; return; }
  // connect() is called at end of ui.js
})();

function toast(msg, style) {
  var el = document.getElementById('task-msg');
  el.style.visibility = 'visible';
  el.textContent = msg;
  el.style.color = style === 'error' ? '#f87171' : style === 'success' ? '#22c55e' : 'var(--text2)';
  clearTimeout(el._timeout);
  el._timeout = setTimeout(function() { el.style.visibility = 'hidden'; }, 3000);
}


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
    // 通过 REST API 拉取设备列表作为兜底
    fetch('/api/devices', {headers:{'Authorization':'Bearer '+localStorage.getItem('nftouch_token')}})
      .then(r=>r.json()).then(d=>{if(Array.isArray(d)){devices=d;if(typeof renderDeviceList==='function')renderDeviceList();}})
      .catch(function(){});
  };

  let pendingFrameHeader = null;

  ws.onmessage = (e) => {
    if (typeof e.data === 'string') {
      const msg = JSON.parse(e.data);
      switch (msg.type) {
        case 'device_list':
          devices = msg.devices || [];
          if (typeof renderDeviceList === 'function') {
            renderDeviceList();
            updateDeviceStatus();
          }
          break;
        case 'frame':
          pendingFrameHeader = msg;
          break;
        case 'task_status':
          showTaskMsg(msg.text);
          break;
        case 'error':
          toast('错误: ' + msg.reason, 'error');
          break;
      }
    } else if (e.data instanceof ArrayBuffer) {
      if (pendingFrameHeader) {
        var fdevId = pendingFrameHeader.device_id;
        // Grid mode: render to device canvas
        if (gridMode && deviceCanvases[fdevId]) {
          var dc = deviceCanvases[fdevId];
          const blob = new Blob([e.data.byteLength > 4 && new Uint8Array(e.data)[0] !== 0xFF ? e.data.slice(4) : e.data], {type: 'image/jpeg'});
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
        const blob = new Blob([e.data.byteLength > 4 && new Uint8Array(e.data)[0] !== 0xFF ? e.data.slice(4) : e.data], {type: 'image/jpeg'});
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
// Device List, Grid, Sorting, Canvas Events
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
        <div class="name">${escHtml(d.name || d.id)}${d.status === 'unbound' ? ' <span style="color:var(--offline);font-size:11px;">● 已解绑</span>' : d.status === 'offline' ? ' <span style="color:var(--offline);font-size:11px;">● 离线</span>' : ''}</div>
        <div class="meta">${escHtml(d.model || '')} · ${d.resolution || ''} · 电量 ${d.battery}%</div>
      </div>
      <span onclick="event.stopPropagation();deleteDevice('${d.id}')" style="cursor:pointer;opacity:.3;font-size:16px;padding:4px;font-weight:bold;" title="删除设备">×</span>
    </div>
  `).join('');

  // Restore selection state
  if (activeDeviceId) {
    const dev = devices.find(d => d.id === activeDeviceId);
    if (!dev || dev.status === 'offline' || dev.status === 'unbound') {
      var msg = '';
      if (dev && dev.status === 'unbound') msg = '⚠ 设备已解绑<br>请重新配对';
      else if (dev && dev.status === 'offline') msg = '⚠ 设备已离线<br>请在设备上打开 NF Touch App';
      else msg = '选择一个设备查看实时屏幕';
      document.getElementById('screen-placeholder').innerHTML = '<div style="color:var(--offline);font-size:14px;text-align:center;line-height:1.8;">' + msg + '</div>';
      document.getElementById('screen-placeholder').classList.remove('hidden');
      canvas.classList.add('hidden');
    } else {
      // 检测离线与无画面状态
      if (dev.last_message_at && new Date(dev.last_message_at).getTime() > 0) {
        var msgAge = (Date.now() - new Date(dev.last_message_at).getTime()) / 1000;
        if (msgAge > 8) {
          document.getElementById('screen-placeholder').innerHTML = '<div style="color:var(--offline);font-size:14px;text-align:center;">⚠ 设备可能已离线<br>最近消息距今 ' + Math.round(msgAge) + ' 秒</div>';
          document.getElementById('screen-placeholder').classList.remove('hidden');
          canvas.classList.add('hidden');
          return;
        }
      }
      if (dev.last_frame && new Date(dev.last_frame).getTime() > 0) {
        var frameAge = (Date.now() - new Date(dev.last_frame).getTime()) / 1000;
        if (frameAge > 5) {
          document.getElementById('screen-placeholder').innerHTML = '<div style="color:var(--offline);font-size:14px;text-align:center;">⚠ 设备在线但无画面<br>请重启无障碍服务或NF Touch</div>';
          document.getElementById('screen-placeholder').classList.remove('hidden');
          canvas.classList.add('hidden');
          return;
        }
      }
      document.getElementById('screen-placeholder').classList.add('hidden');
      canvas.classList.remove('hidden');
      var el = document.getElementById('task-msg');
      if (el.textContent.indexOf('离线') >= 0 || el.textContent.indexOf('解绑') >= 0) {
        el.style.visibility = 'hidden';
      }
    }
  }
}

function escHtml(s) { const d=document.createElement('div'); d.textContent=s; return d.innerHTML; }

function selectDevice(id) {
  activeDeviceId = id;
  // 如果在 Grid 模式，退出并切换到单设备模式
  if (gridMode) toggleGrid();
  send({type: 'watch_one', device_id: id});
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
    var dv = devices.find(d => d.id === activeDeviceId); el.textContent = dv && dv.status === 'unbound' ? '已连接 · 已解绑' : '已连接 · 设备离线 🔴';
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

  const devStartX = Math.round(touchStart.x * scaleX);
  const devStartY = Math.round(touchStart.y * scaleY);
  const devEndX = Math.round(endX * scaleX);
  const devEndY = Math.round(endY * scaleY);

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

  const devStartX = Math.round(touchStart.x * scaleX);
  const devStartY = Math.round(touchStart.y * scaleY);
  const devEndX = Math.round(endX * scaleX);
  const devEndY = Math.round(endY * scaleY);

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
  if (!activeDeviceId) return toast('请先选择一个设备', 'error');
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
      escHtml(d.name||d.id) + ' <span style="color:var(--text2);">' + escHtml(d.model||'') + '</span>' +
      (d.status==='unbound' ? ' <span style="color:var(--offline);font-size:10px;">已解绑</span>' : d.status==='offline' ? ' <span style="color:var(--offline);font-size:10px;">离线</span>' : '') +
      '</div>';
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
        var sx = cvsEl.width || devW || 720;
        var sy = cvsEl.height || devH || 1600;
        var endX = Math.round((e.clientX - rect.left) * (sx / rect.width));
        var endY = Math.round((e.clientY - rect.top) * (sy / rect.height));
        var dx = Math.abs(endX - touchStart.x);
        var dy = Math.abs(endY - touchStart.y);
        if (dx < 8 && dy < 8 && Date.now() - touchStartTime < 400) {
          send({type: 'cmd_tap', device_id: d.id, x: touchStart.x, y: touchStart.y});
        } else {
          send({type: 'cmd_swipe', device_id: d.id, x1: touchStart.x, y1: touchStart.y, x2: endX, y2: endY, duration: Math.min(Date.now() - touchStartTime, 1000)});
        }
        touchStart = null;
      });
    })(d.id, cvs);
    deviceCanvases[d.id] = {canvas: cvs, ctx: cvs.getContext('2d'), img: new Image(), devW: 720, devH: 1600};
  });
  countEl.textContent = devices.length + ' 台设备';
}

// Admin Panel
function showAdminPanel() {
  document.getElementById('admin-modal').classList.remove('hidden');
  var tb = document.getElementById('admin-user-table');
  while (tb.rows.length > 1) tb.deleteRow(1);
  loadAdminUsers();
}

async function loadAdminUsers() {
  var token = localStorage.getItem('nftouch_token');
  var resp = await fetch('/api/admin/users', {headers:{'Authorization':'Bearer '+token}});
  var users = await resp.json();
  var tb = document.getElementById('admin-user-table');
  users.forEach(function(u) {
    var row = tb.insertRow();
    row.style.borderTop = '1px solid var(--border)';
    row.innerHTML = '<td style="padding:8px;">' + escHtml(u.email) + '</td>' +
      '<td style="padding:8px;text-align:center;">' + (u.role==='admin'?'管理员':'用户') + '</td>' +
      '<td style="padding:8px;text-align:center;">' + (u.role==='admin'?'无限制':u.max_devices) + '</td>' +
      '<td style="padding:8px;text-align:center;color:' + (u.status==='active'?'var(--online)':'var(--offline)') + ';">' + (u.status==='active'?'启用':'禁用') + '</td>' +
      '<td style="padding:8px;text-align:center;" class="admin-actions"></td>';
    if (u.role !== 'admin') {
      var td = row.querySelector('.admin-actions');
      var eBtn = document.createElement('button'); eBtn.className = 'quick-btn'; eBtn.style.fontSize = '10px'; eBtn.textContent = '编辑';
      eBtn.onclick = function() { editUser(u.id, u.email, u.nickname||'', u.max_devices); };
      var tBtn = document.createElement('button'); tBtn.className = 'quick-btn'; tBtn.style.fontSize = '10px'; tBtn.textContent = u.status==='active'?'禁用':'启用';
      tBtn.onclick = function() { toggleUser(u.id); };
      var dBtn = document.createElement('button'); dBtn.className = 'quick-btn warn'; dBtn.style.fontSize = '10px'; dBtn.textContent = '删除';
      dBtn.onclick = function() { deleteUser(u.id); };
      td.appendChild(eBtn); td.appendChild(document.createTextNode(' '));
      td.appendChild(tBtn); td.appendChild(document.createTextNode(' '));
      td.appendChild(dBtn);
    } else {
      row.querySelector('.admin-actions').innerHTML = '<span style="color:var(--text2);">-</span>';
    }
  });
}

function showNewUserDialog() {
  document.getElementById('edit-user-title').textContent = '新建用户';
  document.getElementById('eu-id').value = '';
  document.getElementById('eu-email').value = '';
  document.getElementById('eu-nickname').value = '';
  document.getElementById('eu-password').value = '';
  document.getElementById('eu-quota').value = '5';
  document.getElementById('eu-error').classList.add('hidden');
  // Show email, hide nickname
  document.getElementById('eu-email-label').style.display = '';
  document.getElementById('eu-email').style.display = '';
  document.getElementById('eu-nick-label').style.display = 'none';
  document.getElementById('eu-nickname').style.display = 'none';
  document.getElementById('eu-pw-hint').textContent = '';
  document.getElementById('edit-user-modal').classList.remove('hidden');
}

async function createUser(email, password, nickname, maxDevices) {
  var token = localStorage.getItem('nftouch_token');
  var resp = await fetch('/api/admin/users', {
    method:'POST', headers:{'Content-Type':'application/json','Authorization':'Bearer '+token},
    body: JSON.stringify({email:email,password:password,nickname:nickname,max_devices:maxDevices})
  });
  var data = await resp.json();
  if (resp.ok) { toast('用户创建成功', 'success'); showAdminPanel(); }
  else { toast(data.error, 'error'); }
}

function editUser(id, email, nickname, maxDevices) {
  document.getElementById('edit-user-title').textContent = '编辑: ' + email;
  document.getElementById('eu-id').value = id;
  document.getElementById('eu-email').value = email;
  document.getElementById('eu-nickname').value = nickname || '';
  document.getElementById('eu-password').value = '';
  document.getElementById('eu-quota').value = maxDevices || 5;
  document.getElementById('eu-error').classList.add('hidden');
  // Show nickname, hide email (uneditable)
  document.getElementById('eu-email-label').style.display = '';
  document.getElementById('eu-email').style.display = '';
  document.getElementById('eu-email').disabled = true;
  document.getElementById('eu-nick-label').style.display = '';
  document.getElementById('eu-nickname').style.display = '';
  document.getElementById('eu-pw-hint').textContent = '（留空不修改）';
  document.getElementById('edit-user-modal').classList.remove('hidden');
}

async function saveEditUser() {
  var id = document.getElementById('eu-id').value;
  var nickname = document.getElementById('eu-nickname').value.trim();
  var password = document.getElementById('eu-password').value;
  var quota = parseInt(document.getElementById('eu-quota').value) || 5;
  
  if (!id) {
    // Creating new user
    var email = document.getElementById('eu-email').value.trim();
    if (!email || !password) {
      document.getElementById('eu-error').textContent = '邮箱和密码必填';
      document.getElementById('eu-error').classList.remove('hidden');
      return;
    }
    var token = localStorage.getItem('nftouch_token');
    var resp = await fetch('/api/admin/users', {
      method:'POST', headers:{'Content-Type':'application/json','Authorization':'Bearer '+token},
      body: JSON.stringify({email:email, password:password, nickname:'', max_devices:quota})
    });
    var data = await resp.json();
    if (resp.ok) { toast('用户创建成功', 'success'); document.getElementById('edit-user-modal').classList.add('hidden'); showAdminPanel(); }
    else { document.getElementById('eu-error').textContent = data.error; document.getElementById('eu-error').classList.remove('hidden'); }
    return;
  }
  
  // Editing existing user
  var body = {};
  if (password) body.password = password;
  body.nickname = document.getElementById('eu-nickname').value.trim();
  if (quota) body.max_devices = quota;
  var token = localStorage.getItem('nftouch_token');
  var resp = await fetch('/api/admin/users/' + id, {
    method:'PUT', headers:{'Content-Type':'application/json','Authorization':'Bearer '+token},
    body: JSON.stringify(body)
  });
  if (resp.ok) { toast('已更新', 'success'); document.getElementById('edit-user-modal').classList.add('hidden'); showAdminPanel(); }
  else { toast('更新失败', 'error'); }
}

async function toggleUser(id) {
  var token = localStorage.getItem('nftouch_token');
  var resp = await fetch('/api/admin/users/' + id + '/toggle', {
    method:'PUT', headers:{'Authorization':'Bearer '+token}
  });
  if (resp.ok) { toast('状态已切换', 'success'); showAdminPanel(); }
}

async function deleteUser(id) {
  if (!confirm('确定删除此用户及其所有设备？')) return;
  var token = localStorage.getItem('nftouch_token');
  var resp = await fetch('/api/admin/users/' + id, {
    method:'DELETE', headers:{'Authorization':'Bearer '+token}
  });
  if (resp.ok) { toast('已删除', 'success'); showAdminPanel(); }
}

function logout() {
  localStorage.removeItem('nftouch_token');
  localStorage.removeItem('nftouch_user');
  location.replace('/login.html');
}
// Control Bar, Bind Modal, Device Settings, Calibration
function sendText() {
  if (!activeDeviceId) return toast('请先选择一个设备', 'error');
  const input = document.getElementById('text-input');
  const text = input.value;
  if (!text) return;
  send({type: 'cmd_input', device_id: activeDeviceId, text: text});
  showTaskMsg('已发送: ' + text.substring(0, 30));
  input.value = '';
}

function sendTask() {
  if (!activeDeviceId) return toast('请先选择一个设备', 'error');
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
  el.style.visibility = 'visible';
  el.textContent = text;
  // Auto-hide after 15s
  clearTimeout(el._timeout);
  el._timeout = setTimeout(() => { el.style.visibility = 'hidden'; }, 15000);
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
      headers: {'Content-Type': 'application/json', 'Authorization': 'Bearer ' + localStorage.getItem('nftouch_token')},
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
  var text = document.getElementById('bind-token').textContent;
  if (!text) return;
  // Try modern API first
  if (navigator.clipboard && navigator.clipboard.writeText) {
    navigator.clipboard.writeText(text).then(function() {
      toast('Token 已复制！', 'success');
    }).catch(function() { fallbackCopy(text); });
  } else {
    fallbackCopy(text);
  }
}

function fallbackCopy(text) {
  var ta = document.createElement('textarea');
  ta.value = text;
  ta.style.position = 'fixed'; ta.style.left = '-999px';
  document.body.appendChild(ta);
  ta.select();
  try { document.execCommand('copy'); toast('Token 已复制！', 'success'); }
  catch(e) { toast('复制失败，请手动选择并复制', 'error'); }
  document.body.removeChild(ta);
}

function showDeviceSettings(id) {
  var dev = devices.find(d => d.id === id);
  if (!dev) return;
  document.getElementById('dev-settings-title').textContent = '设备: ' + escHtml(dev.name || dev.id);
  document.getElementById('dev-settings-content').innerHTML = `
    <div style="font-size:12px;color:var(--text2);margin-bottom:8px;">
      型号: ${escHtml(dev.model||'-')} · 分辨率: ${escHtml(dev.resolution||'-')} · 电量: ${dev.battery}%<br>
      状态: ${dev.status==='online'?'🟢 在线': dev.status==='unbound'?'🔴 已解绑':'🔴 离线'}
    </div>
    ${dev.status==='offline' ? '<div style="font-size:11px;color:var(--offline);margin-bottom:8px;padding:8px;background:var(--bg);border-radius:6px;">⚠ 请在手机上打开 NFTouch 应用即可自动重连。<br>如果仍无法连接，点 × 删掉后重新配对。</div>' : ''}
  `;
  document.getElementById('dev-settings-modal').classList.remove('hidden');
}

async function deleteDevice(id) {
  if (!confirm('确定要删除该设备吗？')) return;
  const apiBase = `${location.protocol}//${location.host}`;
  try {
    const resp = await fetch(`${apiBase}/api/devices/${encodeURIComponent(id)}`, {
      method: 'DELETE',
      headers: {'Authorization': 'Bearer ' + localStorage.getItem('nftouch_token')}
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
  var rect = canvas.getBoundingClientRect();
  var sx = Math.round((e.clientX - rect.left) * (devW / rect.width));
  var sy = Math.round((e.clientY - rect.top) * (devH / rect.height));
  document.getElementById('status-right').textContent = '坐标 ' + sx + ',' + sy;
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
connect();
connect();
