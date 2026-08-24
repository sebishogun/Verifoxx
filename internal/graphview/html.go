package graphview

import "strings"

const htmlPrefix = `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Verifoxx semantic graph</title><style>
:root{color-scheme:dark;font-family:ui-monospace,SFMono-Regular,Menlo,monospace;background:#020617;color:#e2e8f0}*{box-sizing:border-box}body{margin:0;display:grid;grid-template-rows:auto 1fr;height:100vh}.toolbar{display:flex;gap:.5rem;align-items:center;padding:.75rem 1rem;border-bottom:1px solid #334155;background:#0f172a}.toolbar button{font:inherit;color:#e2e8f0;background:#1e293b;border:1px solid #475569;border-radius:.35rem;padding:.35rem .65rem;cursor:pointer}.toolbar button[aria-pressed=true]{border-color:#22d3ee;color:#67e8f9}.live-state{margin-left:auto;color:#94a3b8}.workspace{min-height:0;display:grid;grid-template-columns:minmax(0,1fr) 20rem}.graph-pane{min-width:0;min-height:0;overflow:hidden}.graph-pane[hidden]{display:none}.graph-pane svg{width:100%;height:100%;touch-action:none;user-select:none}.inspector{border-left:1px solid #334155;padding:1rem;background:#0f172a;overflow:auto}.inspector h2{font-size:1rem;margin:.25rem 0}.inspector code{color:#67e8f9}.node{cursor:pointer}.node.selected rect,.node.current-live rect{stroke:#facc15;stroke-width:4}.node.breakpoint-live rect{stroke:#ef4444;stroke-width:4}.node.watch-live rect{stroke:#a78bfa;stroke-width:4}body[data-truth=true] .node.current-live rect{stroke:#22c55e}body[data-truth=false] .node.current-live rect{stroke:#ef4444}body[data-truth=both] .node.current-live rect{stroke:#d946ef}</style></head><body data-initial-view="ast"><nav class="toolbar" aria-label="graph controls"><button type="button" data-view="ast" aria-pressed="true">AST</button><button type="button" data-view="program" aria-pressed="false">Program</button><button type="button" data-action="zoom-in">Zoom +</button><button type="button" data-action="zoom-out">Zoom -</button><button type="button" data-action="fit">Fit</button><span id="live-state-label" class="live-state" aria-live="polite"></span></nav><div class="workspace"><section id="ast-pane" class="graph-pane" aria-label="AST semantic graph">`

var htmlProgramPrefix = strings.Replace(htmlPrefix, `data-initial-view="ast"`, `data-initial-view="program"`, 1)
var htmlLivePrefix = strings.Replace(htmlPrefix, `<body `, `<body data-live-state="true" `, 1)

const htmlMiddle = `</section><section id="program-pane" class="graph-pane" aria-label="Program semantic graph" hidden>`

const htmlSuffix = `</section><aside class="inspector" aria-live="polite"><h2>node details</h2><div id="node-details">Select a node.</div><h2>source span</h2><code id="source-span">none</code></aside></div><script>
(()=>{'use strict';
const panes={ast:document.querySelector('#ast-pane'),program:document.querySelector('#program-pane')};
let active=document.body.dataset.initialView==='program'?'program':'ast';
const transforms=new WeakMap(),selected={ast:null,program:null};
function svg(){return panes[active].querySelector('svg')}
function state(s){let v=transforms.get(s);if(!v){v={x:0,y:0,k:1};transforms.set(s,v)}return v}
function apply(s){const v=state(s);s.querySelector('.viewport').setAttribute('transform','translate('+v.x+' '+v.y+') scale('+v.k+')')}
function fit(){const s=svg(),v=state(s);v.x=0;v.y=0;v.k=1;apply(s)}
function zoom(f){const s=svg(),v=state(s);v.k=Math.min(8,Math.max(.2,v.k*f));apply(s)}
document.querySelectorAll('[data-view]').forEach(b=>b.addEventListener('click',()=>{active=b.dataset.view;for(const [name,pane] of Object.entries(panes)){pane.hidden=name!==active}document.querySelectorAll('[data-view]').forEach(x=>x.setAttribute('aria-pressed',String(x===b)));fit()}));
document.querySelector('[data-action="zoom-in"]').addEventListener('click',()=>zoom(1.2));
document.querySelector('[data-action="zoom-out"]').addEventListener('click',()=>zoom(1/1.2));
document.querySelector('[data-action="fit"]').addEventListener('click',fit);
for(const pane of Object.values(panes)){const s=pane.querySelector('svg');let drag=null;
s.addEventListener('wheel',e=>{e.preventDefault();zoom(e.deltaY<0?1.1:1/1.1)},{passive:false});
s.addEventListener('pointerdown',e=>{drag={x:e.clientX,y:e.clientY};s.setPointerCapture(e.pointerId)});
s.addEventListener('pointermove',e=>{if(!drag)return;const v=state(s);v.x+=e.clientX-drag.x;v.y+=e.clientY-drag.y;drag={x:e.clientX,y:e.clientY};apply(s)});
s.addEventListener('pointerup',e=>{drag=null;s.releasePointerCapture(e.pointerId)});
s.querySelectorAll('.node').forEach(n=>n.addEventListener('click',()=>{if(selected[active])selected[active].classList.remove('selected');selected[active]=n;n.classList.add('selected');document.querySelector('#node-details').textContent=n.dataset.detail||n.querySelector('text').textContent;document.querySelector('#source-span').textContent='['+n.dataset.sourceStart+','+n.dataset.sourceEnd+')'}))}
function show(name){active=name==='program'?'program':'ast';for(const [view,pane] of Object.entries(panes)){pane.hidden=view!==active}document.querySelectorAll('[data-view]').forEach(x=>x.setAttribute('aria-pressed',String(x.dataset.view===active)))}
function applyLive(value){show(value.mode);document.querySelectorAll('.current-live,.breakpoint-live,.watch-live').forEach(n=>n.classList.remove('current-live','breakpoint-live','watch-live'));const current=value.mode==='program'?value.current_instruction:value.current_node;const node=document.querySelector('#'+value.mode+'-node-'+current);if(node)node.classList.add('current-live');for(const item of value.breakpoints||[]){const target=document.querySelector('#ast-node-'+item.node);if(target)target.classList.add('breakpoint-live')}for(const item of value.watches||[]){const target=document.querySelector('#program-node-'+item.instruction);if(target)target.classList.add('watch-live')}document.body.dataset.truth=value.truth||'neither';document.body.dataset.selectedRow=String(value.selected_row);document.body.dataset.requestId=String(value.request_id);document.querySelector('#live-state-label').textContent='Request #'+value.request_id+' | row '+(value.selected_row+1)+' | '+value.truth}
async function poll(){try{const response=await fetch('/state',{cache:'no-store',credentials:'same-origin'});if(response.ok)applyLive(await response.json())}catch(_){/* the terminal debugger remains authoritative */}finally{setTimeout(poll,200)}}
show(active);fit();if(document.body.dataset.liveState==='true')poll()})();
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
