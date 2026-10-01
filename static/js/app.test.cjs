const { test } = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');
function setup() {
  const events = {}, requests = [], attrs = {};
  const pane = { innerHTML: '', setAttribute(k,v) { attrs[k]=v; }, querySelector(){return null;} };
  const links = ['one','two'].map(id => ({ getAttribute(k){return k==='href'?'/p/'+id:id;}, setAttribute(){}, scrollIntoView(){}, focus(){} }));
  const document = {
    documentElement:{classList:{add(){}}}, activeElement:{tagName:'BODY'},
    addEventListener(k, f){(events[k] ||= []).push(f);},
    getElementById(id){return id==='pane'?pane:null;},
    querySelectorAll(s){return s==='a[data-pane-link]'?links:[];},
    querySelector(s){return s==='a[data-pane-link]'?links[0]:null;},
  };
  const context = { document, window:{matchMedia(){return {matches:true,addEventListener(){}};}}, location:{hash:'',pathname:'/',search:''}, history:{replaceState(){}}, CSS:{escape:v=>v}, URLSearchParams, AbortController, setTimeout, clearTimeout, console,
    fetch(url, opts){return new Promise((resolve,reject)=>requests.push({url,opts,resolve,reject}));}
  };
  context.htmx=context.window.htmx={process(){},ajax(){}};
  vm.runInNewContext(fs.readFileSync(__dirname+'/app.js','utf8'),context);
  function click(i){for(const f of events.click||[]) f({target:{closest(s){return s==='a[data-pane-link]'?links[i]:null;}},button:0,preventDefault(){}});}
  return {requests,pane,attrs,click};
}
const settle=()=>new Promise(resolve=>setImmediate(resolve));
test('only the latest selected problem can replace the preview',async()=>{
  const s=setup();s.click(0);s.click(1);
  assert.equal(s.requests.length,2);
  s.requests[1].resolve({ok:true,text:async()=>'<article>two</article>'});await settle();
  s.requests[0].resolve({ok:true,text:async()=>'<article>one</article>'});await settle();
  assert.match(s.pane.innerHTML,/two/);assert.equal(s.attrs['aria-busy'],'false');
});
test('failed preview offers a regular full-page link and retry',async()=>{
  const s=setup();s.click(0);assert.equal(s.requests.length,1);
  s.requests[0].resolve({ok:false});await settle();
  assert.match(s.pane.innerHTML,/data-preview-retry/);assert.match(s.pane.innerHTML,/href="\/p\/one"/);assert.equal(s.attrs['aria-busy'],'false');
});
