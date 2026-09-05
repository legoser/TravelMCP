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
  document.querySelectorAll('#tab-users button, #tab-keys button, #tab-config button, #tab-dash button, #tab-imports button, #tab-terminals button, #tab-review button, #tab-external button, #tab-quotas button').forEach(b=>{b.disabled=dis});
}
function showTab(name){const all=['users','keys','config','dash','imports','terminals','review','external','quotas'];document.querySelectorAll('.tab').forEach(el=>el.classList.toggle('active', el.getAttribute('onclick').includes("'"+name+"'")));all.forEach(n=>{const e=$('tab-'+n);if(e) e.classList.toggle('hidden', n!==name)});if(name==='dash')loadDash();if(name==='config')loadConfig();if(name==='imports')loadImports();if(name==='review')loadReview();if(name==='quotas'){loadQuotas();loadAudit()} if(name==='terminals')loadTerminals();}
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
async function loadJobs(){try{const j=await api('/api/v1/jobs');const tb=$('jobsBody');tb.innerHTML='';(j||[]).forEach(r=>{const tr=document.createElement('tr');tr.innerHTML='<td>'+r.ID+'</td><td>'+r.Type+'</td><td>'+(r.Region||'—')+'</td><td><span class="badge '+r.State+'">'+r.State+'</span></td><td>'+r.Attempts+'</td><td>'+(r.NextRun||'—')+'</td><td style="max-width:200px;overflow:hidden;text-overflow:ellipsis">'+(r.LastError||'').slice(0,60)+'</td>';tb.appendChild(tr)});if((j||[]).length===0) tb.innerHTML='<tr><td colspan="7" class="muted">jobs нет</td></tr>'}catch(e){$('jobsBody').innerHTML='<tr><td colspan="7" class="muted">'+e.message+'</td></tr>'}}
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
async function loadReview(){try{const r=await api('/api/v1/review');const tb=$('reviewBody');tb.innerHTML='';(r||[]).forEach(x=>{const tr=document.createElement('tr');tr.innerHTML='<td>'+x.entity_type+'</td><td>'+x.entity_id+'</td><td>'+x.reason+'</td><td>'+(x.score||'')+'</td><td>'+(x.created_at||'')+'</td>';tb.appendChild(tr)});if((r||[]).length===0) tb.innerHTML='<tr><td colspan="5" class="muted">очередь пуста</td></tr>'}catch(e){$('reviewBody').innerHTML='<tr><td colspan="5" class="muted">'+e.message+'</td></tr>'}}
async function exportReview(){window.open('/api/v1/review/export.csv','_blank')}
let termPage=1, termLimit=20;
async function loadTerminals(){
  const sort=$('termSort')?$('termSort').value:'id';
  const offset=(termPage-1)*termLimit;
  try{
    const data=await api('/api/v1/admin/terminals?limit='+termLimit+'&offset='+offset+'&sort='+sort);
    const tb=$('terminalsBody'); tb.innerHTML='';
    (data.items||data||[]).forEach(t=>{
      const tr=document.createElement('tr');
      const locked=t.is_locked? '<span class="badge blocked">locked</span>':'<span class="muted">—</span>';
      tr.innerHTML='<td>'+t.id+'</td><td>'+(t.name||t.osm_compatible_name||'—')+'</td><td>'+(t.lat||'')+','+(t.lon||'')+'</td><td>'+locked+'</td><td>'+(t.place_id||'—')+'</td><td><button class="secondary" onclick="fillTerm('+t.id+',\''+(t.name||'').replace(/\'/g,"\\'")+'\','+(t.lat||0)+','+(t.lon||0)+')">→</button></td>';
      tb.appendChild(tr);
    });
    if((data.items||data||[]).length===0) tb.innerHTML='<tr><td colspan="6" class="muted">терминалов нет</td></tr>';
    $('termPage').textContent=termPage;
  }catch(e){ $('terminalsBody').innerHTML='<tr><td colspan="6" class="muted">'+e.message+'</td></tr>'}
}
function fillTerm(id,name,lat,lon){$('termId').value=id; $('termName').value=name; $('termLat').value=lat; $('termLon').value=lon;}
function prevTermPage(){ if(termPage>1){termPage--; loadTerminals();}}
function nextTermPage(){ termPage++; loadTerminals();}
async function updateTerminal(){const id=$('termId').value;if(!id) return toast('ID терминала?',true);const body={name:$('termName').value, lat: parseFloat($('termLat').value)||0, lon: parseFloat($('termLon').value)||0};try{const j=await api('/api/v1/admin/terminals/'+id,{method:'PUT',body:JSON.stringify(body)});$('termMsg').textContent='Сохранено is_locked=true, provenance.actor_id проставлен';toast('Терминал '+j.id+' сохранён'); loadTerminals()}catch(e){$('termMsg').textContent='Ошибка: '+e.message;toast(e.message,true)}}
async function callExternal(){const body={provider:$('extProvider').value, query:$('extQuery').value, lat: $('extLat').value?parseFloat($('extLat').value):undefined, lon: $('extLon').value?parseFloat($('extLon').value):undefined};try{const j=await api('/api/v1/admin/external-call',{method:'POST',body:JSON.stringify(body)});$('extOut').textContent=JSON.stringify(j,null,2);toast('Внешний вызов OK, provenance записан')}catch(e){$('extOut').textContent='Ошибка: '+e.message;toast(e.message,true)}}
async function loadQuotas(){try{const q=await api('/api/v1/quotas');const tb=$('quotasBody');tb.innerHTML='';(q||[]).forEach(r=>{const tr=document.createElement('tr');tr.innerHTML='<td>'+r.Provider+'</td><td>'+r.Day+'</td><td>'+r.Used+'/'+r.Limit+'</td><td>'+(r.ResetAt||'—')+'</td>';tb.appendChild(tr)});if((q||[]).length===0) tb.innerHTML='<tr><td colspan="4" class="muted">нет данных</td></tr>'}catch(e){$('quotasBody').innerHTML='<tr><td colspan="4" class="muted">'+e.message+'</td></tr>'}}
async function loadAudit(){try{const a=await api('/api/v1/admin/audit');const tb=$('auditBody');tb.innerHTML='';(a||[]).forEach(r=>{const tr=document.createElement('tr');tr.innerHTML='<td>'+r.ID+'</td><td>'+(r.UserID||'—')+'</td><td>'+r.Action+'</td><td>'+(r.EntityType||'—')+':'+(r.EntityID||'—')+'</td><td>'+fmtTime(r.At)+'</td>';tb.appendChild(tr)});if((a||[]).length===0) tb.innerHTML='<tr><td colspan="5" class="muted">пусто</td></tr>'}catch(e){$('auditBody').innerHTML='<tr><td colspan="5" class="muted">'+e.message+'</td></tr>'}}
(async()=>{await updateWho();loadUsers();loadDash();})();
