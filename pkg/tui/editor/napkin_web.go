package editor

// napkinAppHTML is the self-contained drawing app served at "/" by launchNapkin.
// It draws on a transparent canvas (so unpainted areas stay transparent and the
// terminal preview keeps its gaps), loads any existing drawing from /image, and on
// Save posts the canvas PNG (as a data URL) to /save. Closing the tab posts
// /cancel so the temporary server shuts down.
const napkinAppHTML = `<!doctype html>
<html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>napkin</title>
<style>
  :root{--bg:#1e2230;--panel:#262b3a;--fg:#d4d4d4;--dim:#7a7a7a;--accent:#4ec9b0}
  *{box-sizing:border-box}
  body{margin:0;background:var(--bg);color:var(--fg);font:14px/1.4 ui-monospace,Menlo,Consolas,monospace;
       display:flex;flex-direction:column;align-items:center;gap:12px;padding:16px;height:100vh}
  #bar{display:flex;gap:10px;align-items:center;flex-wrap:wrap;justify-content:center}
  .sw{width:22px;height:22px;border-radius:4px;border:2px solid #0000;cursor:pointer}
  .sw.on{border-color:var(--fg)}
  button,input[type=color]{font:inherit;color:var(--fg);background:var(--panel);
       border:1px solid #3a4a5a;border-radius:6px;padding:6px 10px;cursor:pointer}
  button.primary{background:var(--accent);color:#06201b;border-color:var(--accent);font-weight:600}
  #wrap{flex:1;display:flex;align-items:center;justify-content:center;min-height:0}
  /* checkerboard so transparency is visible while drawing */
  canvas{background:
    repeating-conic-gradient(#2b3142 0 25%, #232838 0 50%) 0 0/20px 20px;
    border-radius:8px;box-shadow:0 6px 24px #0008;cursor:crosshair;touch-action:none;max-width:96vw;max-height:100%}
  #hint{color:var(--dim)}
  label{color:var(--dim);display:flex;gap:6px;align-items:center}
</style></head>
<body>
  <div id="bar">
    <span id="sws"></span>
    <input type="color" id="custom" value="#4ec9b0" title="custom color">
    <button id="eraser">Eraser</button>
    <label>size <input type="range" id="size" min="1" max="40" value="6"></label>
    <button id="clear">Clear</button>
    <button id="save" class="primary">Save &amp; close</button>
  </div>
  <div id="wrap"><canvas id="c" width="640" height="400"></canvas></div>
  <div id="hint">draw with the mouse · Save sends it back to lflow</div>
<script>
const cv=document.getElementById('c'),ctx=cv.getContext('2d');
let color='#4ec9b0',size=6,erase=false,drawing=false,last=null;
const palette=['#eeeeee','#f44747','#e19646','#dccd5a','#78b45a','#4ec9b0','#569cd6','#c586c0','#969696','#202024'];
const sws=document.getElementById('sws');
palette.forEach((c,i)=>{const d=document.createElement('span');d.className='sw'+(i===5?' on':'');d.style.background=c;
  d.onclick=()=>{color=c;erase=false;document.getElementById('eraser').classList.remove('on');
    [...sws.children].forEach(s=>s.classList.remove('on'));d.classList.add('on');};sws.appendChild(d);});
document.getElementById('custom').oninput=e=>{color=e.target.value;erase=false;
  document.getElementById('eraser').classList.remove('on');[...sws.children].forEach(s=>s.classList.remove('on'));};
document.getElementById('size').oninput=e=>size=+e.target.value;
document.getElementById('eraser').onclick=e=>{erase=!erase;e.target.classList.toggle('on',erase);};
document.getElementById('clear').onclick=()=>ctx.clearRect(0,0,cv.width,cv.height);
function pos(e){const r=cv.getBoundingClientRect();return{x:(e.clientX-r.left)*cv.width/r.width,
  y:(e.clientY-r.top)*cv.height/r.height};}
function stroke(a,b){ctx.globalCompositeOperation=erase?'destination-out':'source-over';
  ctx.strokeStyle=color;ctx.lineWidth=size;ctx.lineCap='round';ctx.lineJoin='round';
  ctx.beginPath();ctx.moveTo(a.x,a.y);ctx.lineTo(b.x,b.y);ctx.stroke();}
cv.addEventListener('pointerdown',e=>{drawing=true;last=pos(e);stroke(last,last);cv.setPointerCapture(e.pointerId);});
cv.addEventListener('pointermove',e=>{if(!drawing)return;const p=pos(e);stroke(last,p);last=p;});
addEventListener('pointerup',()=>drawing=false);
// load any existing drawing
fetch('/image').then(r=>r.ok?r.blob():null).then(b=>{if(!b)return;const i=new Image();
  i.onload=()=>ctx.drawImage(i,0,0,cv.width,cv.height);i.src=URL.createObjectURL(b);});
let saved=false;
document.getElementById('save').onclick=async()=>{saved=true;
  await fetch('/save',{method:'POST',body:cv.toDataURL('image/png')});
  document.getElementById('hint').textContent='saved — you can close this tab';
  try{window.close();}catch(_){}};
addEventListener('pagehide',()=>{if(!saved)navigator.sendBeacon('/cancel');});
</script></body></html>`
