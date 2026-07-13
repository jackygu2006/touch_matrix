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
let offsetX = 0, offsetY = 0;
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
    // 版本号插入到 admin 按钮下方
    document.getElementById('sidebar-footer').innerHTML += '<div id="server-version" style="padding:8px 0 0 0;font-size:10px;color:var(--text2);text-align:center;"></div>';
    var ui = document.getElementById('user-info');
  ui.style.display = 'block';
  var txt = (data.nickname || data.email);
  if (data.role !== 'admin' && data.max_devices) txt += ' · ' + data.max_devices + '台配额';
  if (data.role === 'admin') txt += ' · 管理员';
  ui.textContent = txt;
  } catch(e) { location.href = '/login.html'; return; }

  // 拉取服务端版本号
  fetch('/api/version').then(r => r.json()).then(d => {
    var el = document.getElementById('server-version');
    if (el && d.version) el.textContent = 'v' + d.version;
  }).catch(function(){});

  // connect() is called at end of ui.js
})();

function toast(msg, style) {
  var el = document.getElementById('task-msg');
  el.style.display = 'block';
  el.textContent = msg;
  el.style.color = style === 'error' ? '#f87171' : style === 'success' ? '#22c55e' : 'var(--text2)';
  clearTimeout(el._timeout);
  el._timeout = setTimeout(function() { el.style.display = 'none'; }, 3000);
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
            // 同步 CSS 宽高比与实际帧尺寸，防止 Grid 模式点击偏移
            if (dc.img.naturalHeight > 0) {
              dc.canvas.style.aspectRatio = (dc.img.naturalWidth / dc.img.naturalHeight).toString();
            }
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
          var container = document.getElementById('screen-canvas-area');
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
