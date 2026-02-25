// Theme toggle: switch between dark and light mode.
function toggleTheme(){
  var h=document.documentElement;
  var next=h.dataset.theme==='light'?'dark':'light';
  h.dataset.theme=next;
  localStorage.setItem('theme',next);
}

// Prevent FOUC: show body after fonts and styles are ready.
(function(){
  function reveal(){
    requestAnimationFrame(function(){
      document.body.classList.add('ready');
    });
  }
  if(document.fonts&&document.fonts.ready){
    document.fonts.ready.then(reveal);
  }else{
    reveal();
  }
  // Fallback: ensure body is visible even if fonts stall.
  setTimeout(function(){document.body.classList.add('ready')},800);
})();

// SSE live update: event-driven push from server on data changes.
(function(){
  var app=document.getElementById('app');
  if(!app)return;
  var es=new EventSource('/api/paper/stream');
  es.onmessage=function(e){
    var tmp=document.createElement('div');
    tmp.innerHTML=e.data;
    morphdom(app,tmp,{
      childrenOnly:true,
      onBeforeElUpdated:function(from,to){
        // Preserve user-interactive state across morphdom patches.
        if(from.tagName==='DETAILS'){to.open=from.open}
        if(from.tagName==='INPUT'&&(from.type==='radio'||from.type==='checkbox')){
          to.checked=from.checked;
        }
        // Skip overwriting client-side live-updated elements.
        if(from.dataset&&from.dataset.live){return false}
        return true;
      }
    });
  };
  es.onerror=function(){
    // Browser auto-reconnects EventSource on error.
  };
})();

// Client-side time updater: updates clock, start time and duration every second
// so the backend only needs to push on actual data events.
// All displayed times use the browser's local timezone.
(function(){
  var app=document.getElementById('app');
  if(!app)return;
  var startTs=parseInt(app.dataset.startTs,10);
  if(!startTs)return;
  var serverTZ=app.dataset.serverTz||'';

  function pad(n){return n<10?'0'+n:''+n}

  // Format a Date in browser local timezone.
  function fmtClock(d){
    return d.getFullYear()+'-'+pad(d.getMonth()+1)+'-'+pad(d.getDate())+' '+
           pad(d.getHours())+':'+pad(d.getMinutes())+':'+pad(d.getSeconds());
  }

  // Get browser timezone as UTC offset string (e.g. "UTC+8", "UTC-5:30").
  function browserTZ(){
    var off=-new Date().getTimezoneOffset();
    if(off===0)return 'UTC';
    var sign=off>0?'+':'-';
    var abs=Math.abs(off);
    var h=Math.floor(abs/60);
    var m=abs%60;
    return m?'UTC'+sign+h+':'+pad(m):'UTC'+sign+h;
  }

  function fmtDuration(ms){
    var s=Math.floor(ms/1000);
    if(s<0)s=0;
    var h=Math.floor(s/3600);s%=3600;
    var m=Math.floor(s/60);s%=60;
    if(h>0)return h+'h'+m+'m'+s+'s';
    if(m>0)return m+'m'+s+'s';
    return s+'s';
  }

  var startDate=new Date(startTs*1000);
  var startStr=fmtClock(startDate);
  var btz=browserTZ();
  var tzLabel=btz+(serverTZ&&serverTZ!==btz?' · Server '+serverTZ:'');

  function tick(){
    var now=new Date();
    var elapsed=now.getTime()-startTs*1000;

    var starts=document.querySelectorAll('[data-live="start"]');
    for(var i=0;i<starts.length;i++){starts[i].textContent=startStr}

    var clocks=document.querySelectorAll('[data-live="clock"]');
    for(var i=0;i<clocks.length;i++){clocks[i].textContent=fmtClock(now)}

    var durations=document.querySelectorAll('[data-live="duration"]');
    for(var i=0;i<durations.length;i++){durations[i].textContent=fmtDuration(elapsed)}

    var tzs=document.querySelectorAll('[data-live="tz"]');
    for(var i=0;i<tzs.length;i++){tzs[i].textContent=tzLabel}
  }

  tick();
  setInterval(tick,1000);
})();
