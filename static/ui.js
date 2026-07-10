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
connect();
