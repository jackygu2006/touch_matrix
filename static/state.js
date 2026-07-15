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
    window._profileData = data;  // Save for language switch re-render
    if (data.role === 'admin') {
      document.getElementById('sidebar-footer').innerHTML += '<button class="sidebar-btn" onclick="showAdminPanel()" data-i18n="sidebar.user_management">' + __('sidebar.user_management') + '</button>';
    }
    // Insert version + language switch below the admin button
    document.getElementById('sidebar-footer').innerHTML += '<div id="server-version" style="padding:8px 0 0 0;font-size:10px;color:var(--text2);text-align:center;display:flex;align-items:center;justify-content:center;gap:8px;">' +
      '<span id="version-text"></span>' +
      '<span id="lang-switch" style="cursor:pointer;padding:1px 6px;border:1px solid var(--border);border-radius:4px;font-size:10px;line-height:1.4;" title="Switch language">ZH</span>' +
      '</div>';
    var ui = document.getElementById('user-info');
    ui.style.display = 'block';
    renderUserInfo();
  } catch(e) { location.href = '/login.html'; return; }

  // Fetch server version
  fetch('/api/version').then(r => r.json()).then(d => {
    var el = document.getElementById('version-text');
    if (el && d.version) el.textContent = 'v' + d.version;
  }).catch(function(){});

  // Language switch handler
  document.getElementById('lang-switch').onclick = function() {
    var current = localStorage.getItem('nftouch_lang') || (navigator.language.startsWith('zh') ? 'zh' : 'en');
    var next = current === 'zh' ? 'en' : 'zh';
    __setLang(next);
    this.textContent = next === 'zh' ? 'ZH' : 'EN';
  };
  // Set initial language switch text
  (function() {
    var lang = localStorage.getItem('nftouch_lang') || (navigator.language.startsWith('zh') ? 'zh' : 'en');
    var sw = document.getElementById('lang-switch');
    if (sw) sw.textContent = lang === 'zh' ? 'ZH' : 'EN';
  })();

  // connect() is called at end of ui.js
})();

function renderUserInfo() {
  var data = window._profileData;
  if (!data) return;
  var ui = document.getElementById('user-info');
  if (!ui) return;
  var txt = (data.nickname || data.email);
  if (data.role !== 'admin' && data.max_devices) txt += ' · ' + data.max_devices + __('sidebar.quota');
  if (data.role === 'admin') txt += ' · ' + __('sidebar.admin');
  ui.textContent = txt;
}

function toast(msg, style) {
  var el = document.getElementById('status-left');
  var prev = el.textContent;
  el.textContent = msg;
  el.style.color = style === 'error' ? '#f87171' : style === 'success' ? '#22c55e' : '';
  clearTimeout(el._timeout);
  el._timeout = setTimeout(function() { el.textContent = prev; el.style.color = ''; }, 3000);
}


// ============================================================
// WebSocket
// ============================================================
function connect() {
  updateStatus('connecting', __('status.connecting'));
  ws = new WebSocket(wsUrl);
  ws.binaryType = 'arraybuffer';

  ws.onopen = () => {
    updateStatus('connected', __('status.connected'));
    // Fetch device list via REST API as fallback
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
          if (typeof handleTaskStatus === 'function') handleTaskStatus(msg);
          break;
        case 'error':
          toast(__('status.error') + ': ' + msg.reason, 'error');
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
            // Sync CSS aspect ratio with actual frame size to prevent click offset in Grid mode
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
          // Align internal canvas coordinate system with device resolution
          canvas.width = devW;
          canvas.height = devH;
          // Maintain aspect ratio to fit container
          var container = document.getElementById('screen-canvas-area');
          var scale = Math.min(container.clientWidth / devW, container.clientHeight / devH);
          canvas.style.width = (devW * scale) + 'px';
          canvas.style.height = (devH * scale) + 'px';
          ctx.drawImage(img, 0, 0, devW, devH);
          URL.revokeObjectURL(url);
        };
        img.src = url;
        if (!window._hasActiveWarning) {
          document.getElementById('screen-placeholder').classList.add('hidden');
          canvas.classList.remove('hidden');
        }
      }
      } // close pendingFrameHeader if
      pendingFrameHeader = null;
    }
  };

  ws.onclose = () => {
    updateStatus('disconnected', __('status.disconnected'));
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
// Draggable resize handle
// ============================================================
(function() {
  var dragging = null;
  var startX = 0;
  var startW = 0;

  function initHandle(id, targetId, rightSide, minW, maxW, storageKey) {
    var handle = document.getElementById(id);
    var target = document.getElementById(targetId);
    if (!handle || !target) return;

    // Restore last saved size
    var saved = localStorage.getItem(storageKey);
    if (saved) {
      var w = parseInt(saved);
      if (w >= minW && w <= maxW) target.style.width = w + 'px';
    }

    handle.addEventListener('mousedown', function(e) {
      e.preventDefault();
      dragging = { handle: handle, target: target, rightSide: rightSide, minW: minW, maxW: maxW, key: storageKey };
      startX = e.clientX;
      startW = target.offsetWidth;
      handle.classList.add('active');
      document.body.style.cursor = 'col-resize';
      document.body.style.userSelect = 'none';
    });
  }

  document.addEventListener('mousemove', function(e) {
    if (!dragging) return;
    var dx = e.clientX - startX;
    var newW = dragging.rightSide ? startW - dx : startW + dx;
    newW = Math.max(dragging.minW, Math.min(dragging.maxW, newW));
    dragging.target.style.width = newW + 'px';
  });

  document.addEventListener('mouseup', function() {
    if (!dragging) return;
    dragging.handle.classList.remove('active');
    localStorage.setItem(dragging.key, dragging.target.offsetWidth);
    document.body.style.cursor = '';
    document.body.style.userSelect = '';
    dragging = null;
  });

  // Handle 1: sidebar(left) vs main(right), drag to resize sidebar width
  initHandle('resize-sidebar', 'sidebar', false, 180, 500, 'nftouch_sidebar_w');

  // Handle 2: screen-canvas-area(left) vs task-panel(right), drag to resize task-panel width
  initHandle('resize-task', 'task-panel', true, 200, 600, 'nftouch_task_w');
})();
