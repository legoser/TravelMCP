const $=s=>document.getElementById(s);
function toast(m,err){const t=$('toast');t.textContent=m;t.style.background=err?'#991b1b':'#111827';t.classList.add('show');setTimeout(()=>t.classList.remove('show'),2600)}
async function copyKey(text){try{await navigator.clipboard.writeText(text);toast('Скопировано: '+text.slice(0,12)+'…')}catch(e){const ta=document.createElement('textarea');ta.value=text;ta.style.position='fixed';ta.style.opacity='0';document.body.appendChild(ta);ta.select();try{document.execCommand('copy');toast('Скопировано')}catch(ex){toast('Копирование не поддерживается',true)}ta.remove()} }
function showLastKey(key, scopes){const el=$('lastKey');if(!el) return;el.classList.remove('hidden');el.innerHTML='<b>Новый ключ ('+scopes+'):</b> <code style="word-break:break-all">'+key+'</code> <button class="secondary" style="margin-left:8px;padding:4px 8px" onclick="copyKey(\''+key+'\')">⎘ Копировать</button> <span class="muted">— скопируйте сейчас, повторно не покажется полным</span>'; el.scrollIntoView({behavior:'smooth',block:'nearest'})}
function token(){return localStorage.getItem('travelmcp_token')||''}
async function saveToken(){const v=$('token').value.trim();if(v) localStorage.setItem('travelmcp_token',v); else localStorage.removeItem('travelmcp_token');await updateWho();loadUsers();toast('Токен сохранён')}
async function clearToken(){localStorage.removeItem('travelmcp_token');$('token').value='';await updateWho();toast('Выход')}
function authHeaders(h={}){const t=token();if(t) h['Authorization']='Bearer '+t;h['Content-Type']='application/json';return h}
function authHeadersNoJson(h={}){const t=token();if(t) h['Authorization']='Bearer '+t;return h}
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
  document.querySelectorAll('#tab-users button, #tab-keys button, #tab-config button, #tab-dash button, #tab-collect button, #tab-runs button, #tab-imports button, #tab-terminals button, #tab-schedules button, #tab-review button, #tab-external button, #tab-quotas button').forEach(b=>{b.disabled=dis});
}
function showTab(name){const all=['users','keys','config','dash','collect','runs','imports','terminals','schedules','review','external','quotas'];document.querySelectorAll('.tab').forEach(el=>el.classList.toggle('active', el.getAttribute('onclick').includes("'"+name+"'")));all.forEach(n=>{const e=$('tab-'+n);if(e) e.classList.toggle('hidden', n!==name)});if(name==='dash')loadDash();if(name==='config')loadConfig();if(name==='collect')loadCollectRegions();if(name==='runs')loadRuns();if(name==='imports')loadImports();if(name==='review')loadReview();if(name==='quotas'){loadQuotas();loadAudit()} if(name==='terminals')loadTerminals(); if(name==='schedules')loadRoutes();}
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
let _cfgInit=null;
async function loadConfig(){try{const j=await api('/api/v1/config');_cfgInit=JSON.parse(JSON.stringify(j));$('cfgOut').textContent=JSON.stringify(j,null,2);$('cfgProviders').value=(j.providers.enabled||[]).join(',');$('cfgEngine').value=j.planner.engine||'csa';$('cfgLog').value=j.log.level||'info';$('cfgLevels').value=j.log.levels?JSON.stringify(j.log.levels):''; $('cfgStore').value=j.store.dsn||''; $('cfgCache').value=(j.cache.kind||'memory')+' / '+(j.cache.ttl||''); $('cfgMotis').value=j.motis.url||''; $('cfgGeocoderKind').value=j.geocoder.kind||''; $('cfgNominatim').value=j.nominatim.url||''; $('cfgYandex').value=(j.yandex.geocode_url||'')+' '+(j.yandex.rasp_key||''); $('cfgSem').value=(j.planner.semaphore_size||0)+' , '+(j.planner.semaphore_enable?'true':'false'); $('cfgVerif').value=(j.verification.confidence_threshold||'')+' / '+(j.verification.distance_m||'')+' / '+(j.verification.strong_distance_m||'')+' / '+(j.verification.lev_threshold||''); $('cfgDedup').value=j.deduplication?j.deduplication.distance_m||'':''; $('cfgPricing').value=j.pricing?j.pricing.default_currency||'':''}catch(e){$('cfgOut').textContent='Ошибка: '+e.message;toast(e.message,true)}}
async function saveConfig(){
  if(!_cfgInit) return toast('Сначала Загрузить',true);
  let levels={};try{levels=$('cfgLevels').value?JSON.parse($('cfgLevels').value):{}}catch(e){return toast('levels JSON неверен',true)}
  const cur={
    providers:{enabled:$('cfgProviders').value.split(',').map(s=>s.trim()).filter(Boolean)},
    planner:{engine:$('cfgEngine').value, semaphore_size: parseInt($('cfgSem').value.split(',')[0])||0, semaphore_enable: $('cfgSem').value.includes('true')},
    log:{level:$('cfgLog').value,levels},
    store:{dsn:$('cfgStore').value},
    motis:{url:$('cfgMotis').value},
    geocoder:{kind:$('cfgGeocoderKind').value},
    nominatim:{url:$('cfgNominatim').value},
    verification:(()=>{const p=$('cfgVerif').value.split('/').map(s=>s.trim());return {confidence_threshold:parseFloat(p[0])||undefined, distance_m:parseInt(p[1])||undefined, strong_distance_m:parseInt(p[2])||undefined, lev_threshold:parseFloat(p[3])||undefined}})(),
    deduplication:{distance_m:parseInt($('cfgDedup').value)||undefined},
    pricing:{default_currency:$('cfgPricing').value||undefined}
  };
  const body={};
  function diff(a,b,path){
    for(const k in b){
      if(b[k]==undefined||b[k]=='') continue;
      if(JSON.stringify(a[k])!==JSON.stringify(b[k])) body[k]=b[k];
    }
  }
  // build compare objects
  const initFlat={providers:{enabled:_cfgInit.providers.enabled}, planner:{engine:_cfgInit.planner.engine}, log:{level:_cfgInit.log.level,levels:_cfgInit.log.levels}, store:{dsn:_cfgInit.store.dsn}, motis:{url:_cfgInit.motis.url}, geocoder:{kind:_cfgInit.geocoder.kind}, nominatim:{url:_cfgInit.nominatim.url}};
  if(JSON.stringify(cur.providers.enabled)!==JSON.stringify(_cfgInit.providers.enabled)) body.providers={enabled:cur.providers.enabled};
  if(cur.planner.engine!==_cfgInit.planner.engine) body.planner={engine:cur.planner.engine};
  if(cur.log.level!==_cfgInit.log.level||JSON.stringify(levels)!==JSON.stringify(_cfgInit.log.levels)) body.log={level:cur.log.level,levels};
  if(cur.store.dsn && cur.store.dsn!==_cfgInit.store.dsn) body.store={dsn:cur.store.dsn};
  if(cur.motis.url && cur.motis.url!==_cfgInit.motis.url) body.motis={url:cur.motis.url};
  if(cur.geocoder.kind!==(_cfgInit.geocoder.kind||'')) body.geocoder={kind:cur.geocoder.kind};
  if(cur.nominatim.url!==_cfgInit.nominatim.url) body.nominatim={url:cur.nominatim.url};
  if(Object.keys(body).length===0) return toast('Нет изменений — нечего сохранять',true);
  try{const j=await api('/api/v1/config',{method:'PUT',body:JSON.stringify(body)});toast('Конфиг сохранён (дельта '+Object.keys(body).join(',')+')');$('cfgOut').textContent=JSON.stringify(j,null,2); _cfgInit=j} catch(e){toast(e.message,true)}
}
async function loadDash(){try{const j=await api('/api/v1/dashboard');$('provOut').textContent=JSON.stringify(j.providers||j,null,2);$('metricsOut').textContent=JSON.stringify(j.counters||j,null,2)}catch(e){$('provOut').textContent='Ошибка: '+e.message} try{const p=await api('/api/v1/providers');if($('provOut').textContent==='—')$('provOut').textContent=JSON.stringify(p,null,2)}catch(e){}}
async function loadImports(){try{const im=await api('/api/v1/admin/imports');const tb=$('importsBody');tb.innerHTML='';(im||[]).forEach(r=>{const tr=document.createElement('tr');tr.innerHTML='<td>'+r.ProviderID+'</td><td>'+fmtTime(r.At)+'</td><td>'+r.Records+'</td><td>'+r.Status+'</td><td style="max-width:160px;overflow:hidden;text-overflow:ellipsis">'+(r.Checksum||'').slice(0,12)+'</td>';tb.appendChild(tr)});if((im||[]).length===0) tb.innerHTML='<tr><td colspan="5" class="muted">импортов нет</td></tr>'}catch(e){$('importsBody').innerHTML='<tr><td colspan="5" class="muted">'+e.message+'</td></tr>'}; loadJobs(); loadLogs()}
async function loadJobs(){try{const j=await api('/api/v1/jobs');const tb=$('jobsBody');tb.innerHTML='';(j||[]).forEach(r=>{const tr=document.createElement('tr');const canReset=['dead','retry','running','pending'].includes(r.State);tr.innerHTML='<td>'+r.ID+'</td><td>'+r.Type+'</td><td>'+(r.Region||'—')+'</td><td><span class="badge '+r.State+'">'+r.State+'</span></td><td>'+r.Attempts+'</td><td>'+(r.NextRun||'—')+'</td><td style="max-width:200px;overflow:hidden;text-overflow:ellipsis" title="'+(r.LastError||'').replace(/"/g,'&quot;')+'">'+(r.LastError||'').slice(0,60)+'</td><td>'+(canReset?'<button class="secondary" onclick="resetJob('+r.ID+')" title="Перезапустить (attempts=0, pending)">↻</button>':'<span class="muted">—</span>')+'</td>';tb.appendChild(tr)});if((j||[]).length===0) tb.innerHTML='<tr><td colspan="8" class="muted">jobs нет</td></tr>'}catch(e){$('jobsBody').innerHTML='<tr><td colspan="8" class="muted">'+e.message+'</td></tr>'}}
async function resetJob(id){if(!confirm('Перезапустить job '+id+'? Попытки обнулятся, состояние → pending.')) return;try{const j=await api('/api/v1/jobs/'+id+'/reset',{method:'POST'});toast('Job '+j.id+' → pending');loadJobs()}catch(e){toast(e.message,true)}}
async function loadLogs(){try{const l=await api('/api/v1/admin/logs');$('logsOut').textContent=JSON.stringify(l,null,2)}catch(e){$('logsOut').textContent='Ошибка: '+e.message}}
async function enqueueMintrans(){try{const j=await api('/api/v1/import/mintrans',{method:'POST',body:'{}'});toast('mintrans job '+j.id);loadJobs()}catch(e){toast(e.message,true)}}
async function enqueueRail(){try{const j=await api('/api/v1/import/rail',{method:'POST',body:'{}'});toast('rail job '+j.id);loadJobs()}catch(e){toast(e.message,true)}}
async function enqueueGTFS(input){
  let file=null;
  if(input && input.files) file=input.files[0];
  else {const el=$('gtfsFile'); if(el && el.files) file=el.files[0];}
  if(file){
    const fd=new FormData(); fd.append('file',file);
    try{
      const headers=authHeadersNoJson({});
      const r=await fetch('/api/v1/import/gtfs',{method:'POST', headers, body:fd});
      let j; try{j=await r.json()}catch(e){j={raw:await r.text()}}
      if(!r.ok) throw new Error(j.error||r.status);
      toast('gtfs.zip загружен job '+j.id+' ('+file.name+')'); if($('gtfsFile')) $('gtfsFile').value=''; loadJobs();
    }catch(e){toast(e.message,true)}
    return;
  }
  try{const j=await api('/api/v1/import/gtfs',{method:'POST',body:'{}'});toast('gtfs job '+j.id+' (без файла — pending)');loadJobs()}catch(e){toast(e.message,true)}
}
async function loadReview(){
  try{
    const r=await api('/api/v1/review');const tb=$('reviewBody');tb.innerHTML='';
    (r||[]).forEach(x=>{
      const tr=document.createElement('tr');
      const t=x.terminal||{};
      const where=t.missing?'<span class="muted">удалён из БД</span>':((t.name||'—')+'<br><span class="muted">'+(t.settlement||'НП?')+' • '+(t.lat||'')+','+(t.lon||'')+(t.is_locked?' • locked':'')+'</span>');
      const dups=(x.duplicates||[]).map(d=>'<div>'+d.id+': '+(d.name||'—')+' ('+(d.similarity||'')+')</div>').join('')||'<span class="muted">—</span>';
      const key='\''+x.entity_type+'\','+x.entity_id+',\''+x.reason+'\'';
      tr.innerHTML='<td>'+x.entity_type+'</td><td>'+x.entity_id+'</td><td style="max-width:260px">'+where+'</td><td>'+x.reason+'<br><span class="muted">'+(x.score||'')+'</span></td><td>'+(x.score||'')+'</td><td style="max-width:220px">'+dups+'</td><td><div class="row" style="gap:4px"><button class="secondary" onclick="openReviewTerminal('+x.entity_id+')">→</button><button class="ok" onclick="resolveReview('+key+',\'approve\')">✓</button><button class="danger" onclick="resolveReview('+key+',\'dismiss\')">✕</button></div></td>';
      tb.appendChild(tr);
    });
    if((r||[]).length===0) tb.innerHTML='<tr><td colspan="7" class="muted">очередь пуста</td></tr>';
  }catch(e){$('reviewBody').innerHTML='<tr><td colspan="7" class="muted">'+e.message+'</td></tr>'}
}
async function resolveReview(etype,eid,reason,action){
  if(action==='dismiss' && !confirm('Снять с ревью '+etype+':'+eid+' ('+reason+')?')) return;
  try{await api('/api/v1/review/resolve',{method:'POST',body:JSON.stringify({entity_type:etype,entity_id:eid,reason,action})});toast(action==='approve'?'Подтверждено (locked)':'Снято с ревью');loadReview()}catch(e){toast(e.message,true)}
}
function openReviewTerminal(id){showTab('terminals');const s=$('termSearch');if(s){s.value='';}termPage=1;loadTerminals().then(()=>openTermCard(id));$('termId').value=id;toast('Терминал '+id+' — карточка открыта')}
async function exportReview(){window.open('/api/v1/review/export.csv','_blank')}
let termPage=1, termLimit=20, termSort='id', termOrder='asc';
function updateSortIndicators(){
  ['id','name','is_locked'].forEach(c=>{
    const el=$('sort-'+c);
    if(!el) return;
    if(c===termSort) el.textContent= termOrder==='asc'?'↑':'↓';
    else el.textContent='↕';
  });
}
function sortTerm(col){
  if(termSort===col){
    if(termOrder==='asc') termOrder='desc';
    else if(termOrder==='desc'){ termSort='id'; termOrder='asc'; }
  } else {
    termSort=col; termOrder='asc';
  }
  termPage=1;
  updateSortIndicators();
  loadTerminals();
}
function clearTermSearch(){
  const el=$('termSearch');
  if(el) el.value='';
  termPage=1;
  loadTerminals();
}
async function loadTerminals(){
  const q=($('termSearch')?$('termSearch').value.trim():'');
  const dead=($('termDead')?$('termDead').value:'');
  const off=(termPage-1)*termLimit;
  try{
    if(dead==='yes'||dead==='no'){
      const data=await api('/api/v1/admin/terminals/liveness?limit='+termLimit+'&offset='+off+'&dead='+dead);
      renderTerminals(data);
      return;
    }
    const qs='limit='+termLimit+'&offset='+off+'&sort='+encodeURIComponent(termSort)+'&order='+encodeURIComponent(termOrder)+'&q='+encodeURIComponent(q);
    const data=await api('/api/v1/admin/terminals?'+qs);
    renderTerminals(data);
  }catch(e){ $('terminalsBody').innerHTML='<tr><td colspan="7" class="muted">'+e.message+'</td></tr>'}
}
function renderTerminals(data){
    const tb=$('terminalsBody'); tb.innerHTML='';
    updateSortIndicators();
    (data.items||data||[]).forEach(t=>{
      const tr=document.createElement('tr');
      const locked=t.is_locked? '<span class="badge blocked">locked</span>':'<span class="muted">—</span>';
      const stl=(t.settlement||'')+(t.settlement_manual?' ✓':'');
      const dead=t.dead? '<span class="badge pending" title="нет ни одного stop_times">dead</span>':(t.trips_served!=null?'<span class="badge active">'+t.trips_served+'</span>':'<span class="muted">—</span>');
      const added=t.valid_from||( '<span class="muted">—</span>');
      const removed=t.valid_to?(' → '+t.valid_to):'';
      const esc=(t.name||'').replace(/\'/g,"\\'").replace(/"/g,'&quot;');
      tr.innerHTML='<td>'+t.id+'</td><td>'+(t.name||t.osm_compatible_name||'—')+'</td><td>'+(stl||'<span class="muted">—</span>')+'</td><td>'+(t.lat||'')+','+(t.lon||'')+'</td><td>'+locked+'</td><td>'+dead+'</td><td>'+added+removed+'</td><td><div class="row" style="gap:4px"><button class="secondary" onclick="openTermCard('+t.id+')" title="Карточка и табло">▣</button><button class="secondary" onclick="fillTerm('+t.id+',\''+esc+'\',\''+(t.settlement||'').replace(/\'/g,"\\'")+'\','+(t.lat||0)+','+(t.lon||0)+')">→</button><button class="secondary" title="Слить этот дубликат в другой терминал" onclick="quickMerge('+t.id+',\''+esc+'\')">⇄</button><button class="danger" title="Удалить (тумстоун)" onclick="deleteTerminal('+t.id+',\''+esc+'\')">✕</button></div></td>';
      tb.appendChild(tr);
    });
    if((data.items||data||[]).length===0) tb.innerHTML='<tr><td colspan="8" class="muted">терминалов нет</td></tr>';
    $('termPage').textContent=termPage;
}
function fillTerm(id,name,settlement,lat,lon){$('termId').value=id; $('termName').value=name; $('termSettlement').value=settlement||''; $('termLat').value=lat; $('termLon').value=lon;}
function prevTermPage(){ if(termPage>1){termPage--; loadTerminals();}}
function nextTermPage(){ termPage++; loadTerminals();}
async function updateTerminal(){const id=$('termId').value;if(!id) return toast('ID терминала?',true);const body={name:$('termName').value, settlement:$('termSettlement').value.trim(), lat: parseFloat($('termLat').value)||0, lon: parseFloat($('termLon').value)||0, approve:false};try{const j=await api('/api/v1/admin/terminals/'+id,{method:'PUT',body:JSON.stringify(body)});$('termMsg').textContent='Сохранено is_locked=true, provenance.actor_id проставлен';toast('Терминал '+j.id+' сохранён'); loadTerminals()}catch(e){$('termMsg').textContent='Ошибка: '+e.message;toast(e.message,true)}}
async function approveTerminal(){const id=$('termId').value;if(!id) return toast('ID терминала?',true);if(!confirm('Апрувнуть терминал '+id+'? Ставит last_verified_at, снимает записи ревью.')) return;const body={name:$('termName').value, settlement:$('termSettlement').value.trim(), lat: parseFloat($('termLat').value)||0, lon: parseFloat($('termLon').value)||0, approve:true};try{const j=await api('/api/v1/admin/terminals/'+id,{method:'PUT',body:JSON.stringify(body)});$('termMsg').textContent='Апрувнуто: is_locked=true, last_verified_at='+j.last_verified_at+', записи ревью сняты';toast('Терминал '+j.id+' апрувнут'); loadTerminals(); loadReview()}catch(e){$('termMsg').textContent='Ошибка: '+e.message;toast(e.message,true)}}
async function unapproveTerminal(){const id=$('termId').value;if(!id) return toast('ID терминала?',true);if(!confirm('Снять подтверждение (is_locked) с терминала '+id+'? Автосинк снова сможет обновлять его атрибуты; после этого можно удалять или сливать без force.')) return;try{const j=await api('/api/v1/admin/terminals/'+id,{method:'PUT',body:JSON.stringify({unapprove:true})});$('termMsg').textContent='Снят is_locked: терминал '+j.id+' вернулся в автосинк';toast('Лок терминала '+j.id+' снят'); loadTerminals(); loadReview()}catch(e){$('termMsg').textContent='Ошибка: '+e.message;toast(e.message,true)}}
async function mergeTerminals(){const oldId=parseInt($('mergeOldId').value), newId=parseInt($('mergeNewId').value);if(!oldId||!newId) return toast('Укажите old (дубликат) и new (канонический) ID',true);if(oldId===newId) return toast('ID одинаковы',true);const reason=$('mergeReason').value.trim();if(!confirm('Слить терминал '+oldId+' (дубликат, будет тумстоун) в '+newId+'? Идентификаторы, алиасы, стопы переедут на '+newId+'.')) return;try{const j=await api('/api/v1/admin/terminals/merge',{method:'POST',body:JSON.stringify({old_id:oldId,new_id:newId,reason:reason||undefined})});$('termMsg').textContent='Слито: '+j.old_id+' → '+j.new_id+' ('+(j.reason||'manual_merge')+')';toast('Дубликат '+oldId+' слит в '+newId); loadTerminals(); loadReview()}catch(e){$('termMsg').textContent='Ошибка: '+e.message;toast(e.message,true)}}
function quickMerge(oldId,name){const v=prompt('Слить дубликат '+oldId+' («'+(name||'')+'») в терминал ID:','');if(!v) return;const newId=parseInt(v);if(!newId||newId===oldId) return toast('Неверный ID',true);$('mergeOldId').value=oldId;$('mergeNewId').value=newId;mergeTerminals()}
async function deleteTerminal(id,name){if(!confirm('Удалить терминал '+id+' («'+(name||'')+'»)? Тумстоун valid_to=сегодня, из канона/GTFS исчезает; история сохраняется.')) return;try{const j=await api('/api/v1/admin/terminals/'+id,{method:'DELETE'});$('termMsg').textContent='Удалён (тумстоун): '+j.id;toast('Терминал '+id+' удалён'); loadTerminals(); loadReview()}catch(e){$('termMsg').textContent='Ошибка: '+e.message;toast(e.message,true)}}
async function openTermCard(id){
  const box=$('termCard'); box.classList.remove('hidden');
  box.innerHTML='<span class="muted">загрузка…</span>';
  try{
    const c=await api('/api/v1/admin/terminals/'+id+'/card');
    const st=c.stats||{};
    let h='<div class="row" style="justify-content:space-between"><h3 style="font-size:15px;margin:0">Терминал '+c.id+': '+(c.name||'—')+'</h3><button class="secondary" onclick="$(\'termCard\').classList.add(\'hidden\')">✕</button></div>';
    h+='<div class="row" style="gap:16px;margin-top:8px;flex-wrap:wrap"><span>lat/lon: <b>'+(c.lat||'')+','+(c.lon||'')+'</b></span><span>place: '+(c.place_id||'—')+'</span><span>locked: <b>'+(c.is_locked?'да':'нет')+'</b></span><span>НП: <b>'+(c.settlement||(c.tags&&c.tags.settlement)||'—')+'</b></span></div>';
    h+='<div class="row" style="gap:16px;margin-top:6px;flex-wrap:wrap"><span>stop_times: <b>'+((st.stop_times!=null)?st.stop_times:'—')+'</b></span><span>живых рейсов: <b>'+((st.live_trips!=null)?st.live_trips:'—')+'</b></span><span>provisional: <b>'+((st.provisional_stop_times!=null)?st.provisional_stop_times:'—')+'</b></span>'+(st.dead?'<span class="badge pending">мёртвый терминал</span>':'')+'</div>';
    if(c.aliases&&c.aliases.length){h+='<div style="margin-top:8px"><span class="muted">Алиасы:</span> '+c.aliases.map(a=>'<code>'+a.alias+'</code> ('+a.lang+(a.source?','+a.source:'')+')').join(' ')+'</div>'}
    if(c.identifiers&&c.identifiers.length){h+='<div style="margin-top:4px"><span class="muted">Идентификаторы:</span> '+c.identifiers.map(i=>'<code>'+i.system+':'+i.code_type+'='+i.code+'</code>').join(' ')+'</div>'}
    if(c.tags&&Object.keys(c.tags).length){h+='<div style="margin-top:4px"><span class="muted">Теги:</span> '+Object.entries(c.tags).map(([k,v])=>'<code>'+k+'='+v+'</code>').join(' ')+'</div>'}
    if(c.review&&c.review.length){h+='<div style="margin-top:4px"><span class="muted">Ревью:</span> '+c.review.map(r=>'<span class="badge pending">'+r.reason+'</span>').join(' ')+'</div>'}
    h+='<div class="row" style="margin-top:12px;gap:8px"><select id="termValProvider" style="width:140px"><option value="overpass">overpass (OSM)</option><option value="nominatim">nominatim</option><option value="yandex">yandex</option></select><button class="secondary" onclick="validateTermExternal('+id+')">Валидация через внешний API</button><button class="secondary" onclick="fillTerm('+id+',\''+((c.name)||'').replace(/'/g,"\\'")+'\',\''+((c.settlement)||(c.tags&&c.tags.settlement)||'').replace(/'/g,"\\'")+'\','+c.lat+','+c.lon+')">в поля правки →</button></div><div id="termValidateOut" style="margin-top:8px"></div>';
    h+='<div class="row" style="margin-top:12px;gap:8px"><input id="termSchedDate" type="date" style="width:160px"><button onclick="loadTermSchedule('+id+')">Табло на дату</button></div><div id="termSchedOut" style="margin-top:8px"></div>';
    const tt=(c.transport_types&&c.transport_types.length)?c.transport_types:['bus','train','flight'];
    h+='<div class="row" style="margin-top:14px;gap:8px;border-top:1px solid #ddd;padding-top:10px;flex-wrap:wrap"><b style="font-size:13px">Точечная загрузка расписания</b><span class="muted" style="font-size:11px;align-self:center">источник — Яндекс Rasp (кэш → API по квоте)</span><input id="termCollectDate" type="date" style="width:160px" value="'+new Date().toISOString().slice(0,10)+'"><select id="termCollectTransport" style="width:140px">'+tt.map(t=>'<option value="'+t+'">'+t+'</option>').join('')+'</select><label style="cursor:pointer"><input type="checkbox" id="termCollectOffline" checked> только кэш (offline)</label><button onclick="collectTermTrips('+id+')">Собрать расписание →</button></div><div id="termCollectOut" class="muted" style="margin-top:6px"></div>';
    box.innerHTML=h;
    const d=new Date(); box.querySelector('#termSchedDate').value=d.toISOString().slice(0,10);
  }catch(e){box.innerHTML='<span class="muted">Ошибка: '+e.message+'</span>'}
}
async function loadTermSchedule(id){
  const d=($('termSchedDate')?$('termSchedDate').value:'');
  const out=$('termSchedOut'); out.innerHTML='<span class="muted">загрузка…</span>';
  try{
    const j=await api('/api/v1/admin/terminals/'+id+'/schedule?date='+d);
    if(!(j.items||[]).length){out.innerHTML='<span class="muted">на '+d+' отправлений нет (или терминал мёртвый)</span>';return}
    let h='<div style="overflow:auto;max-height:320px"><table><thead><tr><th>отпр. (сек)</th><th>отпр.</th><th>куда</th><th>mode</th><th>код рейса</th><th>дни</th><th></th></tr></thead><tbody>';
    (j.items).forEach(it=>{
      const mm=Math.floor((it.departure%86400)/60), hh=Math.floor(mm/60), t=('0'+hh).slice(-2)+':'+('0'+(mm%60)).slice(-2);
      h+='<tr><td>'+it.departure+'</td><td><b>'+t+'</b></td><td style="max-width:280px">'+(it.destination||'—')+'</td><td>'+(it.mode||'')+'</td><td>'+it.external_trip_code+'</td><td class="muted">'+(it.service_days||'')+'</td><td><button class="secondary" onclick="toast(\'trip '+it.trip_id+'\')">→</button></td></tr>';
    });
    h+='</tbody></table></div>';
    out.innerHTML=h;
  }catch(e){out.innerHTML='<span class="muted">Ошибка: '+e.message+'</span>'}
}
let routesPage=1;
async function loadRoutes(){
  const q=($('routesSearch')?$('routesSearch').value.trim():'');
  const off=(routesPage-1)*20;
  try{
    const j=await api('/api/v1/admin/routes?limit=20&offset='+off+'&q='+encodeURIComponent(q));
    const tb=$('routesBody'); tb.innerHTML='';
    (j.items||[]).forEach(r=>{
      const tr=document.createElement('tr');
      tr.innerHTML='<td>'+r.id+'</td><td><code>'+r.external_route_code+'</code></td><td style="max-width:280px">'+(r.long_name||r.short_name||'—')+'</td><td>'+r.mode+'</td><td style="max-width:200px">'+(r.carrier||'—')+'</td><td>'+(r.live_trips!=null?('<b'+(r.live_trips===0?' style="color:#b45309"':'')+'>'+r.live_trips+'</b>'):'—')+'</td><td><button class="secondary" onclick="loadTrips('+r.id+',\''+(r.external_route_code||'').replace(/\'/g,"\\'")+'\')">рейсы →</button></td>';
      tb.appendChild(tr);
    });
    if((j.items||[]).length===0) tb.innerHTML='<tr><td colspan="7" class="muted">маршрутов нет</td></tr>';
    $('routesPage').textContent=routesPage;
    $('tripsTitle').style.display='none'; $('tripsTable').style.display='none'; $('stTitle').style.display='none'; $('stTable').style.display='none';
  }catch(e){$('routesBody').innerHTML='<tr><td colspan="7" class="muted">'+e.message+'</td></tr>'}
}
async function loadTrips(routeID,routeCode){
  try{
    const j=await api('/api/v1/admin/trips?route_id='+routeID+'&limit=50');
    const tb=$('tripsBody'); tb.innerHTML='';
    (j.items||[]).forEach(t=>{
      const tr=document.createElement('tr');
      const live=t.is_live?'<span class="badge active">live</span>':'<span class="badge blocked">tomb</span>';
      const prov=t.provisional_count>0?'<span class="badge pending" title="provisional stop_times">'+t.provisional_count+'</span>':'<span class="muted">0</span>';
      tr.innerHTML='<td>'+t.id+'</td><td><code>'+t.external_trip_code+'</code></td><td class="muted">'+(t.service_days||'ежедн.')+'</td><td>'+t.stop_times_count+'</td><td>'+prov+'</td><td>'+live+'</td><td><button class="secondary" onclick="loadTripStops('+t.id+')">стопы →</button></td>';
      tb.appendChild(tr);
    });
    if((j.items||[]).length===0) tb.innerHTML='<tr><td colspan="7" class="muted">рейсов нет</td></tr>';
    $('tripsTitle').style.display=''; $('tripsTitle').textContent='Рейсы маршрута '+routeCode+' ('+(j.total||0)+')';
    $('tripsTable').style.display='';
    $('stTitle').style.display='none'; $('stTable').style.display='none';
  }catch(e){toast(e.message,true)}
}
async function loadTripStops(tripID){
  try{
    const j=await api('/api/v1/admin/trips/'+tripID);
    const tb=$('stBody'); tb.innerHTML='';
    const fmt=s=>{if(s==null) return '—'; const mm=Math.floor((s%86400)/60), hh=Math.floor(mm/60); return ('0'+hh).slice(-2)+':'+('0'+(mm%60)).slice(-2)};
    (j.items||[]).forEach(st=>{
      const tr=document.createElement('tr');
      const prov=st.is_provisional?'<span class="badge pending" title="provisional">P</span>':'';
      tr.innerHTML='<td>'+st.seq+'</td><td>'+fmt(st.arrival)+'</td><td>'+fmt(st.departure)+'</td><td style="max-width:320px">'+(st.name||'—')+'</td><td>'+prov+'</td><td>'+(st.match_score!=null?(Math.round(st.match_score*100)/100):'—')+'</td><td class="muted">'+(st.match_method||'')+'</td>';
      tb.appendChild(tr);
    });
    if((j.items||[]).length===0) tb.innerHTML='<tr><td colspan="7" class="muted">стопов нет</td></tr>';
    $('stTitle').style.display=''; $('stTitle').textContent='Стопы рейса '+tripID;
    $('stTable').style.display='';
  }catch(e){toast(e.message,true)}
}
async function settlementFromApi(){const lat=parseFloat($('termLat').value), lon=parseFloat($('termLon').value);if(!lat||!lon) return toast('Нужны lat/lon',true);try{const j=await api('/api/v1/admin/external-call',{method:'POST',body:JSON.stringify({provider:'nominatim',lat,lon})});if(j.settlement){$('termSettlement').value=j.settlement;toast('НП из API: '+j.settlement)}else{$('termMsg').textContent='НП не определён: '+(j.address||j.error||'');toast('НП не определён',true)}}catch(e){$('termMsg').textContent='Ошибка: '+e.message;toast(e.message,true)}}
async function callExternal(){
  const body={provider:$('extProvider').value, query:$('extQuery').value, lat: $('extLat').value?parseFloat($('extLat').value):undefined, lon: $('extLon').value?parseFloat($('extLon').value):undefined};
  const box=$('extCands'); box.innerHTML='';
  try{
    const j=await api('/api/v1/admin/external-call',{method:'POST',body:JSON.stringify(body)});
    $('extOut').textContent=JSON.stringify(j,null,2);
    let h='';
    if(j.errors) h+='<div class="muted">Ошибки провайдеров: '+Object.entries(j.errors).map(([k,v])=>'<b>'+k+'</b>: '+v).join('; ')+'</div>';
    if(j.candidates && j.candidates.length){
      h+='<table style="margin-top:8px"><thead><tr><th>name</th><th>lat/lon</th><th>provider</th><th>sim</th><th></th></tr></thead><tbody>';
      j.candidates.forEach((c,i)=>{h+='<tr'+(i===0&&j.validated?' style="background:#f0fdf4"':'')+'><td>'+c.name+'</td><td>'+c.lat+','+c.lon+'</td><td>'+c.provider+'</td><td>'+(c.similarity||0).toFixed?c.similarity.toFixed(2):c.similarity+'</td><td><button class="secondary" onclick="useExtCand('+c.lat+','+c.lon+')">взять</button></td></tr>'});
      h+='</tbody></table>';
      if(j.validated) toast('Найдено: '+j.name); else toast('Лучший кандидат ниже порога '+((j.debug||{}).threshold||''),true);
    } else if(j.address){
      h+='<div>Адрес: '+j.address+'</div>'+(j.settlement?'<div>НП: <b>'+j.settlement+'</b></div>':'');
      toast('Reverse OK'+(j.settlement?': '+j.settlement:''));
    } else {
      toast(j.error||'Ничего не найдено',true);
    }
    box.innerHTML=h;
  }catch(e){$('extOut').textContent='Ошибка: '+e.message;toast(e.message,true)}
}
function useExtCand(lat,lon){$('extLat').value=lat;$('extLon').value=lon;toast('Координаты подставлены: '+lat+','+lon)}
async function loadQuotas(){try{const q=await api('/api/v1/quotas');const tb=$('quotasBody');tb.innerHTML='';(q||[]).forEach(r=>{const tr=document.createElement('tr');tr.innerHTML='<td>'+r.Provider+'</td><td>'+r.Day+'</td><td>'+r.Used+'/'+r.Limit+'</td><td>'+(r.ResetAt||'—')+'</td>';tb.appendChild(tr)});if((q||[]).length===0) tb.innerHTML='<tr><td colspan="4" class="muted">нет данных</td></tr>'}catch(e){$('quotasBody').innerHTML='<tr><td colspan="4" class="muted">'+e.message+'</td></tr>'}}
async function loadAudit(){try{const a=await api('/api/v1/admin/audit');const tb=$('auditBody');tb.innerHTML='';(a||[]).forEach(r=>{const tr=document.createElement('tr');tr.innerHTML='<td>'+r.ID+'</td><td>'+(r.UserID||'—')+'</td><td>'+r.Action+'</td><td>'+(r.EntityType||'—')+':'+(r.EntityID||'—')+'</td><td>'+fmtTime(r.At)+'</td>';tb.appendChild(tr)});if((a||[]).length===0) tb.innerHTML='<tr><td colspan="5" class="muted">пусто</td></tr>'}catch(e){$('auditBody').innerHTML='<tr><td colspan="5" class="muted">'+e.message+'</td></tr>'}}
(async()=>{await updateWho();loadUsers();loadDash();const rd=$('rtDate');if(rd&&!rd.value) rd.value=new Date().toISOString().slice(0,10);})();

async function loadCollectRegions(){try{const j=await api('/api/v1/collect/regions');const sel=$('colRegion');const cur=sel.value;sel.innerHTML='';if(!(j||[]).length){sel.innerHTML='<option value="">— дампа нет —</option>';$('colRegionsBody').innerHTML='<tr><td colspan="3" class="muted">Яндекс-дамп не найден (sync.yandex_dump_path)</td></tr>';return}j.forEach(r=>{const o=document.createElement('option');o.value=r.region;o.textContent=r.region+' ('+r.terminal_stations+')';sel.appendChild(o)});if(cur) sel.value=cur;const tb=$('colRegionsBody');tb.innerHTML='';j.forEach(r=>{const tr=document.createElement('tr');tr.innerHTML='<td>'+r.region+'</td><td>'+r.terminal_stations+'</td><td><button class="secondary" onclick="pickRegion(\''+r.region.replace(/'/g,"\\'")+'\')">выбрать</button></td>';tb.appendChild(tr)})}catch(e){$('colRegionsBody').innerHTML='<tr><td colspan="3" class="muted">'+e.message+'</td></tr>'}}
function pickRegion(r){$('colRegion').value=r;toast('Регион выбран: '+r)}
function colRegionChanged(){}
function colTransports(){const t=[];if($('colBus').checked)t.push('bus');if($('colTrain').checked)t.push('train');if($('colFlight').checked)t.push('flight');return t}
function colStationTypes(){const st=[];if($('colStBusStation')&&$('colStBusStation').checked)st.push('bus_station');if($('colStStation')&&$('colStStation').checked)st.push('station');if($('colStTrainStation')&&$('colStTrainStation').checked)st.push('train_station');if($('colStAirport')&&$('colStAirport').checked)st.push('airport');if($('colStBusStop')&&$('colStBusStop').checked)st.push('bus_stop');return st}
async function collectSkeleton(){const region=$('colRegion').value;if(!region) return toast('Выберите регион',true);const body={regions:[region],transports:colTransports(),station_types:colStationTypes(),offline:$('colOffline').checked};try{const j=await api('/api/v1/collect/skeleton',{method:'POST',body:JSON.stringify(body)});$('colMsg').textContent='Скелет: job #'+j.id+' поставлен в очередь (регион '+j.region+'). Следите за прогрессом во вкладках «Прогоны» и «Импорты → Jobs».';toast('Job #'+j.id+' создан')}catch(e){$('colMsg').textContent='Ошибка: '+e.message;toast(e.message,true)}}
async function collectTermTrips(id){const out=$('termCollectOut');if(!out) return;const date=$('termCollectDate').value||new Date().toISOString().slice(0,10);const transport=$('termCollectTransport')?$('termCollectTransport').value:'bus';const offline=$('termCollectOffline')?$('termCollectOffline').checked:true;out.textContent='job ставится в очередь…';try{const j=await api('/api/v1/collect/trips',{method:'POST',body:JSON.stringify({terminal_id:id,date:date,transport:transport,offline:offline,tag:'collect-trips-term-'+id})});out.textContent='Job #'+j.id+' создан: сбор расписания терминала '+id+' ('+transport+', '+date+', offline='+offline+'). Прогресс — «Импорты → Jobs».';toast('Job #'+j.id+' создан')}catch(e){out.textContent='Ошибка: '+e.message;toast(e.message,true)}}
async function collectTrips(){const region=$('colRegion').value;if(!region) return toast('Выберите регион',true);const body={region:region,date:$('colDate').value||'',offline:$('colOffline').checked,stations:($('colStations')?$('colStations').value:'yandex'),station_types:colStationTypes()};try{const j=await api('/api/v1/collect/trips',{method:'POST',body:JSON.stringify(body)});$('colMsg').textContent='Рейсы: job #'+j.id+' поставлен в очередь (регион '+j.region+'). Cache-first; при offline — только сохранённые расписания/нитки.';toast('Job #'+j.id+' создан')}catch(e){$('colMsg').textContent='Ошибка: '+e.message;toast(e.message,true)}}
async function canonReset(){if(!confirm('УДАЛИТЬ ВСЕ терминалы и рейсы? Дубликаты после пересбора не вернуть — история синков тоже стирается. Кэш Яндекса останется.')) return;if(prompt('Введите RESET для подтверждения:')!=='RESET') return toast('Подтверждение не введено — отмена',true);try{const j=await api('/api/v1/admin/canon/reset',{method:'POST',body:JSON.stringify({confirm:'RESET',reason:'ui reset'})});const d=j.deleted||{};const total=Object.values(d).reduce((a,b)=>a+b,0);$('colResetMsg').textContent='Канон очищен: удалено '+total+' строк (терминалов: '+(d.terminals||0)+', рейсов: '+(d.trips||0)+', маршрутов: '+(d.routes||0)+')';toast('Канон очищен ('+total+' строк)');loadCollectRegions()}catch(e){$('colResetMsg').textContent='Ошибка: '+e.message;toast(e.message,true)}}

async function loadRuns(){try{const j=await api('/api/v1/sync/runs?limit=50');const tb=$('runsBody');tb.innerHTML='';(j||[]).forEach(r=>{const tr=document.createElement('tr');const st='<span class="badge '+(r.State==='done'?'active':(r.State==='dead'?'blocked':'pending'))+'">'+r.State+'</span>';tr.innerHTML='<td>'+r.ID+'</td><td><code>'+r.Kind+'</code></td><td>'+(r.Tag||'—')+'</td><td>'+st+'</td><td>'+(r.CreatedAt||'—')+'</td><td>'+(r.FinishedAt||'—')+'</td><td><button class="secondary" onclick="showRunDetail('+r.ID+')">сводка →</button></td>';tb.appendChild(tr)});if((j||[]).length===0) tb.innerHTML='<tr><td colspan="7" class="muted">прогонов нет</td></tr>'}catch(e){$('runsBody').innerHTML='<tr><td colspan="7" class="muted">'+e.message+'</td></tr>'}}

// ── Поиск маршрута (MCP find_route через /mcp) ─────────────────────
function rtParsePoint(v){
  v=(v||'').trim(); if(!v) return null;
  const m=v.match(/^(-?\d+(\.\d+)?),\s*(-?\d+(\.\d+)?)$/);
  if(m) return {lat:parseFloat(m[1]),lon:parseFloat(m[3])};
  return {place:v};
}
function rtHM(d){const t=new Date(d);return ('0'+t.getHours()).slice(-2)+':'+('0'+t.getMinutes()).slice(-2)}
async function rtSearch(){
  const from=rtParsePoint($('rtFrom').value), to=rtParsePoint($('rtTo').value), out=$('rtOut');
  if(!from||!to) return toast('Укажите откуда и куда',true);
  const args={};
  Object.assign(args, from.place?{from_place:from.place}:{from_lat:from.lat,from_lon:from.lon});
  Object.assign(args, to.place?{to_place:to.place}:{to_lat:to.lat,to_lon:to.lon});
  const d=$('rtDate').value||new Date().toISOString().slice(0,10);
  // Локальное время пользователя (без Z): браузер резолвит в зону
  // пользователя и toISOString() даёт RFC3339 с корректным offset.
  const dep=new Date(d+'T'+($('rtTime').value||'10:00')+':00');
  args.departure=dep.toISOString();
  if($('rtAllowGap').checked) args.allow_gap=true;
  const mw=parseInt($('rtMaxWalk').value); if(mw>0) args.max_walk_minutes=mw;
  const mt=parseInt($('rtMaxTransfers').value); if(mt>=0) args.max_transfers=mt;
  if($('rtPref').value) args.preference=$('rtPref').value;
  if(($('rtModes').value||'').trim()) args.transit_modes=$('rtModes').value.trim();
  out.innerHTML='<span class="muted">поиск…</span>';
  try{
    const r=await fetch('/mcp',{method:'POST',headers:authHeaders({'Accept':'application/json'}),body:JSON.stringify({jsonrpc:'2.0',id:1,method:'tools/call',params:{name:'find_route',arguments:args}})});
    const j=await r.json();
    if(!j.result||!j.result.content){out.innerHTML='<b style="color:var(--bad)">MCP: '+(j.error?JSON.stringify(j.error):'пустой ответ')+'</b>';return}
    let text=j.result.content[0].text, journey=null;
    try{journey=JSON.parse(text)}catch(e){}
    if(!journey||!journey.legs){
      const isErr=j.result.isError;
      out.innerHTML='<div style="border-left:4px solid '+(isErr?'var(--bad)':'var(--bad)')+';padding:8px;background:#fef2f2;border-radius:4px"><b>'+(isErr?'Маршрут не найден':'Ошибка')+'</b><br><span style="font-size:13px">'+(isErr?text:JSON.stringify(j.result))+'</span></div>';
      return;
    }
    out.innerHTML=rtRenderJourney(journey,from,to);
  }catch(e){out.innerHTML='<b style="color:var(--bad)">Ошибка: '+e.message+'</b>'}
}
function rtModeBadge(m){
  const map={BUS:'🚌',COACH:'🚍',RAIL:'🚆',SUBWAY:'🚇',TRAM:'🚊',FLIGHT:'✈️',TAXI:'🚕',CAR:'🚗',WALK:'🚶',BICYCLE:'🚲',SCOOTER:'🛴',TRANSFER:'⇄'};
  return '<span class="badge pending" style="font-size:11px">'+(map[m]||'')+' '+m+'</span>';
}
function rtLegRow(l){
  const nm=x=>x.name||x.stop_id||'—';
  const coord=x=>(+x.lat).toFixed(3)+','+(+x.lon).toFixed(3);
  const hint=l.time_hint?' <span class="badge pending" style="font-size:11px" title="'+l.time_hint+'">⚠ время уточнить</span>':'';
  const tbadge=l.is_transit?' <span class="badge pending" style="font-size:11px;background:#fee;color:#933" title="транзитный рейс: садитесь/выходите на промежуточной остановке">транзит</span>':'';
  let stops='';
  if(l.stops&&l.stops.length){
    stops='<div class="muted" style="font-size:11px;margin-top:4px">';
    if(l.is_transit) stops+='<div>Маршрут рейса:</div>';
    stops+='<span style="white-space:nowrap">'+l.stops.map((s,i)=>nm(s)+(s.stop_id!==undefined?' ('+s.stop_id+')':'')+(i<l.stops.length-1?' → ':'')).join('')+'</span>';
    stops+='</div>';
  }
  return '<tr>'
    +'<td style="white-space:nowrap"><b>'+rtHM(l.departure)+'</b> → <b>'+rtHM(l.arrival)+'</b>'+hint+tbadge+stops+'</td>'
    +'<td style="white-space:nowrap">'+rtModeBadge(l.mode)+'</td>'
    +'<td>'+nm(l.from)+' <span class="muted" style="font-size:11px">'+coord(l.from)+'</span><br>↓ '+Math.round((new Date(l.arrival)-new Date(l.departure))/60000)+' мин'+(l.cost&&l.cost.amount?' • '+l.cost.amount+' '+l.cost.currency:'')+'</td>'
    +'<td>'+nm(l.to)+' <span class="muted" style="font-size:11px">'+coord(l.to)+'</span></td>'
    +'<td class="muted" style="font-size:12px">'+(l.route_id?'<code>'+l.route_id+'</code>':'')+(l.trip_id?'<br>trip '+l.trip_id:'')+'</td>'
    +'</tr>';
}
function rtRenderJourney(j,from,to){
  let h='<div style="border-left:4px solid var(--ok);padding:8px;background:#f0fdf4;border-radius:4px;margin-bottom:8px">'
    +'<b>✓ Маршрут найден</b>: '+rtHM(j.departure)+' → '+rtHM(j.arrival)
    +' • в пути '+Math.round((new Date(j.arrival)-new Date(j.departure))/60000)+' мин'
    +' • пересадок: '+j.transfers
    +' • легов: '+j.legs.length+'</div>';
  h+='<div style="overflow:auto"><table style="font-size:13px"><thead><tr><th>время</th><th>режим</th><th>откуда</th><th>куда</th><th>рейс</th></tr></thead><tbody>';
  j.legs.forEach(l=>h+=rtLegRow(l));
  h+='</tbody></table></div>';
  if(j.alternatives&&j.alternatives.length){
    h+='<div class="muted" style="margin-top:6px;font-size:12px">Альтернативы: '+j.alternatives.length+' (не показаны)</div>';
  }
  return h;
}
async function showRunDetail(id){const box=$('runDetail');box.classList.remove('hidden');box.textContent='загрузка…';try{const j=await api('/api/v1/sync/runs/'+id);let sum={};try{sum=JSON.parse(j.Summary||'{}')}catch(e){sum={}};const pretty={run:j.ID,kind:j.Kind,tag:j.Tag,state:j.State,created:j.CreatedAt,finished:j.FinishedAt,summary:sum};box.textContent=JSON.stringify(pretty,null,2)}catch(e){box.textContent='Ошибка: '+e.message}}

async function validateTermExternal(id){const box=$('termValidateOut');if(!box) return;box.innerHTML='<span class="muted">проверка через Overpass/Nominatim…</span>';try{const card=await api('/api/v1/admin/terminals/'+id+'/card');const prov=$('termValProvider')&&$('termValProvider').value||'overpass';const j=await api('/api/v1/admin/external-call',{method:'POST',body:JSON.stringify({provider:prov,query:card.name||'',lat:card.lat,lon:card.lon})});let h='';if(j.candidates&&j.candidates.length){h='<table style="margin-top:6px"><thead><tr><th>имя ('+prov+')</th><th>lat/lon</th><th>sim</th><th></th></tr></thead><tbody>';j.candidates.forEach(c=>{h+='<tr><td>'+c.name+'</td><td>'+c.lat+','+c.lon+'</td><td>'+(c.similarity!=null?(+c.similarity).toFixed(2):'—')+'</td><td><button class="secondary" onclick="fillTerm('+id+',\''+(c.name||'').replace(/'/g,"\\'")+'\',\'\', '+c.lat+','+c.lon+')">в поля правки →</button></td></tr>'});h+='</tbody></table>';if(j.settlement) h+='<div class="muted" style="margin-top:4px">НП из геокодера: <b>'+j.settlement+'</b></div>';if(j.validated) h+='<div class="muted" style="margin-top:2px">Сверка имени: <b style="color:var(--ok)">совпадение ≥ порога</b></div>';else h+='<div class="muted" style="margin-top:2px">Сверка имени: ниже порога — проверьте кандидатов вручную</div>'}else{h='<span class="muted">кандидатов нет (квота исчерпана или ничего не найдено)</span>'}box.innerHTML=h}catch(e){box.innerHTML='<span class="muted">Ошибка: '+e.message+'</span>';toast(e.message,true)}}
