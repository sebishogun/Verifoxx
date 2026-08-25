package graphview

import "strings"

const htmlPrefix = `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Verifoxx semantic graph</title><style>
:root{color-scheme:dark;font-family:ui-monospace,SFMono-Regular,Menlo,monospace;background:#020617;color:#e2e8f0}
*{box-sizing:border-box}
body{margin:0;display:grid;grid-template-rows:auto 1fr;height:100vh}
.toolbar{display:flex;gap:.5rem;align-items:center;padding:.75rem 1rem;border-bottom:1px solid #334155;background:#0f172a}
.toolbar button{font:inherit;color:#e2e8f0;background:#1e293b;border:1px solid #475569;border-radius:.35rem;padding:.35rem .65rem;cursor:pointer}
.toolbar button:focus-visible{outline:2px solid #f8fafc;outline-offset:2px}
.toolbar button[aria-pressed=true]{border-color:#22d3ee;color:#67e8f9}
.live-state{margin-left:auto;color:#94a3b8}
.workspace{min-height:0;display:grid;grid-template-columns:minmax(0,1fr) 20rem}
.graph-pane{min-width:0;min-height:0;overflow:hidden}
.graph-pane[hidden]{display:none}
.graph-pane svg{width:100%;height:100%;touch-action:none;user-select:none}
.inspector{border-left:1px solid #334155;padding:1rem;background:#0f172a;overflow:auto}
.inspector h2{font-size:1rem;margin:.75rem 0 .25rem}
.inspector code{color:#67e8f9}
.inspector ul{margin:.25rem 0;padding-left:1.25rem}
.inspector li{margin:.25rem 0;overflow-wrap:anywhere}
.keyboard-help{color:#94a3b8;font-size:.8rem;line-height:1.4}
.node{cursor:pointer;outline:none}
.node.selected rect,.node.current-live rect{stroke:#facc15;stroke-width:4}
.node:focus-visible rect{stroke:#f8fafc;stroke-width:5}
.node.breakpoint-live rect{stroke:#ef4444;stroke-width:4}
.node.watch-live rect{stroke:#a78bfa;stroke-width:4}
.edge{transition:opacity .12s ease}
.edge-label[data-colliding=true]{display:none}
.edge.related .edge-path{stroke-width:4}
.has-selection .edge:not(.related){opacity:.28}
.has-selection .edge.related{opacity:1}
body[data-truth=true] .node.current-live rect{stroke:#22c55e}
body[data-truth=false] .node.current-live rect{stroke:#ef4444}
body[data-truth=both] .node.current-live rect{stroke:#d946ef}
@media(max-width:800px){
body{height:100vh;height:100dvh}
.toolbar{flex-wrap:wrap}
.live-state{flex-basis:100%;margin-left:0}
.workspace{grid-template-columns:minmax(0,1fr);grid-template-rows:minmax(0,1fr) minmax(9rem,35vh)}
.inspector{border-left:0;border-top:1px solid #334155}
}
</style></head><body data-initial-view="ast"><nav class="toolbar" aria-label="graph controls"><button type="button" data-view="ast" aria-pressed="true">AST</button><button type="button" data-view="program" aria-pressed="false">Program</button><button type="button" data-action="zoom-in">Zoom +</button><button type="button" data-action="zoom-out">Zoom -</button><button type="button" data-action="fit">Fit</button><span id="live-state-label" class="live-state" aria-live="polite"></span></nav><div class="workspace"><section id="ast-pane" class="graph-pane" aria-label="AST semantic graph">`

var htmlProgramPrefix = strings.Replace(htmlPrefix, `data-initial-view="ast"`, `data-initial-view="program"`, 1)
var htmlLivePrefix = strings.Replace(htmlPrefix, `<body `, `<body data-live-state="true" `, 1)

const htmlMiddle = `</section><section id="program-pane" class="graph-pane" aria-label="Program semantic graph" hidden>`

const htmlSuffix = `</section><aside class="inspector" aria-live="polite"><h2>node details</h2><div id="node-details">Select a node.</div><h2>source span</h2><code id="source-span">none</code><h2>relationships</h2><div id="relationships">Select a node.</div><p class="keyboard-help">Tab to the graph. Use arrow keys to navigate nodes; Enter or Space selects.</p></aside></div><script>
(()=>{'use strict';
const panes={ast:document.querySelector('#ast-pane'),program:document.querySelector('#program-pane')};
const details=document.querySelector('#node-details'),source=document.querySelector('#source-span'),relationships=document.querySelector('#relationships');
let active=document.body.dataset.initialView==='program'?'program':'ast';
const transforms=new WeakMap(),selected={ast:null,program:null};
function svg(){return panes[active].querySelector('svg')}
function state(s){let v=transforms.get(s);if(!v){v={x:0,y:0,k:1};transforms.set(s,v)}return v}
function apply(s){const v=state(s);s.querySelector('.viewport').setAttribute('transform','translate('+v.x+' '+v.y+') scale('+v.k+')')}
function fit(){const s=svg(),v=state(s);v.x=0;v.y=0;v.k=1;apply(s)}
function zoom(f){const s=svg(),v=state(s);v.k=Math.min(8,Math.max(.2,v.k*f));apply(s)}
function boxesOverlap(a,b,padding=2){return a.x<b.x+b.width+padding&&a.x+a.width+padding>b.x&&a.y<b.y+b.height+padding&&a.y+a.height+padding>b.y}
function resolveLabels(s){const labels=Array.from(s.querySelectorAll('.edge-label'));for(const label of labels)delete label.dataset.colliding;try{const occupied=Array.from(s.querySelectorAll('.node rect'),rect=>rect.getBBox());for(const label of labels){const box=label.querySelector('rect').getBBox();if(occupied.some(other=>boxesOverlap(box,other))){label.dataset.colliding='true'}else{occupied.push(box)}}}catch(_){for(const label of labels)delete label.dataset.colliding}}
function clearInspector(){details.textContent='Select a node.';source.textContent='none';relationships.textContent='Select a node.'}
function setInspector(s,node){details.textContent=node.dataset.detail||node.querySelector('text').textContent;source.textContent='['+node.dataset.sourceStart+','+node.dataset.sourceEnd+')';relationships.replaceChildren();const id=node.dataset.nodeId,edges=Array.from(s.querySelectorAll('.edge')).filter(edge=>edge.dataset.from===id||edge.dataset.to===id);if(edges.length===0){relationships.textContent='No relationships.';return}const list=document.createElement('ul');for(const edge of edges){const outgoing=edge.dataset.from===id,target=outgoing?edge.dataset.to:edge.dataset.from,item=document.createElement('li');item.textContent=(outgoing?'out ':'in ')+edge.dataset.kind+(outgoing?' -> #':' <- #')+target+': '+edge.dataset.label;list.append(item)}relationships.append(list)}
function selectNode(view,node,focus){if(!node)return;const s=panes[view].querySelector('svg'),prior=selected[view];if(prior&&prior!==node){prior.classList.remove('selected');prior.setAttribute('aria-selected','false')}for(const candidate of s.querySelectorAll('.node[tabindex="0"]'))if(candidate!==node)candidate.setAttribute('tabindex','-1');selected[view]=node;node.classList.add('selected');node.setAttribute('aria-selected','true');node.setAttribute('tabindex','0');for(const edge of s.querySelectorAll('.edge')){const related=edge.dataset.from===node.dataset.nodeId||edge.dataset.to===node.dataset.nodeId;edge.classList.toggle('related',related)}s.classList.add('has-selection');setInspector(s,node);if(focus)node.focus()}
function centerX(node){const box=node.querySelector('rect').getBBox();return box.x+box.width/2}
function nearestNode(nodes,node){const x=centerX(node);return nodes.sort((left,right)=>Math.abs(centerX(left)-x)-Math.abs(centerX(right)-x)||Number(left.dataset.nodeId)-Number(right.dataset.nodeId))[0]||node}
function moveNode(s,node,key){const nodes=Array.from(s.querySelectorAll('.node')),id=node.dataset.nodeId,layer=Number(node.dataset.layer);let candidates=[];switch(key){case 'ArrowLeft':candidates=nodes.filter(candidate=>Number(candidate.dataset.layer)===layer&&centerX(candidate)<centerX(node));break;case 'ArrowRight':candidates=nodes.filter(candidate=>Number(candidate.dataset.layer)===layer&&centerX(candidate)>centerX(node));break;case 'ArrowUp':for(const edge of s.querySelectorAll('.edge[data-to="'+id+'"]')){const candidate=s.querySelector('.node[data-node-id="'+edge.dataset.from+'"]');if(candidate)candidates.push(candidate)}break;case 'ArrowDown':for(const edge of s.querySelectorAll('.edge[data-from="'+id+'"]')){const candidate=s.querySelector('.node[data-node-id="'+edge.dataset.to+'"]');if(candidate)candidates.push(candidate)}break;default:return node}if(candidates.length===0&&(key==='ArrowUp'||key==='ArrowDown')){const layers=nodes.map(candidate=>Number(candidate.dataset.layer)).filter(candidateLayer=>key==='ArrowUp'?candidateLayer<layer:candidateLayer>layer);if(layers.length){const targetLayer=key==='ArrowUp'?Math.max(...layers):Math.min(...layers);candidates=nodes.filter(candidate=>Number(candidate.dataset.layer)===targetLayer)}}return nearestNode(Array.from(new Set(candidates)),node)}
document.querySelectorAll('[data-view]').forEach(button=>button.addEventListener('click',()=>{show(button.dataset.view);fit()}));
document.querySelector('[data-action="zoom-in"]').addEventListener('click',()=>zoom(1.2));
document.querySelector('[data-action="zoom-out"]').addEventListener('click',()=>zoom(1/1.2));
document.querySelector('[data-action="fit"]').addEventListener('click',fit);
for(const [view,pane] of Object.entries(panes)){const s=pane.querySelector('svg');let drag=null;
s.addEventListener('wheel',e=>{e.preventDefault();zoom(e.deltaY<0?1.1:1/1.1)},{passive:false});
s.addEventListener('pointerdown',e=>{drag={x:e.clientX,y:e.clientY};s.setPointerCapture(e.pointerId)});
s.addEventListener('pointermove',e=>{if(!drag)return;const v=state(s);v.x+=e.clientX-drag.x;v.y+=e.clientY-drag.y;drag={x:e.clientX,y:e.clientY};apply(s)});
s.addEventListener('pointerup',e=>{drag=null;s.releasePointerCapture(e.pointerId)});
s.querySelectorAll('.node').forEach(node=>{node.addEventListener('click',()=>selectNode(view,node,true));node.addEventListener('focus',()=>selectNode(view,node,false));node.addEventListener('keydown',e=>{if(e.key==='Enter'||e.key===' '){e.preventDefault();selectNode(view,node,false);return}if(!e.key.startsWith('Arrow'))return;e.preventDefault();selectNode(view,moveNode(s,node,e.key),true)})})}
function show(name){const next=name==='program'?'program':'ast';const changed=active!==next;active=next;for(const [view,pane] of Object.entries(panes))pane.hidden=view!==active;document.querySelectorAll('[data-view]').forEach(button=>button.setAttribute('aria-pressed',String(button.dataset.view===active)));if(selected[active])setInspector(svg(),selected[active]);else clearInspector();if(changed)requestAnimationFrame(()=>resolveLabels(svg()))}
function applyLive(value){show(value.mode);document.querySelectorAll('.current-live,.breakpoint-live,.watch-live').forEach(node=>node.classList.remove('current-live','breakpoint-live','watch-live'));const current=value.mode==='program'?value.current_instruction:value.current_node,node=document.querySelector('#'+value.mode+'-node-'+current);if(node)node.classList.add('current-live');for(const item of value.breakpoints||[]){const target=document.querySelector('#ast-node-'+item.node);if(target)target.classList.add('breakpoint-live')}for(const item of value.watches||[]){const target=document.querySelector('#program-node-'+item.instruction);if(target)target.classList.add('watch-live')}document.body.dataset.truth=value.truth||'neither';document.body.dataset.selectedRow=String(value.selected_row);document.body.dataset.requestId=String(value.request_id);document.querySelector('#live-state-label').textContent='Request #'+value.request_id+' | row '+(value.selected_row+1)+' | '+value.truth}
async function poll(){try{const response=await fetch('/state',{cache:'no-store',credentials:'same-origin'});if(response.ok)applyLive(await response.json())}catch(_){/* the terminal debugger remains authoritative */}finally{setTimeout(poll,200)}}
show(active);fit();requestAnimationFrame(()=>resolveLabels(svg()));if(document.body.dataset.liveState==='true')poll()})();
</script></body></html>`

// AppendHTML appends one dependency-free interactive document containing both
// AST and Program graphs.
func (renderer *Renderer) AppendHTML(dst []byte, astGraph, programGraph *Graph) ([]byte, error) {
	return renderer.appendHTML(dst, astGraph, programGraph, false, false)
}

// AppendHTMLView appends an interactive document with the requested initial
// graph while retaining both AST and Program tabs.
func (renderer *Renderer) AppendHTMLView(dst []byte, astGraph, programGraph *Graph, programInitial bool) ([]byte, error) {
	return renderer.appendHTML(dst, astGraph, programGraph, programInitial, false)
}

// AppendLiveHTML appends the interactive graph document with bounded live-state
// polling enabled for a loopback viewer.
func (renderer *Renderer) AppendLiveHTML(dst []byte, astGraph, programGraph *Graph) ([]byte, error) {
	return renderer.appendHTML(dst, astGraph, programGraph, false, true)
}

func (renderer *Renderer) appendHTML(dst []byte, astGraph, programGraph *Graph, programInitial, live bool) ([]byte, error) {
	if renderer == nil {
		return dst, ErrInvalidGraph
	}
	start := len(dst)
	prefix := htmlPrefix
	if live {
		prefix = htmlLivePrefix
	} else if programInitial {
		prefix = htmlProgramPrefix
	}
	dst = append(dst, prefix...)
	var err error
	dst, err = renderer.appendSVG(dst, astGraph, "ast")
	if err != nil {
		return dst[:start], err
	}
	dst = append(dst, htmlMiddle...)
	dst, err = renderer.appendSVG(dst, programGraph, "program")
	if err != nil {
		return dst[:start], err
	}
	dst = append(dst, htmlSuffix...)
	return dst, nil
}
