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

var taskRunning = false;

function sendTask() {
  if (!activeDeviceId) return toast('请先选择一个设备', 'error');
  var input = document.getElementById('task-input');
  var prompt = input.value.trim();
  if (!prompt) return;

  if (taskRunning) {
    send({type: 'cmd_cancel_task', device_id: activeDeviceId});
    setTaskButton(false);
    appendChatMessage('bot', '⏹ 已发送停止指令');
    return;
  }

  var apiBase = location.protocol + '//' + location.host;
  var token = localStorage.getItem('nftouch_token');
  fetch(apiBase + '/api/devices/' + encodeURIComponent(activeDeviceId) + '/task', {
    method: 'POST',
    headers: {'Content-Type': 'application/json', 'Authorization': 'Bearer ' + token},
    body: JSON.stringify({prompt: prompt})
  }).then(function(r) { return r.json(); }).then(function(d) {
    if (d.task_id) {
      appendChatMessage('user', prompt);
      setTaskButton(true);
      input.value = '';
    } else {
      toast('发送失败: ' + (d.error || '未知错误'), 'error');
    }
  }).catch(function(e) {
    toast('发送失败: ' + e.message, 'error');
  });
}

function setTaskButton(running) {
  taskRunning = running;
  var btn = document.getElementById('execute-btn');
  if (running) {
    btn.innerHTML = '■';
    btn.style.background = '#ef4444';
    btn.title = '停止 (点击取消)';
  } else {
    btn.innerHTML = '➤';
    btn.style.background = '';
    btn.title = '发送 (Enter)';
  }
}

function chatTime() {
  var d = new Date();
  return ('0'+d.getHours()).slice(-2) + ':' + ('0'+d.getMinutes()).slice(-2) + ':' + ('0'+d.getSeconds()).slice(-2);
}

function appendChatMessage(type, text, time) {
  var el = document.getElementById('task-chat');
  if (!el) return;
  time = time || chatTime();
  var div = document.createElement('div');
  div.className = 'chat-msg ' + type;
  div.innerHTML = '<div class="chat-bubble">' + escHtml(text).replace(/\n/g, '<br>') + '</div><div class="chat-time">' + time + '</div>';
  el.appendChild(div);
  el.scrollTop = el.scrollHeight;
  // 保留最近 100 条消息
  while (el.children.length > 100) el.removeChild(el.firstChild);
}

function refreshTaskHistory() {
  if (!activeDeviceId) return;
  var el = document.getElementById('task-chat');
  if (el) el.innerHTML = '<div style="text-align:center;padding:24px;opacity:.3;">加载中...</div>';
  var apiBase = location.protocol + '//' + location.host;
  var token = localStorage.getItem('nftouch_token');
  fetch(apiBase + '/api/devices/' + encodeURIComponent(activeDeviceId) + '/tasks', {
    headers: {'Authorization': 'Bearer ' + token}
  }).then(function(r) {
    if (!r.ok) throw new Error('HTTP ' + r.status);
    return r.json();
  }).then(function(tasks) {
    renderTaskHistory(tasks);
  }).catch(function(e) {
    console.error('Failed to load task history:', e);
    if (el) el.innerHTML = '<div style="text-align:center;padding:24px;opacity:.3;">加载失败，请重试</div>';
  });
}

function renderTaskHistory(tasks) {
  var el = document.getElementById('task-chat');
  if (!el) return;
  el.innerHTML = '';
  if (!tasks || tasks.length === 0) {
    el.innerHTML = '<div style="text-align:center;padding:24px;opacity:.3;">暂无任务记录</div>';
    return;
  }
  // 倒序显示（最新的在底部）
  tasks.reverse().forEach(function(t) {
    var time = t.created_at ? t.created_at.substring(11, 16) : '';
    appendChatMessage('user', t.prompt, time);
    if (t.result) {
      t.result.split('\n---\n').forEach(function(line) {
        var timeMatch = line.match(/^\[(\d{2}:\d{2}:\d{2})\]/);
        var time = timeMatch ? timeMatch[1] : '';
        var text = line.replace(/^\[\d{2}:\d{2}:\d{2}\]\s*/, '');
        if (text.trim()) appendChatMessage('bot', text.trim(), time);
      });
    }
    if (t.status === 'running') {
      appendChatMessage('bot', '⏳ 执行中...', '');
    }
  });
}

function handleTaskStatus(msg) {
  var text = msg.text || '';
  var time = msg.time || chatTime();
  // 除掉可能已有的 [HH:MM:SS] 前缀（服务端已追加时间戳）
  var displayText = text.replace(/^\[\d{2}:\d{2}:\d{2}\]\s*/, '');
  if (displayText) appendChatMessage('bot', displayText, time);
  // 任务完成时重置按钮状态
  if (text.indexOf('✅') >= 0 || text.indexOf('完成') >= 0 || text.indexOf('失败') >= 0 || text.indexOf('取消') >= 0 || text.indexOf('错误') >= 0) {
    setTaskButton(false);
  }
}

// ============================================================
// 任务历史列表
// ============================================================
var historyOffset = 0, historyLimit = 20, historyHasMore = true, showingHistory = false;

function toggleTaskHistory() {
  showingHistory = !showingHistory;
  var chatEl = document.getElementById('task-chat');
  var listEl = document.getElementById('task-history-list');
  var btn = document.getElementById('history-btn');

  if (showingHistory) {
    chatEl.style.display = 'none';
    listEl.style.display = 'block';
    btn.textContent = '✕';
    btn.title = '关闭历史';
    historyOffset = 0;
    historyHasMore = true;
    loadHistoryList();
  } else {
    chatEl.style.display = 'block';
    listEl.style.display = 'none';
    btn.textContent = '⏱';
    btn.title = '历史记录';
  }
}

function loadHistoryList(append) {
  if (!activeDeviceId) return;
  var listEl = document.getElementById('task-history-list');
  if (!append) listEl.innerHTML = '<div style="text-align:center;padding:24px;opacity:.3;">加载中...</div>';

  var apiBase = location.protocol + '//' + location.host;
  fetch(apiBase + '/api/devices/' + encodeURIComponent(activeDeviceId) + '/tasks?limit=' + historyLimit + '&offset=' + historyOffset, {
    headers: {'Authorization': 'Bearer ' + localStorage.getItem('nftouch_token')}
  }).then(function(r) { return r.json(); }).then(function(tasks) {
    if (!append) listEl.innerHTML = '';
    if (tasks.length === 0 && !append) {
      listEl.innerHTML = '<div style="text-align:center;padding:24px;opacity:.3;">暂无任务记录</div>';
      return;
    }
    if (Array.isArray(tasks)) tasks.forEach(function(t) {
      var time = t.created_at ? t.created_at.substring(5, 16).replace('T', ' ') : '';
      var div = document.createElement('div');
      div.className = 'history-item';
      div.innerHTML = '<div style="display:flex;align-items:center;gap:4px;">' +
        '<div class="history-item-content" style="flex:1;min-width:0;cursor:pointer;">' +
          '<div class="prompt">' + escHtml(t.prompt) + '</div>' +
          '<div class="meta">' + time + '</div>' +
        '</div>' +
        '<span class="copy-icon" title="复制"></span>' +
        '<span class="trash-icon" title="删除"></span>' +
      '</div>';
      div.querySelector('.history-item-content').onclick = function() { showTaskDetail(t); };
      div.querySelector('.copy-icon').onclick = function(e) { e.stopPropagation(); copyTask(t); };
      div.querySelector('.trash-icon').onclick = function(e) { e.stopPropagation(); deleteTask(t.id); };
      listEl.appendChild(div);
    });
    historyOffset += tasks.length || 0;
    historyHasMore = (tasks.length || 0) >= historyLimit;

    var oldBtn = listEl.querySelector('.load-more');
    if (oldBtn) oldBtn.remove();
    if (historyHasMore) {
      var btn = document.createElement('div');
      btn.className = 'load-more';
      btn.textContent = '↓ 加载更多';
      btn.onclick = function() { loadHistoryList(true); };
      listEl.appendChild(btn);
    }
  });
}

function copyTask(t) {
  var text = '任务: ' + t.prompt + '\n时间: ' + (t.created_at || '') + '\n';
  if (t.result) {
    text += '----------------------------\n';
    t.result.split('\n---\n').forEach(function(line) {
      text += line.trim() + '\n';
    });
  }
  navigator.clipboard.writeText(text).then(function() {
    toast('已复制', 'success');
  }).catch(function() {
    // fallback for older browsers
    var ta = document.createElement('textarea');
    ta.value = text;
    document.body.appendChild(ta);
    ta.select();
    document.execCommand('copy');
    document.body.removeChild(ta);
    toast('已复制', 'success');
  });
}

function deleteTask(taskId) {
  if (!confirm('确定要删除这条任务记录吗？')) return;
  var apiBase = location.protocol + '//' + location.host;
  var token = localStorage.getItem('nftouch_token');
  fetch(apiBase + '/api/devices/' + encodeURIComponent(activeDeviceId) + '/tasks/' + taskId, {
    method: 'DELETE',
    headers: {'Authorization': 'Bearer ' + token}
  }).then(function(r) {
    if (r.ok) {
      historyOffset = 0;
      historyHasMore = true;
      loadHistoryList();
    }
  });
}

function showTaskDetail(task) {
  showingHistory = false;
  document.getElementById('task-chat').style.display = 'block';
  document.getElementById('task-history-list').style.display = 'none';
  document.getElementById('history-btn').textContent = '⏱';

  var el = document.getElementById('task-chat');
  el.innerHTML = '';
  var time = task.created_at ? task.created_at.substring(11, 16) : '';
  appendChatMessage('user', task.prompt, time);
  if (task.result) {
    task.result.split('\n---\n').forEach(function(line) {
      var timeMatch = line.match(/^\[(\d{2}:\d{2}:\d{2})\]/);
      var t = timeMatch ? timeMatch[1] : '';
      var text = line.replace(/^\[\d{2}:\d{2}:\d{2}\]\s*/, '');
      if (text.trim()) appendChatMessage('bot', text.trim(), t);
    });
  }
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
