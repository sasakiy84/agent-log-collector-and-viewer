'use strict';
let skillsQuery='',skillsOffset=0,skillsData=null,skillsPager=null;
function skillSessionURL(name,plugin){return '/?'+new URLSearchParams({skill:name,plugin:plugin||''})}
function sessionSkillsHTML(rows){return rows?.length?`<span class="muted">Skills</span>${rows.map(row=>`<button type="button" class="session-skill" data-skill-event="${esc(row.event)}" title="View latest skill event">${esc(row.name)} <small>${row.uses?'×'+row.uses.toLocaleString():'reference'}</small></button>`).join('')}<a class="skill-browse" href="/skills">Browse skills</a>`:''}
function eventSkillsHTML(rows){return rows?.length?`<div class="event-skills">${rows.map(row=>`<a href="${esc(skillSessionURL(row.name,row.plugin))}">${row.evidence==='reported'?'Skill':'Skill reference'}: ${esc(row.name)}</a>`).join('')}</div>`:''}
function bindSkillEvents(root){root.querySelectorAll('[data-skill-event]').forEach(button=>button.onclick=()=>{openInspector(button.dataset.skillEvent);inspectorTrigger=button})}
async function refreshSkills(signal){
 const params=new URLSearchParams({offset:String(skillsOffset)});if(skillsQuery)params.set('q',skillsQuery);
 const data=await api('/api/skills?'+params,signal);if(signal?.aborted)return;
 skillsData={...data,query:skillsQuery};skillsOffset=data.offset;
 $('#skillcount').textContent=data.total.toLocaleString()+' skills';$('#skillindexstatus').textContent=data.ready?'':'Indexing history';
 $('#skillrows').innerHTML=data.skills.length?data.skills.map(row=>`<a class="skill-row" href="${esc(skillSessionURL(row.name,row.plugin))}"><div><h2>${esc(row.name)}</h2><small>${esc([row.plugin,...row.scopes.filter(Boolean).slice(0,16)].filter(Boolean).join(' · '))}</small></div><span>${row.sessions.toLocaleString()}</span><span>${row.uses?row.uses.toLocaleString():'—'}</span><span>${row.references.toLocaleString()}</span><time>${esc(date(row.last))}</time></a>`).join(''):'<p class="empty">No skills found</p>';
 skillsPager.update({total:data.total,offset:data.offset,size:200});const url=new URLSearchParams();if(skillsQuery)url.set('q',skillsQuery);if(skillsOffset)url.set('offset',skillsOffset);history.replaceState(null,'','/skills'+(url.size?'?'+url:''));
}
function setupSkills(){
 skillsQuery=new URL(location.href).searchParams.get('q')||'';skillsOffset=Number(new URL(location.href).searchParams.get('offset'))||0;$('#skillsearch').value=skillsQuery;
 skillsPager=new Pagination($('#skillpagination'),{noun:'skills',onChange:page=>{skillsOffset=(page-1)*200;refresh('top')}});
 let timer,composing=false;const search=()=>{clearTimeout(timer);timer=setTimeout(()=>{skillsQuery=$('#skillsearch').value;skillsOffset=0;refresh()},250)};
 $('#skillsearch').oninput=event=>{if(!composing&&!event.isComposing)search()};$('#skillsearch').oncompositionstart=()=>{composing=true;clearTimeout(timer)};$('#skillsearch').oncompositionend=()=>{composing=false;search()};
 $('#clearskillfilter').onclick=()=>{skillFilter='';skillPlugin='';sessionOffset=0;refresh()};
}
