(function(){
  var reduce = matchMedia('(prefers-reduced-motion: reduce)').matches;
  requestAnimationFrame(function(){ document.body.classList.add('loaded'); });

  var rv = [].slice.call(document.querySelectorAll('.rv'));
  if (reduce) rv.forEach(function(e){ e.classList.add('in'); });
  else {
    var io = new IntersectionObserver(function(es){
      es.forEach(function(e){
        if (!e.isIntersecting) return;
        var sib = [].slice.call(e.target.parentNode.children).filter(function(n){return n.classList.contains('rv');});
        e.target.style.transitionDelay = Math.min(Math.max(0,sib.indexOf(e.target)),8)*60 + 'ms';
        e.target.classList.add('in'); io.unobserve(e.target);
      });
    }, {rootMargin:'0px 0px -12% 0px', threshold:0.06});
    rv.forEach(function(e){ io.observe(e); });
  }

  var vio = new IntersectionObserver(function(es){
    es.forEach(function(e){
      if (e.isIntersecting) { if (!reduce) e.target.play().catch(function(){}); }
      else e.target.pause();
    });
  }, {threshold:0.2});
  document.querySelectorAll('video[data-lazy]').forEach(function(v){ vio.observe(v); });

  var fmt = new Intl.NumberFormat('en-US');
  var cio = new IntersectionObserver(function(es){
    es.forEach(function(e){
      if (!e.isIntersecting) return;
      var el = e.target, txt = el.getAttribute('data-text'); cio.unobserve(el);
      if (txt) { el.textContent = txt; return; }
      var to = parseInt(el.getAttribute('data-count'),10)||0;
      if (reduce) { el.textContent = fmt.format(to); return; }
      var t0 = performance.now();
      (function tick(now){
        var p = Math.min(1,(now-t0)/1150), k = 1-Math.pow(1-p,3);
        el.textContent = fmt.format(Math.round(to*k));
        if (p<1) requestAnimationFrame(tick);
      })(t0);
    });
  }, {threshold:0.5});
  document.querySelectorAll('[data-count]').forEach(function(e){ cio.observe(e); });

  var bar = document.getElementById('pbar'), tick = false;
  function frame(){
    tick = false;
    var vh = innerHeight, max = document.body.scrollHeight - vh;
    bar.style.width = (max>0 ? Math.min(1, scrollY/max)*100 : 0) + '%';
    var steps = [].slice.call(document.querySelectorAll('.st')), on = -1;
    steps.forEach(function(s,i){
      var r = s.getBoundingClientRect(), hit = r.top < vh*0.6 && r.bottom > vh*0.2;
      if (hit) on = i;
      s.setAttribute('data-on', hit ? '1':'0');
    });
    document.querySelectorAll('#nodes .nd').forEach(function(n,i){
      n.setAttribute('data-live', on>=0 && i<=on ? '1':'0');
    });
  }
  addEventListener('scroll', function(){ if(!tick){ tick=true; requestAnimationFrame(frame); } }, {passive:true});
  addEventListener('resize', frame); frame();

  /* Background. Contour lines drawn from a real depth-anything.cpp depth map
     everywhere, switching to a locate-anything.cpp style detection sweep while
     you are inside the engines section. The two are separated by a scan line
     that wipes across, so the change reads as a sensor switching mode. */
  (function(){
    var cv = document.getElementById('field'), ctx = cv.getContext('2d');
    var W=0, H=0, dpr = Math.min(devicePixelRatio||1, 2);
    var depth=null, disp=null, GW=0, GH=0, boxes=[], seeded=false, reticles=[];
    var blend=0, target=0, last=0, t0=performance.now(), run=true;

    var LABELS=['person 0.98','laptop 0.94','cup 0.91','chair 0.88','monitor 0.96',
                'bottle 0.79','keyboard 0.92','plant 0.83','phone 0.87','book 0.81'];
    var small=false, rings=[];
    function rnd(a,b){ return a+Math.random()*(b-a); }
    function newBox(delay){
      var w=rnd(100,320), h=rnd(80,230);
      var x, tries=0;
      /* keep boxes out of the middle third on wide screens, which is where the copy sits */
      do { x=rnd(16, Math.max(40, W-w-16)); tries++; }
      while (W>900 && tries<6 && x+w > W*0.22 && x < W*0.62);
      return {x:x, y:rnd(70,Math.max(120,H-h-40)), w:w, h:h,
              l:LABELS[Math.floor(Math.random()*LABELS.length)], t:-delay, life:rnd(3.6,7)};
    }
    function seedBoxes(stagger){
      var n = small ? 5 : 9;
      boxes=[]; for (var i=0;i<n;i++) boxes.push(newBox(stagger ? rnd(0,2.2) : rnd(0,6)));
    }
    function size(){
      W=innerWidth; H=innerHeight;
      small = W < 760;
      dpr = Math.min(devicePixelRatio||1, small ? 1.5 : 2);
      cv.width=W*dpr; cv.height=H*dpr; cv.style.width=W+'px'; cv.style.height=H+'px';
      ctx.setTransform(dpr,0,0,dpr,0,0);
      seedBoxes(false); seeded=true;
    }
    addEventListener('resize', size);
    addEventListener('orientationchange', size);

    /* clicking anywhere that is not a control pings the field:
       a ring travels out, and in detection mode something gets found there */
    document.addEventListener('pointerdown', function(e){
      if (e.target.closest('a,button,input,textarea,select,video,summary')) return;
      rings.push({x:e.clientX, y:e.clientY, t:0});
      if (rings.length>5) rings.shift();
      if (target===1){
        var w=rnd(120,220), h=rnd(96,168);
        reticles.push({cx:e.clientX, cy:e.clientY, w:w, h:h, t:0,
                       l:LABELS[Math.floor(Math.random()*LABELS.length)]});
        if (reticles.length>4) reticles.shift();
      }
    }, {passive:true});

    var img=new Image();
    img.onload=function(){
      var g=document.createElement('canvas'); GW=img.width; GH=img.height;
      g.width=GW; g.height=GH;
      var gx=g.getContext('2d',{willReadFrequently:true}); gx.drawImage(img,0,0);
      var d=gx.getImageData(0,0,GW,GH).data;
      depth=new Float32Array(GW*GH);
      for (var i=0;i<GW*GH;i++) depth[i]=d[i*4]/255;
      disp=new Float32Array(GW*GH);
      size(); requestAnimationFrame(loop);
    };
    img.src='/img/depth-field.jpg';

    document.addEventListener('visibilitychange', function(){
      run=!document.hidden;
      if (run){ t0=performance.now(); last=0; requestAnimationFrame(loop); }
    });

    function glow(t){
      var x1=W*(.28+Math.sin(t*.05)*.14), y1=H*(.32+Math.cos(t*.04)*.13);
      var g1=ctx.createRadialGradient(x1,y1,0,x1,y1,Math.max(W,H)*.55);
      g1.addColorStop(0,'rgba(95,205,228,.13)'); g1.addColorStop(1,'rgba(95,205,228,0)');
      ctx.fillStyle=g1; ctx.fillRect(0,0,W,H);
      var x2=W*(.74+Math.cos(t*.043)*.15), y2=H*(.66+Math.sin(t*.037)*.14);
      var g2=ctx.createRadialGradient(x2,y2,0,x2,y2,Math.max(W,H)*.5);
      g2.addColorStop(0,'rgba(129,129,196,.11)'); g2.addColorStop(1,'rgba(129,129,196,0)');
      ctx.fillStyle=g2; ctx.fillRect(0,0,W,H);
    }

    function ripple(dt){
      if (!disp) return;
      disp.fill(0);
      if (!rings.length) return;
      for (var ri=0; ri<rings.length; ri++){
        var r=rings[ri];
        var gx=(r.x/W)*(GW-1), gy=(r.y/H)*(GH-1);
        var front=r.t*46, decay=Math.max(0, 1-r.t/1.5);
        if (decay<=0) continue;
        var amp=0.16*decay;
        for (var y=0;y<GH;y++){
          var dy=y-gy;
          for (var x=0;x<GW;x++){
            var dx=x-gx, d2=dx*dx+dy*dy;
            var dist=Math.sqrt(d2);
            var band=dist-front;
            if (band<-26 || band>26) continue;
            disp[y*GW+x] += amp*Math.cos(band*0.24)*Math.exp(-Math.abs(band)/13)*Math.exp(-dist/95);
          }
        }
      }
    }

    function contours(t, alpha){
      if (!depth) return;
      var sx=W/(GW-1), sy=H/(GH-1);
      var scrollN=scrollY/Math.max(1, document.body.scrollHeight-H);
      var drift=Math.sin(t*.09)*.05 + scrollN*.12;
      ctx.lineWidth=1;
      var LN = small ? 6 : 9;
      for (var li=0; li<LN; li++){
        var th=0.10+li*(small?0.14:0.093)+drift;
        if (th<=0.02||th>=0.99) continue;
        var a = (li<3 ? .22 : li<6 ? .27 : .32) * alpha;
        ctx.strokeStyle = li<3 ? 'rgba(129,129,196,'+a+')'
                        : li<6 ? 'rgba(95,205,228,'+a+')'
                               : 'rgba(189,240,250,'+a+')';
        ctx.beginPath();
        for (var y=0;y<GH-1;y++) for (var x=0;x<GW-1;x++){
          var i0=y*GW+x, i1=i0+1, i2=i0+GW+1, i3=i0+GW;
          var A=depth[i0]+disp[i0], B=depth[i1]+disp[i1], C=depth[i2]+disp[i2], D=depth[i3]+disp[i3];
          var id=(A>th?8:0)|(B>th?4:0)|(C>th?2:0)|(D>th?1:0);
          if (id===0||id===15) continue;
          var X=x*sx, Y=y*sy;
          var T=[X+sx*.5,Y], R=[X+sx,Y+sy*.5], Bo=[X+sx*.5,Y+sy], L=[X,Y+sy*.5], seg=null;
          if (id===1||id===14) seg=[L,Bo]; else if (id===2||id===13) seg=[Bo,R];
          else if (id===3||id===12) seg=[L,R]; else if (id===4||id===11) seg=[T,R];
          else if (id===6||id===9) seg=[T,Bo]; else seg=[L,T];
          ctx.moveTo(seg[0][0],seg[0][1]); ctx.lineTo(seg[1][0],seg[1][1]);
        }
        ctx.stroke();
      }
    }

    function detections(dt, alpha){
      ctx.font='10px ui-monospace, SFMono-Regular, monospace';
      for (var i=0;i<boxes.length;i++){
        var b=boxes[i]; b.t+=dt;
        if (b.t>b.life){ boxes[i]=newBox(0); continue; }
        if (b.t<0) continue;
        var inK=Math.min(1,b.t/.5), outK=Math.min(1,(b.life-b.t)/.7), al=Math.min(inK,outK)*alpha;
        var grow=1-Math.pow(1-inK,3), w=b.w*grow, h=b.h*grow;
        ctx.globalAlpha=al*.45; ctx.strokeStyle='#5FCDE4'; ctx.lineWidth=1.4;
        ctx.strokeRect(b.x,b.y,w,h);
        ctx.globalAlpha=al*.09; ctx.fillStyle='#5FCDE4'; ctx.fillRect(b.x,b.y,w,h);
        /* corner ticks, the way a detector overlay usually draws them */
        ctx.globalAlpha=al*.75; ctx.strokeStyle='#BDF0FA'; ctx.lineWidth=2; var c=9;
        ctx.beginPath();
        ctx.moveTo(b.x,b.y+c); ctx.lineTo(b.x,b.y); ctx.lineTo(b.x+c,b.y);
        ctx.moveTo(b.x+w-c,b.y+h); ctx.lineTo(b.x+w,b.y+h); ctx.lineTo(b.x+w,b.y+h-c);
        ctx.stroke();
        if (inK>.55){
          var tw=ctx.measureText(b.l).width+12;
          ctx.globalAlpha=al*.62; ctx.fillStyle='#5FCDE4'; ctx.fillRect(b.x,b.y-14,tw,14);
          ctx.globalAlpha=al*.95; ctx.fillStyle='#04131C'; ctx.fillText(b.l,b.x+6,b.y-4);
        }
      }
      ctx.globalAlpha=1;
    }

    function acquire(dt){
      for (var i=reticles.length-1;i>=0;i--){
        var q=reticles[i]; q.t+=dt;
        var LIFE=3.6;
        if (q.t>LIFE){ reticles.splice(i,1); continue; }
        var lock=Math.min(1, q.t/0.42);                 /* reticle closes in */
        var open=Math.max(0, Math.min(1,(q.t-0.42)/0.34)); /* box snaps out */
        var fade=Math.min(1,(LIFE-q.t)/0.8);
        var ease=1-Math.pow(1-open,3);

        if (lock<1){
          var far=110*(1-lock), a=lock*0.9*fade;
          ctx.globalAlpha=a; ctx.strokeStyle='#BDF0FA'; ctx.lineWidth=1.6;
          ctx.beginPath();
          ctx.arc(q.cx,q.cy, 16+far, 0, 6.2832); ctx.stroke();
          ctx.globalAlpha=a*.55;
          ctx.beginPath();
          ctx.moveTo(q.cx-24-far,q.cy); ctx.lineTo(q.cx-6,q.cy);
          ctx.moveTo(q.cx+6,q.cy); ctx.lineTo(q.cx+24+far,q.cy);
          ctx.moveTo(q.cx,q.cy-24-far); ctx.lineTo(q.cx,q.cy-6);
          ctx.moveTo(q.cx,q.cy+6); ctx.lineTo(q.cx,q.cy+24+far);
          ctx.stroke();
        }
        if (open>0){
          var w=q.w*ease, h=q.h*ease, x=q.cx-w/2, y=q.cy-h/2;
          ctx.globalAlpha=.55*fade; ctx.strokeStyle='#5FCDE4'; ctx.lineWidth=1.5;
          ctx.strokeRect(x,y,w,h);
          ctx.globalAlpha=.11*fade; ctx.fillStyle='#5FCDE4'; ctx.fillRect(x,y,w,h);
          ctx.globalAlpha=.9*fade; ctx.strokeStyle='#BDF0FA'; ctx.lineWidth=2.2;
          var c=12;
          ctx.beginPath();
          ctx.moveTo(x,y+c); ctx.lineTo(x,y); ctx.lineTo(x+c,y);
          ctx.moveTo(x+w-c,y); ctx.lineTo(x+w,y); ctx.lineTo(x+w,y+c);
          ctx.moveTo(x,y+h-c); ctx.lineTo(x,y+h); ctx.lineTo(x+c,y+h);
          ctx.moveTo(x+w-c,y+h); ctx.lineTo(x+w,y+h); ctx.lineTo(x+w,y+h-c);
          ctx.stroke();
          if (open>0.6){
            var conf=(0.62+ease*0.34).toFixed(2);
            var txt=q.l.replace(/\s[0-9.]+$/,'') + ' ' + conf;
            ctx.font='10px ui-monospace, SFMono-Regular, monospace';
            var tw=ctx.measureText(txt).width+12;
            ctx.globalAlpha=.85*fade; ctx.fillStyle='#5FCDE4'; ctx.fillRect(x,y-15,tw,15);
            ctx.globalAlpha=fade; ctx.fillStyle='#04131C'; ctx.fillText(txt,x+6,y-4);
          }
        }
      }
      ctx.globalAlpha=1;
    }

    function pings(dt){
      for (var i=rings.length-1;i>=0;i--){
        var r=rings[i]; r.t+=dt;
        if (r.t>1.5){ rings.splice(i,1); continue; }
        var k=r.t/1.5, ease=1-Math.pow(1-k,3);
        var rad=ease*Math.max(W,H)*0.46, a=1-k;
        /* a soft shockwave band with a bright leading edge */
        if (rad>6){
          var g=ctx.createRadialGradient(r.x,r.y,Math.max(0,rad-30),r.x,r.y,rad+8);
          g.addColorStop(0,'rgba(95,205,228,0)');
          g.addColorStop(.72,'rgba(95,205,228,'+(a*.13).toFixed(3)+')');
          g.addColorStop(1,'rgba(189,240,250,0)');
          ctx.fillStyle=g; ctx.beginPath(); ctx.arc(r.x,r.y,rad+8,0,6.2832); ctx.fill();
          ctx.globalAlpha=a*.55; ctx.strokeStyle='#BDF0FA'; ctx.lineWidth=1.3;
          ctx.beginPath(); ctx.arc(r.x,r.y,rad,0,6.2832); ctx.stroke();
        }
        /* the impact point flashes and settles */
        var f=Math.max(0,1-r.t/0.34);
        ctx.globalAlpha=a*.65+f*.35; ctx.fillStyle='#BDF0FA';
        var d0=2+f*7; ctx.fillRect(r.x-d0/2, r.y-d0/2, d0, d0);
      }
      ctx.globalAlpha=1;
    }

    function scanline(y){
      var g=ctx.createLinearGradient(0,y-90,0,y+90);
      g.addColorStop(0,'rgba(95,205,228,0)');
      g.addColorStop(.5,'rgba(95,205,228,.16)');
      g.addColorStop(1,'rgba(95,205,228,0)');
      ctx.fillStyle=g; ctx.fillRect(0,y-90,W,180);
      ctx.globalAlpha=.9; ctx.fillStyle='#BDF0FA'; ctx.fillRect(0,y-1,W,2);
      ctx.globalAlpha=.35; ctx.fillStyle='#5FCDE4'; ctx.fillRect(0,y-4,W,8);
      ctx.globalAlpha=1;
    }

    function render(ms){
      var t=ms/1000, dt=Math.min(.05,(ms-last)/1000); last=ms;
      ripple(dt);

      var head=document.getElementById('localai'), eng=document.getElementById('engines');
      var hr=head?head.getBoundingClientRect():null, er=eng?eng.getBoundingClientRect():null;
      var inTop = hr ? hr.bottom > H*0.45 : false;
      var inEng = er ? (er.top < H*0.55 && er.bottom > H*0.35) : false;
      var scheme=document.body.dataset.scheme||'engines', want=0;
      if (scheme==='top') want = inTop?1:0;
      else if (scheme==='engines') want = inEng?1:0;
      else if (scheme==='both') want = (inTop||inEng)?1:0;
      else if (scheme==='detect') want = 1;
      if (want !== target){ target=want; if (want) seedBoxes(true); }
      /* the layer steps back once the reading starts, so copy always wins */
      cv.style.opacity = (hr && hr.bottom > H*0.25) ? '1' : (small ? '0.72' : '0.85');
      var d=target-blend;
      if (Math.abs(d)>0.0015) blend += d*Math.min(1, dt*2.6); else blend=target;

      ctx.clearRect(0,0,W,H);
      glow(t);

      if (blend<=0.002){ contours(t,1); }
      else if (blend>=0.998){ detections(dt,1); }
      else {
        var y=blend*H;
        ctx.save(); ctx.beginPath(); ctx.rect(0,0,W,y); ctx.clip();
        detections(dt, Math.min(1, blend*1.6)); ctx.restore();
        ctx.save(); ctx.beginPath(); ctx.rect(0,y,W,H-y); ctx.clip();
        contours(t, Math.min(1,(1-blend)*1.6)); ctx.restore();
        scanline(y);
      }
      acquire(dt);
      pings(dt);
    }

    function loop(now){
      if (!run) return;
      render(now-t0);
      if (!reduce) requestAnimationFrame(loop);
    }
  })();

  /* the install transcript prints itself the first time you reach it */
  (function(){
    function prep(pre){
      if (pre.dataset.split) return;
      pre.dataset.split = '1';
      pre.innerHTML = pre.innerHTML.split('\n')
        .map(function(l){ return '<span class="ln">' + (l || '&nbsp;') + '</span>'; }).join('');
      pre.insertAdjacentHTML('beforeend', '<span class="caret" aria-hidden="true"></span>');
    }
    function play(pre){
      prep(pre);
      var lines = pre.querySelectorAll('.ln');
      lines.forEach(function(l,i){
        l.classList.remove('on');
        setTimeout(function(){ l.classList.add('on'); }, reduce ? 0 : 90 + i*135);
      });
    }
    window.__playTerm = play;
    var term = document.querySelector('#p-script pre');
    if (!term) return;
    var io = new IntersectionObserver(function(es){
      es.forEach(function(e){ if (e.isIntersecting){ play(e.target); io.unobserve(e.target); } });
    }, {threshold:0.35});
    io.observe(term);
  })();

  var CMD = {'p-script':'curl -sSL https://localai.io/install.sh | sh',
             'p-docker':'docker run -p 8080:8080 --name local-ai -ti localai/localai:latest',
             'p-k8s':'kubectl apply -f https://localai.io/install/kubernetes.yaml'};
  var LBL = {'p-script':'bash','p-docker':'docker','p-k8s':'kubectl'};
  document.querySelectorAll('.tab').forEach(function(t){
    t.addEventListener('click', function(){
      document.querySelectorAll('.tab').forEach(function(o){ o.setAttribute('aria-selected', String(o===t)); });
      document.querySelectorAll('.pane').forEach(function(p){ p.hidden = (p.id !== t.dataset.pane); });
      var c = document.getElementById('cpy');
      c.dataset.copy = CMD[t.dataset.pane]; c.textContent = 'copy';
      document.getElementById('term-label').textContent = LBL[t.dataset.pane];
      var pre = document.querySelector('#' + t.dataset.pane + ' pre');
      if (pre && window.__playTerm) window.__playTerm(pre);
    });
  });

  document.body.dataset.scheme = 'engines';

  var pl=document.getElementById('player'), pv=document.getElementById('pv');
  if (pl && pv){
    pl.addEventListener('click', function(){
      if (pv.paused){ pv.play(); pl.classList.add('playing'); }
      else { pv.pause(); pl.classList.remove('playing'); }
    });
    pv.addEventListener('ended', function(){ pl.classList.remove('playing'); });
    pv.addEventListener('pause', function(){ pl.classList.remove('playing'); });
  }

  document.querySelectorAll('.cpy').forEach(function(b){
    b.addEventListener('click', function(){
      navigator.clipboard.writeText(b.dataset.copy).then(function(){
        b.textContent = 'copied'; setTimeout(function(){ b.textContent='copy'; }, 1600);
      });
    });
  });
})();
