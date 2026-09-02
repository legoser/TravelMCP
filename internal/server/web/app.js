const $=s=>document.getElementById(s);
function toast(m,err){const t=$('toast');t.textContent=m;t.style.background=err?'#991b1b':'#111827';t.classList.add('show');setTimeout(()=>t.classList.remove('show'),2600)}
async function copyKey(text){try{await navigator.clipboard.writeText(text);toast('Скопировано: '+text.slice(0,12)+'…')}catch(e){const ta=document.createElement('textarea');ta.value=text;ta.style.position='fixed';ta.style.opacity='0';document.body.appendChild(ta);ta.select();try{document.execCommand('copy');toast('Скопировано')}catch(ex){toast('Копирование не поддерживается',true)}ta.remove()} }
function showLastKey(key, scopes){const el=$('lastKey');if(!el) return;el.classList.remove('hidden');el.innerHTML='<b>Новый ключ ('+scopes+'):</b> <code style="word-break:break-all">'+key+'</code> <button class="secondary" style="margin-left:8px;padding:4px 8px" onclick="copyKey(\''+key+'\')">⎘ Копировать</button> <span class="muted">— скопируйте сейчас, повторно не покажется полным</span>'; el.scrollIntoView({behavior:'smooth',block:'nearest'})}
function token(){return localStorage.getItem('travelmcp_token')||''}
async function saveToken(){const v=$('token').value.trim();if(v) localStorage.setItem('travelmcp_token',v); else localStorage.removeItem('travelmcp_token');await updateWho();loadUsers();toast('Токен сохранён')}
async function clearToken(){localStorage.removeItem('travelmcp_token');$('token').value='';await updateWho();toast('Выход')}
function authHeaders(h={}){const t=token();if(t) h['Authorization']='Bearer '+t;h['Content-Type']='application/json';return h}
async function updateWho(){
  const t=token();$('token').value=t;
  const who=$('who'), banner=$('authBanner'), detail=$('authDetail'), card=$('authCard');
  if(!t){who.textContent='нет токена';who.className='badge blocked';if(card) card.style.borderLeftColor='var(--bad)';if(banner) banner.innerHTML='<b>⛔ Не авторизован</b> — вставьте ADMIN_TOKEN сверху или войдите по email/паролю. <span class="muted">Первый админ: <code>ADMIN_TOKEN=secret</code> в .env → перезапуск → вставьте <code>secret</code>.</span>';if(detail) detail.textContent='';setTabsDisabled(true);return}
  who.textContent=t.slice(0,14)+'…';who.className='badge pending';
  try{
    const me=await api('/api/v1/me');
    who.textContent=me.email+' ('+me.role+')';who.className='badge '+(me.role==='admin'?'admin':'active');
    if(card) card.style.borderLeftColor= me.role==='admin' ? 'var(--ok)' : 'var(--accent)';
    if(banner) banner.innerHTML='✅ Авторизован как <b>'+me.email+'</b> — роль <span class="badge '+me.role+'">'+me.role+'</span> статус <span class="badge '+me.status+'">'+me.status+'</span>';
    if(detail) detail.textContent='Токен scope проверен • '+new Date().toLocaleTimeString();
    setTabsDisabled(false);
  }catch(e){
    who.textContent='неверный токен';who.className='badge blocked';
    if(card) card.style.borderLeftColor='var(--bad)';
    if(banner) banner.innerHTML='⛔ Токен недействителен — '+e.message+' <span class="muted">Проверьте ADMIN_TOKEN или войдите заново.</span>';
    if(detail) detail.textContent='';
    setTabsDisabled(true);
  }
}
function setTabsDisabled(dis){
  document.querySelectorAll('.tab').forEach(el=>{el.style.opacity=dis?'0.45':'';el.style.pointerEvents=dis?'none':''});
  document.querySelectorAll('#tab-users button, #tab-keys button, #tab-config button, #tab-dash button').forEach(b=>{b.disabled=dis});
}
function showTab(name){document.querySelectorAll('.tab').forEach((el,i)=>el.classList.toggle('active',['users','keys','config','dash'][i]===name));['users','keys','config','dash'].forEach(n=>$('tab-'+n).classList.toggle('hidden', n!==name));if(name==='dash')loadDash();if(name==='config')loadConfig()}
async function api(path,opts={}){opts.headers=authHeaders(opts.headers||{});const r=await fetch(path,opts);let j;try{j=await r.json()}catch(e){j={raw:await r.text()}};if(!r.ok) throw new Error((j.error||j.message||r.status)+' '+(j.status||''));return j}
async function doLogin(){try{const j=await api('/api/v1/login',{method:'POST',body:JSON.stringify({email:$('loginEmail').value,password:$('loginPass').value})});localStorage.setItem('travelmcp_token',j.token);await updateWho();$('loginMsg').textContent='Вход OK, token '+j.token.slice(0,16)+'…';toast('Вход выполнен');loadUsers()}catch(e){$('loginMsg').textContent='Ошибка: '+e.message;toast(e.message,true)}}
async function doRegister(){try{const j=await api('/api/v1/register',{method:'POST',body:JSON.stringify({email:$('loginEmail').value,password:$('loginPass').value})});$('loginMsg').textContent='Регистрация: '+j.status+' id='+j.id;toast('Зарегистрирован '+j.email);loadUsers()}catch(e){$('loginMsg').textContent='Ошибка: '+e.message;toast(e.message,true)}}
function fmtTime(ts){if(!ts) return '—';const d=new Date(ts*1000);return d.toLocaleString()}
async function loadUsers(){try{const j=await api('/api/v1/users');const tb=$('usersBody');tb.innerHTML='';j.forEach(u=>{const tr=document.createElement('tr');tr.innerHTML='<td>'+u.id+'</td><td>'+u.email+'</td><td><span class="badge '+u.status+'">'+u.status+'</span></td><td><span class="badge '+u.role+'">'+u.role+'</span></td><td>'+fmtTime(u.created_at)+'</td><td style="max-width:200px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap" title="'+(u.config||'')+'">'+(u.config?u.config.slice(0,40):'<span class=muted>—</span>')+'</td><td><div class=row style="gap:4px"><button class=secondary onclick="quick(\''+u.id+'\',\'active\')">✓</button><button class=secondary onclick="quick(\''+u.id+'\',\'blocked\')">⊘</button><button onclick="makeAdmin(\''+u.id+'\')">admin</button><button class=danger onclick="delUser(\''+u.id+'\')">✕</button></div></td>';tb.appendChild(tr)});if(j.length===0) tb.innerHTML='<tr><td colspan=7 class=muted>пользователей нет</td></tr>'}catch(e){toast('users: '+e.message,true);$('usersBody').innerHTML='<tr><td colspan=7 class=muted>'+e.message+'</td></tr>'}}
async function quick(id,status){try{await api('/api/v1/users/'+id+'/moderate',{method:'POST',body:JSON.stringify({status})});toast('Статус '+status);loadUsers()}catch(e){toast(e.message,true)}}
async function makeAdmin(id){try{await api('/api/v1/users/'+id,{method:'PATCH',body:JSON.stringify({role:'admin',status:'active'})});toast('Роль admin');loadUsers()}catch(e){toast(e.message,true)}}
async function delUser(id){if(!id) id=$('editUserId').value;if(!id) return toast('Укажите ID',true);if(!confirm('Удалить пользователя '+id+'?')) return;try{await api('/api/v1/users/'+id,{method:'DELETE'});toast('Удалён');loadUsers()}catch(e){toast(e.message,true)}}
async function patchUser(){const id=$('editUserId').value;if(!id) return toast('ID?',true);const body={};if($('editStatus').value) body.status=$('editStatus').value;if($('editRole').value) body.role=$('editRole').value;if($('editCfg').value) body.config=$('editCfg').value;if(Object.keys(body).length===0) return toast('Нечего обновлять',true);try{await api('/api/v1/users/'+id,{method:'PATCH',body:JSON.stringify(body)});toast('Обновлён');loadUsers()}catch(e){toast(e.message,true)}}
async function loadMyKeys(){try{const j=await api('/api/v1/keys');renderKeys(j)}catch(e){toast(e.message,true)}}
async function loadKeysForUser(){const id=$('keysUserId').value||$('editUserId').value;if(!id) return toast('user ID?',true);try{const j=await api('/api/v1/users/'+id+'/keys');renderKeys(j)}catch(e){toast(e.message,true)}}
function renderKeys(arr){const tb=$('keysBody');tb.innerHTML='';(arr||[]).forEach(k=>{const tr=document.createElement('tr');tr.innerHTML='<td>'+k.ID+'</td><td>'+k.UserID+'</td><td style="max-width:220px;overflow:hidden;text-overflow:ellipsis" title="'+k.Key+'"><span>'+k.Key.slice(0,18)+'…</span> <button class="secondary" style="padding:4px 8px" onclick="copyKey(\''+k.Key+'\')" title="Копировать полный ключ">⎘</button></td><td>'+k.Scopes+'</td><td>'+fmtTime(k.CreatedAt)+'</td><td>'+fmtTime(k.LastUsed)+'</td><td><button class=danger onclick="delKey('+k.ID+','+k.UserID+')">✕</button></td>';tb.appendChild(tr)});if((arr||[]).length===0) tb.innerHTML='<tr><td colspan=7 class=muted>ключей нет</td></tr>'}
async function createKey(){const scopes=$('newKeyScopes').value||'mcp:read';try{const j=await api('/api/v1/keys',{method:'POST',body:JSON.stringify({scopes})});toast('Ключ '+j.Key.slice(0,12)+'…');showLastKey(j.Key, j.Scopes);loadMyKeys()}catch(e){toast(e.message,true)}}
async function createUserKey(){const id=$('keysUserId').value||$('editUserId').value;if(!id) return toast('user ID?',true);const scopes=$('newKeyScopes').value||'mcp:read';try{const j=await api('/api/v1/users/'+id+'/keys',{method:'POST',body:JSON.stringify({scopes})});toast('Ключ для '+id+' '+j.Key.slice(0,12));showLastKey(j.Key, j.Scopes);loadKeysForUser()}catch(e){toast(e.message,true)}}
async function delKey(id,uid){try{const path=uid? '/api/v1/users/'+uid+'/keys/'+id : '/api/v1/keys/'+id;await api(path,{method:'DELETE'});toast('Ключ удалён');loadMyKeys()}catch(e){toast(e.message,true)}}
async function loadConfig(){try{const j=await api('/api/v1/config');$('cfgOut').textContent=JSON.stringify(j,null,2);$('cfgProviders').value=(j.providers.enabled||[]).join(',');$('cfgEngine').value=j.planner.engine||'csa';$('cfgLog').value=j.log.level||'info';$('cfgLevels').value=j.log.levels?JSON.stringify(j.log.levels):''}catch(e){$('cfgOut').textContent='Ошибка: '+e.message;toast(e.message,true)}}
async function saveConfig(){let levels={};try{levels=$('cfgLevels').value?JSON.parse($('cfgLevels').value):{}}catch(e){return toast('levels JSON неверен',true)}const body={providers:{enabled:$('cfgProviders').value.split(',').map(s=>s.trim()).filter(Boolean)},planner:{engine:$('cfgEngine').value},log:{level:$('cfgLog').value,levels}};try{const j=await api('/api/v1/config',{method:'PUT',body:JSON.stringify(body)});toast('Конфиг сохранён');$('cfgOut').textContent=JSON.stringify(j,null,2)}catch(e){toast(e.message,true)}}
async function loadDash(){try{const j=await api('/api/v1/dashboard');$('provOut').textContent=JSON.stringify(j.providers||j,null,2);$('metricsOut').textContent=JSON.stringify(j.counters||j,null,2)}catch(e){$('provOut').textContent='Ошибка: '+e.message} try{const p=await api('/api/v1/providers');if($('provOut').textContent==='—')$('provOut').textContent=JSON.stringify(p,null,2)}catch(e){}}
(async()=>{await updateWho();loadUsers();loadDash();})();
