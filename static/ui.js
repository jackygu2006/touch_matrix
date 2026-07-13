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
  const p = document.createElement('div');
  p.textContent = text;
  p.style.padding = '4px 0';
  p.style.borderBottom = '1px solid var(--border)';
  el.appendChild(p);
  el.scrollTop = el.scrollHeight;
  // 保留最近 30 条消息
  while (el.children.length > 30) el.removeChild(el.firstChild);
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
      状态: ${dev.status==='online'?'🟢 在线':'🔴 离线'}
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
