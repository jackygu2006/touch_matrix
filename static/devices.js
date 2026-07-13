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

  container.innerHTML = sorted.map(d => {
    var hasData = d.permissions && Object.keys(d.permissions).length > 0;
    var dotClass = d.status === 'online' ? (hasData ? (hasAllPerms(d) ? 'online' : 'warn') : 'loading') : 'offline';
    var missing = hasData ? getMissingPerms(d) : [];
    var warnHtml = missing.length > 0 ? ' <span style="color:var(--warn);font-size:10px;" title="缺少权限: ' + missing.join(', ') + '">⚠ ' + missing.slice(0,2).join('/') + (missing.length > 2 ? '...' : '') + '</span>' : '';
    var screenOffHtml = (hasData && d.screen_on === false) ? ' <span style="color:var(--info);font-size:10px;" title="屏幕已关闭">◉ 息屏</span>' : '';
    return `
    <div class="device-item${d.id === activeDeviceId ? ' active' : ''}" onclick="selectDevice('${d.id}')">
      <div class="dot ${dotClass}"></div>
      <div class="info">
        <div class="name">${escHtml(d.name || d.id)}${d.status === 'unbound' ? ' <span style="color:var(--offline);font-size:11px;">● 已解绑</span>' : d.status !== 'online' ? ' <span style="color:var(--offline);font-size:11px;">● 离线</span>' : warnHtml + screenOffHtml}</div>
        <div class="meta">${escHtml(d.model || '')} · ${d.resolution || ''} · 电量 ${d.battery}%</div>
      </div>
      <span onclick="event.stopPropagation();deleteDevice('${d.id}')" style="cursor:pointer;opacity:.3;font-size:16px;padding:4px;font-weight:bold;" title="删除设备">×</span>
    </div>
  `}).join('');

  // Restore selection state
  if (activeDeviceId) {
    const dev = devices.find(d => d.id === activeDeviceId);
    if (!dev || dev.status === 'offline' || dev.status === 'unbound') {
      document.getElementById('perm-warning').style.display = 'none';
      document.getElementById('screen-warning').style.display = 'none';
      var msg = '';
      if (dev && dev.status === 'unbound') msg = '⚠ 设备已解绑<br>请重新配对';
      else if (dev && dev.status === 'offline') msg = '⚠ 设备已离线<br>请在设备上打开 NF Touch App';
      else msg = '选择一个设备查看实时屏幕';
      document.getElementById('screen-placeholder').innerHTML = '<div style="color:var(--offline);font-size:14px;text-align:center;line-height:1.8;">' + msg + '</div>';
      document.getElementById('screen-placeholder').classList.remove('hidden');
      canvas.classList.add('hidden');
    } else {
      var hasData = dev.permissions && Object.keys(dev.permissions).length > 0;

      // 权限检测：WebSocket 在线但权限不完整，显示黄色警告（数据未就绪时不显示）
      var missingPerms = hasData ? getMissingPerms(dev) : [];
      var permWarnEl = document.getElementById('perm-warning');
      if (missingPerms.length > 0) {
        permWarnEl.innerHTML = '⚠ 缺少权限：' + missingPerms.join('、') + ' — 请在手机上开启';
        permWarnEl.style.display = 'block';
      } else {
        permWarnEl.style.display = 'none';
      }

      // 屏幕状态检测：数据未就绪时不显示
      var screenWarnEl = document.getElementById('screen-warning');
      if (hasData && dev.screen_on === false) {
        screenWarnEl.style.display = 'block';
      } else {
        screenWarnEl.style.display = 'none';
      }

      // 检测设备是否无数据（5秒无帧=可能卡死，8秒无消息=可能离线）
      if (dev.last_frame && new Date(dev.last_frame).getTime() > 0) {
        var age = (Date.now() - new Date(dev.last_frame).getTime()) / 1000;
        if (age > 8) {
          document.getElementById('screen-placeholder').innerHTML = '<div style="color:var(--offline);font-size:14px;text-align:center;">⚠ 设备可能已离线<br>超过 ' + Math.round(age) + ' 秒无数据，正在确认...</div>';
          document.getElementById('screen-placeholder').classList.remove('hidden');
          canvas.classList.add('hidden');
          return;
        }
        if (age > 5) {
          document.getElementById('screen-placeholder').innerHTML = '<div style="color:var(--offline);font-size:14px;text-align:center;">⚠ 设备在线但无画面<br>请重启无障碍服务或NF Touch</div>';
          document.getElementById('screen-placeholder').classList.remove('hidden');
          canvas.classList.add('hidden');
          return;
        }
      }
      document.getElementById('screen-placeholder').classList.add('hidden');
      canvas.classList.remove('hidden');
    }
  }
}

function escHtml(s) { const d=document.createElement('div'); d.textContent=s; return d.innerHTML; }

function getMissingPerms(dev) {
  if (!dev.permissions) return [];
  var names = {accessibility:'无障碍服务', notification:'常驻通知', overlay:'悬浮窗', battery_opt:'电池优化', storage:'存储权限'};
  var missing = [];
  for (var k in dev.permissions) {
    if (!dev.permissions[k]) missing.push(names[k] || k);
  }
  return missing;
}

function hasAllPerms(dev) {
  if (!dev.permissions) return true;
  for (var k in dev.permissions) {
    if (!dev.permissions[k]) return false;
  }
  return true;
}

function selectDevice(id) {
  activeDeviceId = id;
  // 如果在 Grid 模式，退出并切换到单设备模式
  if (gridMode) toggleGrid();
  send({type: 'watch_one', device_id: id});
  renderDeviceList();
  updateDeviceStatus();
  setTaskButton(false);
  if (typeof refreshTaskHistory === 'function') refreshTaskHistory();
}

function updateDeviceStatus() {
  const el = document.getElementById('status-left');
  if (!activeDeviceId) {
    el.textContent = '未连接';
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

// 千分比坐标 (0-1000)，根据设备 JPEG 分辨率自动换算
function sendGesture(dir) {
  if (!activeDeviceId) return toast('请先选择一个设备', 'error');
  var w = devW || 720;
  var h = devH || 1600;
  var s = {
    up:           [w*0.5, h*0.7,  w*0.5, h*0.3,  300],
    down:         [w*0.5, h*0.3,  w*0.5, h*0.7,  300],
    left:         [w*0.8, h*0.5,  w*0.2, h*0.5,  300],
    right:        [w*0.2, h*0.5,  w*0.8, h*0.5,  300],
    notify:       [w*0.5, h*0.05, w*0.5, h*0.4,  300],
    home_gesture: [w*0.5, h*0.95, w*0.5, h*0.3,  500]
  };
  var g = s[dir];
  if (!g) return;
  send({type: 'cmd_swipe', device_id: activeDeviceId, x1: Math.round(g[0]), y1: Math.round(g[1]), x2: Math.round(g[2]), y2: Math.round(g[3]), duration: g[4]});
}

function toggleGrid() {
  gridMode = !gridMode;
  var btn = document.getElementById('grid-toggle');
  var gv = document.getElementById('grid-view');
  var sa = document.getElementById('screen-area');
  var cb = document.getElementById('control-bar');
  var tp = document.getElementById('task-panel');
  if (gridMode) {
    btn.style.borderColor = 'var(--accent)'; btn.style.color = 'var(--accent)';
    sa.style.display = 'none'; cb.style.display = 'none'; tp.style.display = 'none';
    gv.style.display = 'block';
    send({type: 'watch_all'});
    buildGrid();
  } else {
    btn.style.borderColor = 'var(--border)'; btn.style.color = 'var(--text2)';
    sa.style.display = ''; cb.style.display = ''; tp.style.display = '';
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
        // 使用 canvas 像素尺寸（与 JPEG 流一致），而非设备屏幕分辨率
        var sx = cvsEl.width || devW || 720;
        var sy = cvsEl.height || devH || 1600;
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

