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
let mobileFullscreen = false;
let mobileFabExpanded = false;

function isMobileViewport() {
  return window.matchMedia('(max-width: 820px)').matches;
}

function resizeMainCanvas() {
  if (!canvas || !devW || !devH) return;
  var container = document.getElementById('screen-canvas-area');
  if (!container) return;
  var rect = container.getBoundingClientRect();
  var width = Math.floor(rect.width || container.clientWidth || 0);
  var height = Math.floor(rect.height || container.clientHeight || 0);
  if (!height && container.parentElement) {
    height = Math.floor(container.parentElement.clientHeight || 0);
  }
  if (!width && container.parentElement) {
    width = Math.floor(container.parentElement.clientWidth || 0);
  }
  if (!width || !height) return;
  var scale = Math.min(width / devW, height / devH);
  if (!isFinite(scale) || scale <= 0) return;
  canvas.style.width = Math.max(2, Math.floor(devW * scale)) + 'px';
  canvas.style.height = Math.max(2, Math.floor(devH * scale)) + 'px';
}

function resetMobileFabPosition() {
  var fab = document.getElementById('mobile-fab');
  if (!fab) return;
  fab.style.left = '';
  fab.style.top = '18px';
  fab.style.right = '18px';
  fab.style.bottom = '';
}

function updateMobileFab() {
  var fab = document.getElementById('mobile-fab');
  var actions = document.getElementById('mobile-fab-actions');
  var toggle = document.getElementById('mobile-fab-toggle');
  if (!fab || !actions || !toggle) return;
  var show = mobileFullscreen && isMobileViewport();
  fab.classList.toggle('hidden', !show);
  actions.classList.toggle('hidden', !show || !mobileFabExpanded);
  toggle.textContent = mobileFabExpanded ? 'X' : 'O';
  toggle.classList.toggle('is-expanded', mobileFabExpanded);
  toggle.classList.toggle('is-collapsed', !mobileFabExpanded);
}

function updateMobileFullscreenButtons() {
  var enterBtn = document.getElementById('fullscreen-toggle');
  var canShow = isMobileViewport();
  if (enterBtn) {
    enterBtn.style.display = canShow ? 'inline-flex' : 'none';
    enterBtn.textContent = mobileFullscreen ? __('control.fullscreen_exit_short') : __('control.fullscreen_enter_short');
    enterBtn.title = mobileFullscreen ? __('control.fullscreen_exit_title') : __('control.fullscreen_enter_title');
    enterBtn.style.borderColor = mobileFullscreen ? 'var(--accent)' : 'var(--border)';
    enterBtn.style.color = mobileFullscreen ? 'var(--accent)' : 'var(--text2)';
  }
  updateMobileFab();
}

function toggleMobileFabActions(forceExpanded) {
  if (typeof forceExpanded === 'boolean') {
    mobileFabExpanded = forceExpanded;
  } else {
    mobileFabExpanded = !mobileFabExpanded;
  }
  updateMobileFab();
}

function sendFullscreenQuickAction(action) {
  if (action === 'restore') {
    setMobileFullscreen(false);
    return;
  }
  if (action === 'home' || action === 'back') {
    sendKey(action);
    return;
  }
  if (action === 'up' || action === 'down' || action === 'left' || action === 'right' || action === 'notify') {
    sendGesture(action);
  }
}

function initMobileFab() {
  var fab = document.getElementById('mobile-fab');
  var toggle = document.getElementById('mobile-fab-toggle');
  var container = document.getElementById('screen-canvas-area');
  if (!fab || !toggle || !container || fab.dataset.bound === '1') return;
  fab.dataset.bound = '1';

  var drag = null;

  function getPoint(event) {
    var source = event.touches && event.touches[0] ? event.touches[0] : event;
    return { x: source.clientX, y: source.clientY };
  }

  function clampPosition(left, top) {
    var bounds = container.getBoundingClientRect();
    var maxLeft = Math.max(8, bounds.width - fab.offsetWidth - 8);
    var maxTop = Math.max(8, bounds.height - fab.offsetHeight - 8);
    return {
      left: Math.min(Math.max(8, left), maxLeft),
      top: Math.min(Math.max(8, top), maxTop)
    };
  }

  function handleMove(event) {
    if (!drag) return;
    var point = getPoint(event);
    var nextLeft = drag.startLeft + (point.x - drag.originX);
    var nextTop = drag.startTop + (point.y - drag.originY);
    var pos = clampPosition(nextLeft, nextTop);
    fab.style.left = pos.left + 'px';
    fab.style.top = pos.top + 'px';
    fab.style.right = 'auto';
    fab.style.bottom = 'auto';
    if (Math.abs(point.x - drag.originX) > 6 || Math.abs(point.y - drag.originY) > 6) {
      drag.moved = true;
    }
    event.preventDefault();
  }

  function handleEnd(event) {
    if (!drag) return;
    var moved = drag.moved;
    drag = null;
    document.removeEventListener('mousemove', handleMove);
    document.removeEventListener('mouseup', handleEnd);
    document.removeEventListener('touchmove', handleMove);
    document.removeEventListener('touchend', handleEnd);
    document.removeEventListener('touchcancel', handleEnd);
    if (!moved) {
      toggleMobileFabActions();
      if (event) event.preventDefault();
    }
  }

  function handleStart(event) {
    if (!mobileFullscreen || !isMobileViewport()) return;
    var point = getPoint(event);
    var bounds = container.getBoundingClientRect();
    var rect = fab.getBoundingClientRect();
    drag = {
      originX: point.x,
      originY: point.y,
      startLeft: rect.left - bounds.left,
      startTop: rect.top - bounds.top,
      moved: false
    };
    document.addEventListener('mousemove', handleMove);
    document.addEventListener('mouseup', handleEnd);
    document.addEventListener('touchmove', handleMove, { passive: false });
    document.addEventListener('touchend', handleEnd, { passive: false });
    document.addEventListener('touchcancel', handleEnd, { passive: false });
    event.preventDefault();
  }

  toggle.addEventListener('mousedown', handleStart);
  toggle.addEventListener('touchstart', handleStart, { passive: false });
}

function setMobileFullscreen(next) {
  if (next && !activeDeviceId) {
    toast(__('common.select_device_first'), 'error');
    return;
  }
  if (!isMobileViewport()) {
    mobileFullscreen = false;
    mobileFabExpanded = false;
    document.body.classList.remove('mobile-single-fullscreen');
    updateMobileFullscreenButtons();
    resizeMainCanvas();
    return;
  }
  if (next && gridMode && typeof toggleGrid === 'function') {
    toggleGrid();
  }
  mobileFullscreen = !!next;
  mobileFabExpanded = false;
  document.body.classList.toggle('mobile-single-fullscreen', mobileFullscreen);
  if (mobileFullscreen) resetMobileFabPosition();
  updateMobileFullscreenButtons();
  requestAnimationFrame(function() {
    requestAnimationFrame(resizeMainCanvas);
  });
}

function toggleMobileFullscreen() {
  setMobileFullscreen(!mobileFullscreen);
}

function getFrameBlob(arrayBuffer, frameHeader) {
  const bytes = new Uint8Array(arrayBuffer);
  const payload = arrayBuffer.byteLength > 4 && bytes[0] !== 0xFF ? arrayBuffer.slice(4) : arrayBuffer;
  const mime = (frameHeader && frameHeader.mime) || 'image/jpeg';
  return new Blob([payload], {type: mime});
}

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
  initMobileFab();
  updateMobileFullscreenButtons();

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
  const currentWs = new WebSocket(wsUrl);
  ws = currentWs;
  currentWs.binaryType = 'arraybuffer';

  currentWs.onopen = () => {
    if (ws !== currentWs) return;
    updateStatus('connected', __('status.connected'));
    // Fetch device list via REST API as fallback
    fetch('/api/devices', {headers:{'Authorization':'Bearer '+localStorage.getItem('nftouch_token')}})
      .then(r=>r.json()).then(d=>{if(Array.isArray(d)){devices=d;if(typeof renderDeviceList==='function')renderDeviceList();}})
      .catch(function(){});
  };

  let pendingFrameHeader = null;

  currentWs.onmessage = (e) => {
    if (ws !== currentWs) return;
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
          const blob = getFrameBlob(e.data, pendingFrameHeader);
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
        const blob = getFrameBlob(e.data, pendingFrameHeader);
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
          resizeMainCanvas();
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

  currentWs.onclose = () => {
    if (ws !== currentWs) return;
    ws = null;
    updateStatus('disconnected', __('status.disconnected'));
    setTimeout(function() {
      if (!ws) connect();
    }, 5000);
  };

  currentWs.onerror = () => {
    if (ws !== currentWs) return;
  };
}

function send(msg) {
  if (ws && ws.readyState === WebSocket.OPEN) {
    ws.send(JSON.stringify(msg));
  }
}

function updateStatus(_status, text) {
  if (!activeDeviceId) {
    document.getElementById('status-left').textContent = text;
  }
}

window.addEventListener('resize', function() {
  if (typeof window.syncResizablePaneWidths === 'function') {
    window.syncResizablePaneWidths();
  }
  if (!isMobileViewport() && mobileFullscreen) {
    mobileFullscreen = false;
    mobileFabExpanded = false;
    document.body.classList.remove('mobile-single-fullscreen');
  }
  updateMobileFullscreenButtons();
  requestAnimationFrame(resizeMainCanvas);
});

// ============================================================
// Draggable resize handle
// ============================================================
(function() {
  var dragging = null;
  var startX = 0;
  var startW = 0;
  var paneConfigs = [];

  function syncResizablePaneWidths() {
    paneConfigs.forEach(function(cfg) {
      if (!cfg.target) return;
      if (isMobileViewport()) {
        cfg.target.style.width = '';
        return;
      }
      var saved = localStorage.getItem(cfg.key);
      if (!saved) {
        cfg.target.style.width = '';
        return;
      }
      var w = parseInt(saved, 10);
      if (w >= cfg.minW && w <= cfg.maxW) {
        cfg.target.style.width = w + 'px';
      } else {
        cfg.target.style.width = '';
      }
    });
  }

  window.syncResizablePaneWidths = syncResizablePaneWidths;

  function initHandle(id, targetId, rightSide, minW, maxW, storageKey) {
    var handle = document.getElementById(id);
    var target = document.getElementById(targetId);
    if (!handle || !target) return;
    paneConfigs.push({ target: target, minW: minW, maxW: maxW, key: storageKey });

    syncResizablePaneWidths();

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

  syncResizablePaneWidths();
})();
