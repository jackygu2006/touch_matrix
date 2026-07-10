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
  document.cookie = 'nftouch_token=;path=/;max-age=0';
  location.replace('/login.html');
}
