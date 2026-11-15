'use strict';
let subagentState=null,subagentHistory=[],subagentRequest=null,subagentPager=null;
function subagentLinks(items){return items?.length?`<div class="subagent-links">${items.map(item=>`<a class="subagent-link" href="${esc(link(item.session))}" title="Preview subagent logs" aria-controls="inspector" data-subagent-session="${esc(item.session)}" data-subagent-agent="${esc(item.agent||'')}" data-subagent-label="${esc(item.label)}">Subagent: ${esc(item.label)}</a>`).join('')}</div>`:''}
function bindSubagentLinks(root){
 root.querySelectorAll('[data-subagent-session]').forEach(anchor=>anchor.onclick=event=>{
  if(event.button!==0||event.metaKey||event.ctrlKey||event.shiftKey||event.altKey)return;
  event.preventDefault();event.stopPropagation();
  const parent=anchor.closest('#timeline [data-event]');
  const fromDetails=!!anchor.closest('#inspectdetails');
  if(!subagentState&&!fromDetails)inspectorTrigger=anchor;
  openSubagent({session:anchor.dataset.subagentSession,agent:anchor.dataset.subagentAgent,label:anchor.dataset.subagentLabel},parent?.dataset.event||(fromDetails?inspectedID:undefined));
 });
}
function resetSubagentPreview(){
 subagentRequest?.abort();subagentRequest=null;subagentState=null;subagentHistory=[];
 $('#inspectagent').hidden=true;$('#subagentactions').hidden=true;$('.inspectortabs').hidden=false;
}
function showSubagentPreview(){
 const state=subagentState;
 $('#inspector').hidden=false;$('#detailpage').classList.add('inspector-open');
 $('.inspectortabs').hidden=true;$('#inspectdetails').hidden=true;$('#inspectjson').hidden=true;$('#inspectagent').hidden=false;$('#subagentactions').hidden=false;
 $('#inspectortitle').textContent='Subagent: '+state.label;$('#inspectortime').textContent=state.page?`${label(state.page.source)} · ${state.page.count.toLocaleString()} ${state.page.count===1?'event':'events'}`:'';
 $('#subagentfull').href=link(state.session);$('#subagentfull').hidden=!state.page;
 $('#subagentback').hidden=!subagentHistory.length&&!inspectedID;
 $('#subagentback').textContent=subagentHistory.length?'Back':'Event details';
 syncInspectorMode();
}
async function openSubagent(target,eventID){
 if(subagentState){subagentState.scroll=$('.inspectorbody').scrollTop;subagentHistory.push(subagentState)}
 else {subagentHistory=[];inspectorRequest?.abort();inspectedID=eventID||'';inspectedRecord=null;
  document.querySelectorAll('#timeline [data-event]').forEach(card=>{const active=card.dataset.event===inspectedID;card.classList.toggle('selected',active);card.setAttribute('aria-expanded',String(active))});
 }
 subagentState={...target,page:null};showSubagentPreview();$('#subagentevents').textContent='';$('#closeinspector').focus({preventScroll:true});
 await loadSubagent(null);
}
async function loadSubagent(offset){
 const state=subagentState;if(!state)return;
 subagentRequest?.abort();const controller=new AbortController();subagentRequest=controller;
 $('#inspectstatus').textContent='Loading subagent logs…';$('#subagentrefresh').disabled=true;subagentPager.setLoading(true);
 const params=new URLSearchParams(state.agent?{session:state.session,agent:state.agent}:{id:state.session,limit:'40'});
 params.set(offset===null?'position':'offset',offset===null?'latest':String(offset));
 try{
  const page=await api((state.agent?'/api/subagent?':'/api/session?')+params,controller.signal);
  if(controller.signal.aborted||subagentState!==state)return;
  state.page=page;state.scroll=offset===null?null:0;$('#inspectstatus').textContent='';renderSubagentPreview();
 }catch(error){
  if(error.name==='AbortError'||subagentState!==state)return;
  $('#inspectstatus').textContent=error.message==='Session not found.'?'':error.message;
  if(!state.page){$('#subagentevents').innerHTML=`<p class="preview-empty">${error.message==='Session not found.'?'Logs have not been collected.':'Unable to load subagent logs.'}</p>`;$('#subagentpagination').hidden=true}
 }finally{if(subagentState===state&&subagentRequest===controller){$('#subagentrefresh').disabled=false;subagentPager.setLoading(false)}}
}
function renderSubagentPreview(){
 const state=subagentState,page=state.page;showSubagentPreview();$('#subagentrefresh').disabled=false;subagentPager.setLoading(false);
 $('#subagentevents').innerHTML=page.events.length?page.events.slice(0,page.limit).map(event=>{
  const p=event.detail;
  return `<section class="preview-entry"><div class="preview-heading"><h3>${esc(p.title)}</h3>${p.error?'<span class="status failed">Failed</span>':''}<time datetime="${esc(event.time)}">${esc(date(event.time))}</time></div>${p.blocks.map(b=>`<div class="block">${['Request','Response','Message',p.kind].includes(b.label)?'':`<div class="blocklabel">${esc(b.label)}</div>`}<${b.code?'pre':'div'} class="${b.code?'code':'prose'}">${esc(b.text)}</${b.code?'pre':'div'}></div>`).join('')}${subagentLinks(event.subagents)}${p.fields.length?`<dl class="fields">${p.fields.map(([k,v])=>`<div><dt>${esc(k)}</dt><dd>${esc(v)}</dd></div>`).join('')}</dl>`:''}</section>`;
 }).join(''):'<p class="preview-empty">Logs have not been collected.</p>';
 bindSubagentLinks($('#subagentevents'));$('#subagentpagination').hidden=!page.count;subagentPager.update({total:page.count,offset:page.offset,size:page.limit});
 requestAnimationFrame(()=>{if(subagentState===state)$('.inspectorbody').scrollTop=state.scroll??$('.inspectorbody').scrollHeight});
}
function setupSubagentPreview(){
 subagentPager=new Pagination($('#subagentpagination'),{noun:'events',onChange:page=>loadSubagent((page-1)*40),onLatest:()=>loadSubagent(null)});
 $('#subagentrefresh').onclick=()=>loadSubagent(null);
 $('#subagentback').onclick=()=>{
  subagentRequest?.abort();$('#inspectstatus').textContent='';
  if(subagentHistory.length){subagentState=subagentHistory.pop();if(subagentState.page)renderSubagentPreview();else{showSubagentPreview();loadSubagent(null)}}
  else if(inspectedID)openInspector(inspectedID);
 };
}
