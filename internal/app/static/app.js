'use strict';
const $=s=>document.querySelector(s);
const esc=s=>String(s??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const label=s=>({codex:'Codex',claude:'Claude Code',copilot:'Copilot',other:'Other'}[s]||s);
const link=id=>'/sessions/'+encodeURIComponent(id);
const date=s=>new Date(s).toLocaleString(undefined,{month:'short',day:'numeric',hour:'2-digit',minute:'2-digit',hour12:false});
const clock=s=>new Date(s).toLocaleTimeString(undefined,{hour12:false});
let generation=0,busy=false,sessionOffset=0,eventOffset=0,source='',query='',kind='',origin='',events=[],meta=null;
let inspectedID='', inspectedSnapshot='', inspectorTab='details';
let selected=location.pathname.startsWith('/sessions/')?decodeURIComponent(location.pathname.slice(10)):'';
const archiveMode=location.pathname==='/archive';
const listURL=new URL(location.href);
source=listURL.searchParams.get('source')||'';query=listURL.searchParams.get('q')||'';sessionOffset=Number(listURL.searchParams.get('offset'))||0;
$('#search').value=query;
if(!selected&&!archiveMode) sessionStorage.setItem('sessionsURL',location.pathname+location.search);
$('.back').href=sessionStorage.getItem('sessionsURL')||'/';
async function api(path){const r=await fetch(path);if(r.status===401){closeInspector(false);$('#login').hidden=false;$('#workspace').hidden=true;throw Error('Sign in required.')}if(!r.ok)throw Error(r.status===404?'Session not found.':`Unable to load (HTTP ${r.status}). Retrying automatically.`);return r.json()}
function titleFor(id,p){if(p){const prompt=p.blocks.find(b=>/request|user/i.test(b.label));if(prompt&&prompt.text!=='Content not exported')return prompt.text.split('\n')[0].slice(0,150)}return id.includes('/agent:')?'Subagent · '+id.split('/agent:').pop():id.replace(/^(codex|claude|copilot|other):/,'')}
function listState(){const p=new URLSearchParams();if(source)p.set('source',source);if(query)p.set('q',query);if(sessionOffset)p.set('offset',sessionOffset);history.replaceState(null,'','/'+(p.size?'?'+p:''));sessionStorage.setItem('sessionsURL',location.pathname+location.search)}
function projectLabel(p){return [p?.project,...(Array.isArray(p?.tags)?p.tags:p?.tags?[p.tags]:[])].filter(Boolean).join(' · ')}
function renderSessions(rows){
 $('#listcount').textContent=rows.length?`${sessionOffset+1}–${sessionOffset+Math.min(rows.length,200)}`:'0';
 $('#sessions').innerHTML=rows.length?rows.slice(0,200).map(s=>`<a class="session" href="${esc(link(s.id))}" data-session="${esc(s.id)}"><div><h2>${esc(titleFor(s.id,s.preview))}</h2><small>${projectLabel(s.preview)?esc(projectLabel(s.preview))+' · ':''}${esc(s.id)}${s.parent?' · Subagent':''}</small></div><span class="source">${esc(label(s.source))}</span><time>${esc(date(s.end))}</time><span class="count">${s.count}</span></a>`).join(''):'<p class="empty">No sessions</p>';
 $('#prevsessions').disabled=sessionOffset===0;$('#nextsessions').disabled=rows.length<=200;
}
function renderEvents(){
 const focusedID=document.activeElement?.dataset.event;
 const slice=events.slice(0,200),parsed=slice.map(e=>[e,e.detail]);
 const inspected=events.find(e=>e.id===inspectedID||e.records.some(r=>r.id===inspectedID));
 if(inspected) inspectedID=inspected.id;
 const visible=parsed.filter(([,p])=>(!kind||(kind==='conversation'?(['user','assistant'].includes(p.kind)||(['model','agent'].includes(p.kind)&&p.blocks.some(b=>['user','assistant','Input','Output'].includes(b.label)))):kind==='error'?p.error:p.kind===kind)));
 $('#sessiontitle').textContent=titleFor(selected,meta.preview);
 $('#export').href='/api/export?session='+encodeURIComponent(selected)+'&origin='+encodeURIComponent(origin);
 const context={};for(const key of ['model','cwd','host','project','tags'])context[key]=parsed.map(([,p])=>p).find(p=>p[key])?.[key] ?? meta.preview?.[key];
 $('#sessionmeta').innerHTML=`<strong>${esc(label(meta.source))}</strong>${meta.start?`<span>${esc(date(meta.start))}</span>`:''}${context.model?`<span>${esc(context.model)}</span>`:''}${context.cwd?`<span>${esc(context.cwd)}</span>`:''}${context.host?`<span>${esc(context.host)}</span>`:''}${context.project?`<span>Project: ${esc(context.project)}</span>`:''}${context.tags?`<span>Tags: ${esc(Array.isArray(context.tags)?context.tags.join(', '):context.tags)}</span>`:''}<span class="sessionid">${esc(selected)}</span>`;
 $('#relations').innerHTML=(meta.parent?`<a href="${esc(link(meta.parent))}">Parent session</a>`:'')+meta.children.map(id=>`<a href="${esc(link(id))}">Subagent: ${esc(id.split('/agent:').pop())}</a>`).join('');
 $('#pageinfo').textContent=meta.count?`${eventOffset+1}–${eventOffset+slice.length} / ${meta.count}${kind||origin?' · '+visible.length+' matching on this page':''}`:'0 events';
 $('#timeline').innerHTML=visible.length?visible.map(([e,p])=>`<article class="event ${esc(p.kind)} ${p.error?'error':''}" id="span-${esc(e.span||e.id)}" data-event="${esc(e.id)}" role="button" tabindex="0" aria-label="${esc(p.title+' · '+clock(e.time))}" aria-expanded="${e.id===inspectedID}" aria-controls="inspector"><div class="eventcontent"><div class="eventheading"><h2>${esc(p.title)}</h2>${e.origins.length>1?'<span class="kind">Imported + OTEL</span>':''}${['tool','metric'].includes(p.kind)?`<span class="kind">${esc(p.kind)}</span>`:''}${p.error?'<span class="status failed">Failed</span>':String(p.attributes.success)==='true'?'<span class="status succeeded">Succeeded</span>':''}<time datetime="${esc(e.time)}" title="${esc(date(e.time))}">${esc(clock(e.time))}</time></div>${p.blocks.map(b=>`<div class="block ${b.label==='Error'?'failure-detail':''}">${['Request','Response','Message',p.kind].includes(b.label)?'':`<div class="blocklabel">${esc(b.label)}</div>`}<${b.code?'pre':'div'} class="${b.code?'code':'prose'}">${esc(b.text)}</${b.code?'pre':'div'}></div>`).join('')}${p.fields.length?`<dl class="fields">${p.fields.filter(([k])=>k!=='Result').map(([k,v])=>`<div><dt>${esc(k)}</dt><dd>${esc(v)}</dd></div>`).join('')}</dl>`:''}</div></article>`).join(''):'<p class="empty">No matching events on this page</p>';
 if(inspectedID && !visible.some(([e])=>e.id===inspectedID)) closeInspector(false);
 document.querySelectorAll('[data-event]').forEach(card=>{
  card.classList.toggle('selected',card.dataset.event===inspectedID);
  card.onclick=()=>{if(!window.getSelection()?.toString()) openInspector(card.dataset.event)};
  card.onkeydown=e=>{if(e.key==='Enter'||e.key===' '){e.preventDefault();openInspector(card.dataset.event)}};
 });
 if(focusedID) document.querySelector(`[data-event="${CSS.escape(focusedID)}"]`)?.focus({preventScroll:true});
 if(inspectedID) renderInspector();
 $('#prevevents').disabled=eventOffset===0;$('#nextevents').disabled=events.length<=200;
}

function setInspectorTab(tab){
 inspectorTab=tab;
 for(const name of ['details','json']){
  const button=$(name==='details'?'#detailtab':'#jsontab');
  button.setAttribute('aria-selected',String(name===tab));button.tabIndex=name===tab?0:-1;
  $('#inspect'+name).hidden=name!==tab;
 }
}
function syncInspectorMode(){
 const modal=!!inspectedID&&matchMedia('(max-width: 900px)').matches;
 $('#inspector').setAttribute('role',modal?'dialog':'region');
 if(modal) $('#inspector').setAttribute('aria-modal','true');else $('#inspector').removeAttribute('aria-modal');
 document.body.classList.toggle('inspector-modal',modal);
 for(const el of [...$('#detailpage').children].filter(x=>!x.classList.contains('eventlayout'))) el.inert=modal;
 $('#eventlist').inert=modal;$('.topbar').inert=modal;
 if(modal&&!$('#inspector').contains(document.activeElement)) $('#closeinspector').focus({preventScroll:true});
}
function openInspector(id){
 inspectedID=id;$('#inspector').hidden=false;$('#detailpage').classList.add('inspector-open');
 document.querySelectorAll('[data-event]').forEach(card=>{const active=card.dataset.event===id;card.classList.toggle('selected',active);card.setAttribute('aria-expanded',String(active))});
 renderInspector();syncInspectorMode();$('#closeinspector').focus({preventScroll:true});
}
function closeInspector(restore=true){
 const id=inspectedID;inspectedID='';inspectedSnapshot='';$('#inspector').hidden=true;$('#detailpage').classList.remove('inspector-open');syncInspectorMode();
 document.querySelectorAll('[data-event]').forEach(card=>{card.classList.remove('selected');card.setAttribute('aria-expanded','false')});
 if(restore&&id) document.querySelector(`[data-event="${CSS.escape(id)}"]`)?.focus({preventScroll:true});
}
function renderInspector(){
 const e=events.find(e=>e.id===inspectedID);if(!e)return;
 const snapshot=JSON.stringify(e);if(snapshot===inspectedSnapshot)return;inspectedSnapshot=snapshot;
 const parent=events.find(x=>x.span===e.parent_span&&x.trace===e.trace&&x.source===e.source&&document.getElementById('span-'+x.span));
 const p=e.detail,value=v=>typeof v==='string'?v:JSON.stringify(v,null,2);
 $('#inspectortitle').textContent=p.title;$('#inspectortime').textContent=date(e.time)+' · '+label(e.source);
 const properties=[['Sources',e.origins.map(x=>x==='import'?'Imported':'OTEL').join(' + ')],['Signal',e.signal],['Time',e.time],['Event',p.name],['Trace ID',e.trace],['Span ID',e.span],['Parent span',e.parent_span],['Session',e.session],...p.fields].filter(([,v])=>v!==undefined&&v!=='');
 $('#inspectdetails').innerHTML=p.blocks.map(b=>`<div class="block ${b.label==='Error'?'failure-detail':''}"><div class="blocklabel">${esc(b.label)}</div><${b.code?'pre':'div'} class="${b.code?'code':'prose'}">${esc(b.text)}</${b.code?'pre':'div'}></div>`).join('')+
 `<h3>Properties</h3><dl class="propertylist">${properties.map(([k,v])=>`<div><dt>${esc(k)}</dt><dd>${k==='Parent span'&&parent?`<a href="#span-${esc(parent.span)}" id="inspectparent">${esc(value(v))}</a>`:esc(value(v))}</dd></div>`).join('')}</dl>`+
 (p.conflicts?.length?`<h3>Source differences</h3><dl class="propertylist">${p.conflicts.map(c=>`<div><dt>${esc(c.field)}</dt><dd>${c.values.map(v=>`${v.origin==='import'?'Imported':'OTEL'}: ${esc(v.value)}`).join('\n')}</dd></div>`).join('')}</dl>`:'')+
 `<h3>Attributes</h3><dl class="propertylist">${Object.entries(p.attributes).map(([k,v])=>`<div><dt>${esc(k)}</dt><dd>${esc(value(v))}</dd></div>`).join('')||'<div>No attributes</div>'}</dl>`;
 if($('#inspectparent')) $('#inspectparent').onclick=ev=>{ev.preventDefault();openInspector(parent.id);document.getElementById('span-'+parent.span)?.scrollIntoView({block:'nearest'})};
 $('#eventjson').textContent=JSON.stringify(e.records.length===1?e.records[0].raw:{records:e.records},null,2);$('#copystatus').textContent='';setInspectorTab(inspectorTab);
}
$('#closeinspector').onclick=()=>closeInspector();
$('#detailtab').onclick=()=>setInspectorTab('details');$('#jsontab').onclick=()=>setInspectorTab('json');
$('.inspectortabs').onkeydown=e=>{if(['ArrowLeft','ArrowRight','Home','End'].includes(e.key)){e.preventDefault();setInspectorTab(e.key==='Home'?'details':e.key==='End'?'json':inspectorTab==='json'?'details':'json');$(inspectorTab==='json'?'#jsontab':'#detailtab').focus()}};
$('#copyjson').onclick=async()=>{try{await navigator.clipboard.writeText($('#eventjson').textContent);$('#copystatus').textContent='Copied'}catch{$('#copystatus').textContent='Select the JSON to copy'}};
addEventListener('resize',syncInspectorMode);
addEventListener('keydown',e=>{
 if(!inspectedID)return;
 if(e.key==='Escape'){e.preventDefault();closeInspector();return}
 if(e.key==='Tab'&&matchMedia('(max-width: 900px)').matches){
  const items=[...$('#inspector').querySelectorAll('button,[tabindex="0"]')].filter(el=>el.getClientRects().length&&!el.disabled&&el.tabIndex>=0);
  const first=items[0],last=items.at(-1);
  if(e.shiftKey&&document.activeElement===first){e.preventDefault();last.focus()}else if(!e.shiftKey&&document.activeElement===last){e.preventDefault();first.focus()}
 }
});
async function refresh(){if(busy)return;busy=true;const g=generation;try{
 if(archiveMode){await refreshArchive()}
 else if(selected){const page=await api(`/api/session?id=${encodeURIComponent(selected)}&origin=${encodeURIComponent(origin)}&offset=${eventOffset}`);if(g!==generation)return;meta=page;events=page.events;renderEvents()}
 else {const rows=await api(`/api/sessions?source=${encodeURIComponent(source)}&q=${encodeURIComponent(query)}&offset=${sessionOffset}`);if(g!==generation)return;renderSessions(rows)}
 $('#login').hidden=true;$('#workspace').hidden=false;$('#listpage').hidden=!!selected||archiveMode;$('#detailpage').hidden=!selected||archiveMode;$('#archivepage').hidden=!archiveMode;$('#error').textContent='';
 }catch(e){$('#error').textContent=e.message;if($('#login').hidden){$('#workspace').hidden=false;$('#listpage').hidden=!!selected||archiveMode;$('#detailpage').hidden=!selected||archiveMode;$('#archivepage').hidden=!archiveMode;$('#timeline').innerHTML=''}}finally{busy=false;if(g!==generation)refresh()}}
$('#loginform').onsubmit=async e=>{e.preventDefault();try{const r=await fetch('/api/login',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({token:$('#token').value})});if(!r.ok)throw Error('Invalid access token.');$('#token').value='';refresh()}catch(e){$('#loginerror').textContent=e.message}};
$('#logout').onclick=async()=>{await fetch('/api/logout',{method:'POST'});location.reload()};
document.querySelectorAll('[data-source]').forEach(b=>{b.classList.toggle('active',b.dataset.source===source);b.onclick=()=>{source=b.dataset.source;sessionOffset=0;generation++;listState();document.querySelectorAll('[data-source]').forEach(x=>x.classList.toggle('active',x===b));refresh()}});
document.querySelectorAll('[data-kind]').forEach(b=>b.onclick=()=>{kind=b.dataset.kind;document.querySelectorAll('[data-kind]').forEach(x=>x.classList.toggle('active',x===b));renderEvents()});
let timer;$('#search').oninput=()=>{clearTimeout(timer);timer=setTimeout(()=>{query=$('#search').value;sessionOffset=0;generation++;listState();refresh()},250)};
for(const [id,delta,type]of[['prevsessions',-200,'s'],['nextsessions',200,'s'],['prevevents',-200,'e'],['nextevents',200,'e']])$('#'+id).onclick=()=>{if(type==='s'){sessionOffset=Math.max(0,sessionOffset+delta);listState()}else eventOffset=Math.max(0,eventOffset+delta);generation++;refresh();window.scrollTo(0,0)};
$('#origin').onchange=()=>{origin=$('#origin').value;eventOffset=0;generation++;closeInspector(false);refresh()};
$('#autorefresh').onchange=()=>$('#connection').textContent=$('#autorefresh').checked?'Auto-refresh · 5s':'Paused';
const archiveSelection=new Set();let archiveAll=false,archiveOffset=0,archiveData=null;
const bytes=n=>{const units=['B','KiB','MiB','GiB','TiB'];let i=0;while(n>=1024&&i<units.length-1){n/=1024;i++}return `${n.toFixed(i?1:0)} ${units[i]}`};
async function archivePost(path,body){const r=await fetch(path,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)});if(r.status===401){$('#login').hidden=false;$('#workspace').hidden=true;throw Error('Sign in required.')}if(!r.ok)throw Error((await r.text()).trim());return r.json()}
function updateArchiveSelection(){const n=archiveAll?archiveData.total_sessions:archiveSelection.size;$('#archiveselected').textContent=`${n} selected`;$('#archivecreate').disabled=!n;document.querySelectorAll('[data-archive-session]').forEach(el=>{el.checked=archiveAll||archiveSelection.has(el.dataset.archiveSession);el.disabled=archiveAll})}
async function refreshArchive(){
 const data=await api('/api/archive?offset='+archiveOffset);archiveData=data;
 const job=data.job,active=job&&['preparing','ready','deleting','compacting'].includes(job.status);
 $('#connection').textContent='Auto-refresh · 5s';$('#archivetotal').textContent=`${data.total_sessions} sessions · ${bytes(data.total_bytes)} uncompressed`;
 $('#archivecutoff').textContent=`Last activity before ${new Date(data.cutoff).toLocaleDateString()} · DB data only; source files stay in place.`;
 $('#archiveselection').hidden=!!active;
 $('#archiveitems').innerHTML=data.sessions.length?data.sessions.slice(0,200).map(s=>`<label class="archive-row"><input type="checkbox" data-archive-session="${esc(s.id)}" ${active?'disabled checked':''}><span><a href="${esc(link(s.id))}">${esc(s.id)}</a><small>${esc(label(s.source))} · ${esc(new Date(s.last_activity).toLocaleString())}</small></span><span>${s.records.toLocaleString()} ${s.records===1?'record':'records'}</span><span>${bytes(s.raw_bytes)}</span></label>`).join(''):'<p class="empty">No inactive sessions</p>';
 document.querySelectorAll('[data-archive-session]').forEach(el=>el.onchange=()=>{if(el.checked)archiveSelection.add(el.dataset.archiveSession);else archiveSelection.delete(el.dataset.archiveSession);updateArchiveSelection()});if(!active)updateArchiveSelection();
 $('#archiveprev').disabled=archiveOffset===0;$('#archivenext').disabled=data.sessions.length<=200;
 const key=job?`${job.id}:${job.status}:${job.compaction_error}`:'';
 if($('#archivejob').dataset.key===key){const progress=$('#archiveprogress');if(progress)progress.textContent=archiveProgress(job);return}
 $('#archivejob').dataset.key=key;
 if(!job){$('#archivejob').innerHTML='';return}
 const cancel=['preparing','ready'].includes(job.status)?'<button id="archivecancel">Cancel archive</button>':'';
 let content='';
 if(job.status==='preparing')content=`<h2>Creating ZIP</h2><p id="archiveprogress">${esc(archiveProgress(job))}</p>${cancel}`;
 if(job.status==='ready')content=`<h2>Ready to download</h2><p>${job.session_count} sessions · ${job.record_count.toLocaleString()} records · ${bytes(job.archive_bytes)}</p><a id="archivedownload" class="button primary" href="/api/archives/${encodeURIComponent(job.id)}/download" download>Download ZIP</a> ${cancel}<details><summary>SHA-256</summary><code class="archivehash">${esc(job.sha256)}</code></details><form id="archivedeleteform"><label>After checking your downloaded ZIP, type <strong>DOWNLOADED</strong><input id="archiveconfirmation" autocomplete="off" spellcheck="false" required placeholder="DOWNLOADED"></label><button id="archivedelete" class="danger" disabled>Delete archived DB records</button></form><p id="archiveactionerror" role="alert"></p>`;
 if(job.status==='deleting')content=`<h2>Deleting archived records</h2><p id="archiveprogress">${esc(archiveProgress(job))}</p>`;
 if(job.status==='compacting')content='<h2>Reclaiming database space</h2><p>Reclaiming free pages. A full rebuild pauses ingestion until it finishes.</p>';
 if(job.status==='deleted')content=`<h2>Archive complete</h2><p>${job.deleted_records.toLocaleString()} records deleted · ${bytes(job.reclaimed_bytes)} reclaimed</p>${job.compaction_error?`<p class="failure-detail">${esc(job.compaction_error)}</p><button id="archivecompact">${job.rebuild_required?'Rebuild database · pauses ingestion':'Retry compaction'}</button>`:''}`;
 if(job.status==='failed')content=`<h2 class="failure-detail">Archive failed</h2><p>${esc(job.error)}</p>${job.deleted_records?`<p>${job.deleted_records} records already deleted.</p>`:''}`;
 if(job.status==='canceled')content='<p>Archive canceled. DB records retained.</p>';
 $('#archivejob').innerHTML=content;
 if($('#archivecancel'))$('#archivecancel').onclick=()=>archiveAction('/api/archives/'+job.id+'/cancel',{});
 if($('#archivecompact'))$('#archivecompact').onclick=()=>archiveAction('/api/archives/'+job.id+'/compact',{rebuild:job.rebuild_required});
 if($('#archiveconfirmation')){$('#archiveconfirmation').oninput=()=>$('#archivedelete').disabled=$('#archiveconfirmation').value!=='DOWNLOADED';$('#archivedeleteform').onsubmit=async e=>{e.preventDefault();$('#archivedelete').disabled=true;await archiveAction('/api/archives/'+job.id+'/delete',{confirmation:$('#archiveconfirmation').value})}};
}
function archiveProgress(job){return job.status==='deleting'?`${job.deleted_records.toLocaleString()} / ${job.record_count.toLocaleString()} records`:`${job.processed_records.toLocaleString()} / ${job.record_count.toLocaleString()} records · ${bytes(job.archive_bytes)}`}
async function archiveAction(path,body){try{await archivePost(path,body);archiveSelection.clear();archiveAll=false;await refresh()}catch(e){const target=$('#archiveactionerror')||$('#error');target.textContent=e.message;if($('#archiveconfirmation'))$('#archiveconfirmation').oninput()}}
$('#archiveall').onclick=()=>{archiveAll=true;archiveSelection.clear();updateArchiveSelection()};$('#archiveclear').onclick=()=>{archiveAll=false;archiveSelection.clear();updateArchiveSelection()};
$('#archivecreate').onclick=async()=>{$('#archivecreate').disabled=true;await archiveAction('/api/archives',{cutoff:archiveData.cutoff,all:archiveAll,sessions:archiveAll?[]:[...archiveSelection]})};
$('#archiveprev').onclick=()=>{archiveOffset=Math.max(0,archiveOffset-200);refresh()};$('#archivenext').onclick=()=>{archiveOffset+=200;refresh()};
setInterval(()=>{if(!document.hidden&&(!selected||$('#autorefresh').checked)&&!window.getSelection()?.toString())refresh()},5000);refresh();
