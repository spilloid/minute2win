// The product page's boot sequence: firmware PXE, the armed chain, the engine's phases on a stopwatch, then the gold finish.
// One canvas, no libraries. A simulation of the real chain (strings follow server/server.go and engine/Start-Minute2Win.ps1),
// labelled as such on screen; the stopwatch runs at the one-minute target pace, not at a measured speed.
(() => {
  'use strict';
  const canvas = document.getElementById('boot');
  if (!canvas || !canvas.getContext) return;
  const ctx = canvas.getContext('2d');
  const stage = canvas.closest('.stage');
  const btnReplay = document.getElementById('boot-replay');
  const btnPause = document.getElementById('boot-pause');
  const reduce = window.matchMedia('(prefers-reduced-motion: reduce)').matches;

  const GOLD = ['#fff4c2', '#ffd76a', '#f5b82e', '#c8851a'];
  const EMBER = ['#ffd76a', '#ffb53a', '#ff8a1f', '#fff1b8'];
  const MONO = '"JetBrains Mono", ui-monospace, Consolas, monospace';
  const DISPLAY = '"Space Grotesk", "Segoe UI", system-ui, sans-serif';

  const MAC = '00:15:5d:00:2a:01';
  const SERVER = 'http://192.168.1.50:8080';
  // [start second, text, style]. Style: d dim, n normal, c cyan, g gold.
  const FIRMWARE = [
    [0.05, 'UEFI network boot  ·  SIMULATION', 'd'],
    [0.30, '>>Checking media presence......', 'n'],
    [0.55, '>>Media present......', 'n'],
    [0.75, '>>Start PXE over IPv4.', 'n'],
    [1.00, '  Station IP address is 192.168.1.73', 'n'],
    [1.25, '  proxyDHCP: minute2win answers ARMED ' + MAC, 'g'],
    [1.50, '  TFTP  ipxe.efi ................................ ok', 'c'],
    [1.75, 'iPXE initialising devices... ok', 'n'],
    [1.95, 'chain ' + SERVER + '/boot/' + MAC, 'c'],
    [2.20, 'minute2win: ' + MAC + ' is armed, fetching its one-time deployment script', 'g'],
    [2.45, 'minute2win: armed for lab-win11, deploying (this wipes the disk)', 'g'],
    [2.65, 'wimboot  BCD  boot.sdi  manifest.json  boot.wim', 'c'],
    [2.85, 'Starting WinPE...', 'n'],
  ];
  // Phases the engine reports, with the simulated clock (seconds) at which each lands. Target pace, not a measurement.
  const LAPS = [['READY', 3.1], ['PARTITIONED', 4.0], ['DOWNLOADED', 31.5], ['APPLIED', 52.2], ['DONE', 58.1]];
  const SHORT = ['READY', 'PART', 'DOWN', 'APPLY', 'DONE'];
  const STATUS = [
    [0.0, 'Checking manifest, image and disk...'],
    [1.4, 'WIPING Disk 0 and installing Windows. Starting in  3s, press any key to abort.'],
    [3.1, 'Partitioning...'],
    [4.0, 'Downloading and verifying the image...'],
    [31.5, 'Applying image...'],
    [52.2, 'bcdboot  ·  first-boot bootstrap  ·  reclaiming staging...'],
    [58.1, 'Deployment complete.'],
  ];

  const T_FW = 3.05;     // firmware ends
  const T_CRT = 3.45;    // CRT collapse ends, boot scene begins
  const T_BOOT = 8.5;    // boot scene ends, gold begins
  const BOOT_LEN = T_BOOT - T_CRT;
  const SIM_END = 58.1;  // simulated clock at the end of the boot scene
  // [start second, text, size relative to the main line]; stacked top to bottom. Phones get shorter, bigger lines.
  const SLAMS_WIDE = [
    [8.75, 'VROOM VROOM.', 0.62],
    [9.45, 'MACHINE BECOME NEW.', 1],
    [10.2, 'ARM IT. BOOT IT. GO GO GO.', 0.5],
  ];
  const SLAMS_NARROW = [
    [8.75, 'VROOM VROOM.', 0.6],
    [9.45, 'MACHINE', 1],
    [9.75, 'BECOME NEW.', 1],
    [10.2, 'ARM IT. BOOT IT.', 0.5],
    [10.45, 'GO GO GO.', 0.62],
  ];
  const slamList = () => (W < 600 ? SLAMS_NARROW : SLAMS_WIDE);

  let W = 0, H = 0, DPR = 1;
  let t0 = 0, now = 0, raf = 0, paused = false, visible = true, lastFrame = 0;
  let particles = [], streaks = [], embers = [], burstDone = false;

  function resize() {
    const r = canvas.getBoundingClientRect();
    DPR = Math.min(window.devicePixelRatio || 1, 2);
    W = Math.max(1, r.width); H = Math.max(1, r.height);
    canvas.width = Math.round(W * DPR); canvas.height = Math.round(H * DPR);
    ctx.setTransform(DPR, 0, 0, DPR, 0, 0);
    seedEmbers();
    if (reduce || paused) draw(reduce ? 14 : now);
  }

  const clamp = (v, a, b) => Math.min(b, Math.max(a, v));
  const ease = (x) => 1 - Math.pow(1 - clamp(x, 0, 1), 3);
  const easeBack = (x) => { x = clamp(x, 0, 1); const c = 2.2; return 1 + (c + 1) * Math.pow(x - 1, 3) + c * Math.pow(x - 1, 2); };
  const rand = (a, b) => a + Math.random() * (b - a);
  const pick = (arr) => arr[(Math.random() * arr.length) | 0];

  function seedEmbers() {
    embers = [];
    const n = Math.round(clamp(W * H / 9000, 30, 140));
    for (let i = 0; i < n; i++) embers.push({ x: rand(0, W), y: rand(0, H), r: rand(0.6, 2.2), v: rand(8, 34), w: rand(0, 6.28), c: pick(EMBER), a: rand(0.25, 0.8) });
  }

  function burst() {
    particles = [];
    const cx = W / 2, cy = H * 0.46;
    const n = W < 600 ? 220 : 420;
    for (let i = 0; i < n; i++) {
      const a = rand(0, Math.PI * 2), s = rand(80, W < 600 ? 520 : 900);
      particles.push({ x: cx, y: cy, vx: Math.cos(a) * s, vy: Math.sin(a) * s - rand(0, 120), r: rand(1, 3.6), life: rand(0.9, 2.4), age: 0, c: pick(GOLD.concat(EMBER)) });
    }
    streaks = [];
    for (let i = 0; i < 90; i++) streaks.push({ a: rand(0, Math.PI * 2), d: rand(20, 120), s: rand(900, 2200), len: rand(40, 220), w: rand(0.6, 2.2), c: pick(GOLD) });
  }

  // ---------- scenes ----------

  function firmware(t) {
    ctx.fillStyle = '#020305'; ctx.fillRect(0, 0, W, H);
    const fs = clamp(Math.min(W / 52, H / 22), 9.5, 15);
    ctx.font = `500 ${fs}px ${MONO}`; ctx.textBaseline = 'top';
    const lh = fs * 1.55, x = Math.max(16, W * 0.05);
    let y = Math.max(18, H * 0.08);
    for (const [start, text, style] of FIRMWARE) {
      if (t < start) break;
      const shown = text.slice(0, Math.floor((t - start) * 95));
      ctx.fillStyle = style === 'g' ? '#ffd76a' : style === 'c' ? '#7fe3ff' : style === 'd' ? '#5c6577' : '#c9d1dc';
      if (style === 'g') { ctx.shadowColor = 'rgba(255,200,80,.55)'; ctx.shadowBlur = 10; }
      fitText(shown, x, y, W - x - 12);
      ctx.shadowBlur = 0;
      y += lh;
    }
    if (Math.floor(t * 3) % 2 === 0) { ctx.fillStyle = '#c9d1dc'; ctx.fillRect(x, y + 2, fs * 0.6, fs * 1.05); }
    scanlines(0.10);
  }

  function fitText(s, x, y, max) {
    const w = ctx.measureText(s).width;
    if (w <= max) { ctx.fillText(s, x, y); return; }
    ctx.save(); ctx.translate(x, y); ctx.scale(max / w, 1); ctx.fillText(s, 0, 0); ctx.restore();
  }

  function scanlines(alpha) {
    ctx.fillStyle = `rgba(0,0,0,${alpha})`;
    for (let y = 0; y < H; y += 3) ctx.fillRect(0, y, W, 1);
  }

  function crt(t) {
    const k = ease((t - T_FW) / (T_CRT - T_FW));
    firmware(T_FW);
    ctx.fillStyle = `rgba(0,0,0,${k})`; ctx.fillRect(0, 0, W, H);
    const h = Math.max(1.5, H * (1 - k) * 0.5);
    const g = ctx.createLinearGradient(0, H / 2 - h, 0, H / 2 + h);
    g.addColorStop(0, 'rgba(255,255,255,0)'); g.addColorStop(0.5, `rgba(255,248,220,${1 - k * 0.6})`); g.addColorStop(1, 'rgba(255,255,255,0)');
    ctx.fillStyle = g; ctx.fillRect(W * k * 0.35, H / 2 - h, W * (1 - k * 0.7), h * 2);
  }

  function bootScene(t) {
    const p = clamp((t - T_CRT) / BOOT_LEN, 0, 1);
    // the clock eases in, runs, and settles on the target pace
    const sim = SIM_END * (p < 0.08 ? 0 : ease((p - 0.08) / 0.92) * 0.15 + ((p - 0.08) / 0.92) * 0.85);
    const bg = ctx.createRadialGradient(W / 2, H * 0.45, 0, W / 2, H * 0.45, Math.max(W, H) * 0.75);
    bg.addColorStop(0, '#0b1424'); bg.addColorStop(1, '#030407');
    ctx.fillStyle = bg; ctx.fillRect(0, 0, W, H);
    speedLines(t, 0.10 + p * 0.35);

    const wide = W >= 760;
    const cx = wide ? W * 0.40 : W / 2;
    const cy = H * (wide ? 0.46 : 0.36);
    const R = clamp(Math.min(W, H) * 0.16, 44, 110);
    const fade = ease((t - T_CRT) / 0.5);
    ctx.globalAlpha = fade;

    // stopwatch ring: one lap is the one-minute target
    ctx.lineWidth = Math.max(3, R * 0.07);
    ctx.strokeStyle = 'rgba(255,255,255,.08)';
    ctx.beginPath(); ctx.arc(cx, cy, R, 0, Math.PI * 2); ctx.stroke();
    const arc = ctx.createLinearGradient(cx - R, cy - R, cx + R, cy + R);
    arc.addColorStop(0, '#ffe9a3'); arc.addColorStop(1, '#f5a623');
    ctx.strokeStyle = arc; ctx.lineCap = 'round'; ctx.shadowColor = 'rgba(255,190,60,.6)'; ctx.shadowBlur = 16;
    ctx.beginPath(); ctx.arc(cx, cy, R, -Math.PI / 2, -Math.PI / 2 + Math.PI * 2 * (sim / 60)); ctx.stroke();
    ctx.shadowBlur = 0;
    for (const [, at] of LAPS) {
      const a = -Math.PI / 2 + Math.PI * 2 * (at / 60);
      ctx.fillStyle = sim >= at ? '#ffd76a' : 'rgba(255,255,255,.25)';
      ctx.beginPath(); ctx.arc(cx + Math.cos(a) * (R + ctx.lineWidth * 1.6), cy + Math.sin(a) * (R + ctx.lineWidth * 1.6), 2.6, 0, Math.PI * 2); ctx.fill();
    }

    // the boot spinner: five dots orbiting with a stagger, inside the ring
    const r2 = R * 0.52;
    for (let i = 0; i < 5; i++) {
      const local = ((t - T_CRT) * 0.62 - i * 0.075) % 1;
      const q = local < 0 ? local + 1 : local;
      // slow over the top, fast through the bottom, like the classic boot spinner
      const a = -Math.PI / 2 + Math.PI * 2 * (q - Math.sin(q * Math.PI * 2) / (Math.PI * 2) * 0.85);
      ctx.fillStyle = `rgba(255,255,255,${0.95 - i * 0.12})`;
      ctx.beginPath(); ctx.arc(cx + Math.cos(a) * r2, cy + Math.sin(a) * r2, Math.max(2.2, R * 0.045), 0, Math.PI * 2); ctx.fill();
    }

    // clock
    ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
    const cs = R * 0.36;
    ctx.font = `700 ${cs}px ${MONO}`;
    ctx.fillStyle = '#fff';
    ctx.fillText(fmt(sim), cx, cy + R * 1.42);
    ctx.font = `600 ${Math.max(9, cs * 0.34)}px ${MONO}`;
    ctx.fillStyle = 'rgba(255,215,106,.85)';
    ctx.fillText('TARGET PACE · 1:00', cx, cy + R * 1.42 + cs * 0.82);

    // wordmark
    ctx.font = `700 ${R * 0.42}px ${DISPLAY}`;
    const parts = ['minute', '2', 'win'];
    const widths = parts.map((s) => ctx.measureText(s).width);
    let x = cx - widths.reduce((a, b) => a + b, 0) / 2;
    const wy = cy - R * 1.55;
    ctx.textAlign = 'left';
    parts.forEach((s, i) => { ctx.fillStyle = i === 1 ? '#ffd76a' : '#e9ecf2'; ctx.fillText(s, x, wy); x += widths[i]; });

    // status line
    let status = STATUS[0][1];
    for (const [at, s] of STATUS) if (sim >= at) status = s;
    ctx.textAlign = 'center';
    ctx.font = `500 ${clamp(W / 70, 10, 14)}px ${MONO}`;
    ctx.fillStyle = 'rgba(201,209,220,.85)';
    fitCentered(status, cx, cy + R * 2.25, (wide ? W * 0.62 : W) - 32);

    // timing tower
    if (wide) tower(W * 0.72, cy - LAPS.length * 0.5 * 40, sim, fade);
    else pips(W / 2, Math.min(H - 26, cy + R * 2.75), sim);
    ctx.globalAlpha = 1;
    ctx.textAlign = 'left';
    scanlines(0.05);
  }

  function fitCentered(s, cx, y, max) {
    const w = ctx.measureText(s).width;
    if (w <= max) { ctx.fillText(s, cx, y); return; }
    ctx.save(); ctx.translate(cx, y); ctx.scale(max / w, 1); ctx.fillText(s, 0, 0); ctx.restore();
  }

  function fmt(s) {
    const m = Math.floor(s / 60), r = s - m * 60;
    return `${m}:${r < 10 ? '0' : ''}${r.toFixed(1)}`;
  }

  function tower(x, y, sim, alpha) {
    const w = Math.min(250, W * 0.24), rowH = 40;
    ctx.globalAlpha = alpha;
    ctx.fillStyle = 'rgba(255,255,255,.035)';
    roundRect(x - 14, y - 40, w + 28, rowH * LAPS.length + 54, 12); ctx.fill();
    ctx.font = `600 10.5px ${MONO}`; ctx.fillStyle = 'rgba(255,215,106,.8)'; ctx.textAlign = 'left'; ctx.textBaseline = 'middle';
    ctx.fillText('LAPS  ·  SIMULATED', x, y - 20);
    LAPS.forEach(([name, at], i) => {
      const yy = y + i * rowH + rowH / 2;
      const on = sim >= at;
      const flash = on ? clamp(1 - (sim - at) / 4, 0, 1) : 0;
      if (flash > 0) { ctx.fillStyle = `rgba(255,200,80,${0.22 * flash})`; roundRect(x - 8, yy - rowH / 2 + 3, w + 16, rowH - 6, 6); ctx.fill(); }
      ctx.fillStyle = on ? '#ffd76a' : 'rgba(255,255,255,.18)';
      ctx.beginPath(); ctx.arc(x + 5, yy, 4.5, 0, Math.PI * 2); ctx.fill();
      ctx.font = `600 13px ${MONO}`;
      ctx.fillStyle = on ? '#f3f5f8' : 'rgba(255,255,255,.32)';
      ctx.textAlign = 'left'; ctx.fillText(name, x + 20, yy);
      ctx.textAlign = 'right'; ctx.fillStyle = on ? '#ffd76a' : 'rgba(255,255,255,.22)';
      ctx.fillText(on ? at.toFixed(1) + 's' : '--.-', x + w, yy);
    });
    ctx.textAlign = 'left';
  }

  function pips(cx, y, sim) {
    const gap = Math.min(64, (W - 40) / LAPS.length);
    const x0 = cx - gap * (LAPS.length - 1) / 2;
    ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
    LAPS.forEach(([name, at], i) => {
      const on = sim >= at;
      ctx.fillStyle = on ? '#ffd76a' : 'rgba(255,255,255,.2)';
      ctx.beginPath(); ctx.arc(x0 + i * gap, y, 4, 0, Math.PI * 2); ctx.fill();
      ctx.font = `600 8.5px ${MONO}`;
      ctx.fillStyle = on ? 'rgba(255,240,200,.9)' : 'rgba(255,255,255,.3)';
      ctx.fillText(SHORT[i], x0 + i * gap, y + 13);
    });
  }

  function roundRect(x, y, w, h, r) {
    ctx.beginPath(); ctx.moveTo(x + r, y); ctx.arcTo(x + w, y, x + w, y + h, r); ctx.arcTo(x + w, y + h, x, y + h, r);
    ctx.arcTo(x, y + h, x, y, r); ctx.arcTo(x, y, x + w, y, r); ctx.closePath();
  }

  let lineSeed = null;
  function speedLines(t, strength) {
    if (!lineSeed) { lineSeed = []; for (let i = 0; i < 46; i++) lineSeed.push({ y: Math.random(), s: rand(0.4, 1.6), l: rand(0.05, 0.25), o: Math.random() }); }
    ctx.lineCap = 'round';
    for (const L of lineSeed) {
      const x = W * (1.2 - ((t * L.s * 0.9 + L.o) % 1.4));
      const g = ctx.createLinearGradient(x, 0, x + W * L.l, 0);
      g.addColorStop(0, `rgba(255,215,106,${strength * 0.9})`); g.addColorStop(1, 'rgba(255,215,106,0)');
      ctx.strokeStyle = g; ctx.lineWidth = 1;
      ctx.beginPath(); ctx.moveTo(x, L.y * H); ctx.lineTo(x + W * L.l, L.y * H); ctx.stroke();
    }
  }

  function goldScene(t, dt, still) {
    const k = t - T_BOOT;
    if (!burstDone && !still) { burst(); burstDone = true; }
    // the room turns gold, then settles to a dark gold night
    const settle = ease(k / 1.6);
    const bg = ctx.createRadialGradient(W / 2, H * 0.46, 0, W / 2, H * 0.46, Math.max(W, H) * 0.8);
    bg.addColorStop(0, mix('#ffcf5a', '#3a2606', settle));
    bg.addColorStop(0.45, mix('#a8650d', '#120c05', settle));
    bg.addColorStop(1, mix('#2a1702', '#040303', settle));
    ctx.fillStyle = bg; ctx.fillRect(0, 0, W, H);

    // shake
    const shake = still ? 0 : Math.max(0, 1 - k / 0.5) * 12 + slamShake(t);
    ctx.save();
    if (shake > 0) ctx.translate(rand(-shake, shake), rand(-shake, shake));

    // warp streaks
    const cx = W / 2, cy = H * 0.46;
    if (!still) for (const s of streaks) {
      // a hard burst outwards, then a slow endless warp
      const d = k < 1.2 ? s.d + s.s * k : s.d + ((s.s * 0.22 * (k - 1.2)) % (Math.max(W, H) * 0.7));
      const fadeS = k < 1.2 ? 1 : 0.35;
      const x1 = cx + Math.cos(s.a) * d, y1 = cy + Math.sin(s.a) * d;
      const x2 = cx + Math.cos(s.a) * (d + s.len), y2 = cy + Math.sin(s.a) * (d + s.len);
      const g = ctx.createLinearGradient(x1, y1, x2, y2);
      g.addColorStop(0, 'rgba(255,240,190,0)'); g.addColorStop(1, hexA(s.c, 0.85 * fadeS));
      ctx.strokeStyle = g; ctx.lineWidth = s.w; ctx.beginPath(); ctx.moveTo(x1, y1); ctx.lineTo(x2, y2); ctx.stroke();
    }

    // shockwaves
    if (!still) for (let i = 0; i < 3; i++) {
      const kk = k - i * 0.18;
      if (kk <= 0 || kk > 1.4) continue;
      const rr = ease(kk / 1.4) * Math.max(W, H) * 0.9;
      ctx.strokeStyle = `rgba(255,236,170,${(1 - kk / 1.4) * 0.8})`;
      ctx.lineWidth = 10 * (1 - kk / 1.4) + 1;
      ctx.beginPath(); ctx.arc(cx, cy, rr, 0, Math.PI * 2); ctx.stroke();
    }

    // particles
    if (!still) {
      ctx.globalCompositeOperation = 'lighter';
      for (const q of particles) {
        q.age += dt; if (q.age > q.life) continue;
        q.vx *= 0.985; q.vy = q.vy * 0.985 + 160 * dt; q.x += q.vx * dt; q.y += q.vy * dt;
        ctx.fillStyle = hexA(q.c, 1 - q.age / q.life);
        ctx.beginPath(); ctx.arc(q.x, q.y, q.r, 0, Math.PI * 2); ctx.fill();
      }
      ctx.globalCompositeOperation = 'source-over';
    }

    drawEmbers(dt, still);
    slams(t, still);
    ctx.restore();

    // flash
    if (!still && k < 0.6) { ctx.fillStyle = `rgba(255,250,225,${(1 - k / 0.6) * 0.95})`; ctx.fillRect(0, 0, W, H); }
    vignette();
  }

  function slamShake(t) {
    let s = 0;
    for (const [at] of slamList()) { const d = t - at - 0.16; if (d > 0 && d < 0.35) s += (1 - d / 0.35) * 7; }
    return s;
  }

  function drawEmbers(dt, still) {
    ctx.globalCompositeOperation = 'lighter';
    for (const e of embers) {
      if (!still) { e.y -= e.v * dt; e.w += dt * 1.3; if (e.y < -6) { e.y = H + 6; e.x = rand(0, W); } }
      const x = e.x + Math.sin(e.w) * 8;
      ctx.fillStyle = hexA(e.c, e.a * (0.6 + 0.4 * Math.sin(e.w * 2.1)));
      ctx.beginPath(); ctx.arc(x, e.y, e.r, 0, Math.PI * 2); ctx.fill();
    }
    ctx.globalCompositeOperation = 'source-over';
  }

  function slams(t, still) {
    const list = slamList();
    const narrow = W < 600;
    const base = narrow ? clamp(Math.min(W / 5.6, H / 7.5), 26, 80) : clamp(Math.min(W / 11.5, H / 5.2), 26, 112);
    const lineH = (f) => base * f * 1.14;
    const total = list.reduce((sum, [, , f]) => sum + lineH(f), 0);
    const midY = H * 0.46;
    let top = midY - total / 2;
    const tops = list.map(([, , f]) => { const y = top + lineH(f) / 2; top += lineH(f); return y; });
    const bottom = top;
    ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
    for (let i = 0; i < list.length; i++) {
      const [at, text, f] = list[i];
      const k = still ? 9 : t - at;
      if (k < 0) continue;
      const size = base * f;
      const y = tops[i];
      const s = still ? 1 : 1 + (1 - easeBack(k / 0.32)) * 1.4;
      const a = still ? 1 : clamp(k / 0.12, 0, 1);
      ctx.save();
      ctx.translate(W / 2, y); ctx.scale(s, s); ctx.globalAlpha = a;
      ctx.font = `700 ${size}px ${DISPLAY}`;
      const tw = ctx.measureText(text).width;
      const maxW = W * 0.92;
      if (tw > maxW) ctx.scale(maxW / tw, maxW / tw);
      // foil: a gradient whose bright band sweeps across every few seconds
      const sweep = still ? 0.35 : ((t * 0.32) % 1.6) - 0.3;
      const g = ctx.createLinearGradient(-tw / 2, -size / 2, tw / 2, size / 2);
      g.addColorStop(0, '#b8730f');
      g.addColorStop(clamp(sweep - 0.15, 0, 1), '#f2b630');
      g.addColorStop(clamp(sweep, 0, 1), '#fffbe6');
      g.addColorStop(clamp(sweep + 0.15, 0, 1), '#f7c548');
      g.addColorStop(1, '#a8650d');
      ctx.lineJoin = 'round';
      ctx.lineWidth = Math.max(3, size * 0.09);
      ctx.strokeStyle = 'rgba(30,16,2,.9)';
      ctx.shadowColor = 'rgba(255,190,60,.75)'; ctx.shadowBlur = size * 0.45;
      ctx.strokeText(text, 0, 0);
      ctx.shadowBlur = 0;
      ctx.fillStyle = g; ctx.fillText(text, 0, 0);
      ctx.restore();
    }
    // the honest footnote, always on once the slams start
    const k = still ? 9 : t - list[0][0];
    if (k > 0) {
      ctx.globalAlpha = clamp((k - 1.6) / 0.6, 0, 1);
      ctx.font = `500 ${clamp(W / 95, 9.5, 12.5)}px ${MONO}`;
      ctx.fillStyle = 'rgba(255,236,190,.78)';
      // under the slams, clear of the page copy that overlaps the bottom of the stage
      const fy = Math.min(bottom + base * 0.45, H - 96);
      if (narrow) {
        fitCentered('SIMULATION  ·  ONE MINUTE IS THE TARGET,', W / 2, fy, W - 32);
        fitCentered('NOT YET A MEASUREMENT  ·  BRING NVMe + 10 GbE', W / 2, fy + 17, W - 32);
      } else {
        fitCentered('SIMULATION  ·  ONE MINUTE IS THE TARGET, NOT YET A MEASUREMENT  ·  BRING NVMe + 10 GbE', W / 2, fy, W - 32);
      }
      ctx.globalAlpha = 1;
    }
  }

  function vignette() {
    const v = ctx.createRadialGradient(W / 2, H / 2, Math.min(W, H) * 0.35, W / 2, H / 2, Math.max(W, H) * 0.75);
    v.addColorStop(0, 'rgba(0,0,0,0)'); v.addColorStop(1, 'rgba(0,0,0,.55)');
    ctx.fillStyle = v; ctx.fillRect(0, 0, W, H);
  }

  function hexA(hex, a) {
    const n = parseInt(hex.slice(1), 16);
    return `rgba(${(n >> 16) & 255},${(n >> 8) & 255},${n & 255},${clamp(a, 0, 1)})`;
  }
  function mix(h1, h2, k) {
    const a = parseInt(h1.slice(1), 16), b = parseInt(h2.slice(1), 16);
    const c = (s) => Math.round(((a >> s) & 255) * (1 - k) + ((b >> s) & 255) * k);
    return `rgb(${c(16)},${c(8)},${c(0)})`;
  }

  // ---------- loop ----------

  function draw(t, dt = 0, still = false) {
    if (t < T_FW) firmware(t);
    else if (t < T_CRT) crt(t);
    else if (t < T_BOOT) bootScene(t);
    else goldScene(t, dt, still);
  }

  function frame(ms) {
    raf = 0;
    if (paused || !visible) return;
    const dt = lastFrame ? Math.min(0.05, (ms - lastFrame) / 1000) : 0.016;
    lastFrame = ms;
    now = (ms - t0) / 1000;
    draw(now, dt);
    if (stage && now > 11 && !stage.classList.contains('landed')) stage.classList.add('landed');
    raf = requestAnimationFrame(frame);
  }

  function start() {
    if (reduce) { resize(); draw(14, 0, true); stage && stage.classList.add('landed'); return; }
    burstDone = false; particles = []; streaks = [];
    stage && stage.classList.remove('landed');
    t0 = performance.now(); lastFrame = 0; paused = false; setPauseLabel();
    if (!raf) raf = requestAnimationFrame(frame);
  }

  function setPauseLabel() {
    if (!btnPause) return;
    btnPause.setAttribute('aria-pressed', String(paused));
    btnPause.textContent = paused ? 'Play' : 'Pause';
  }

  if (btnReplay) btnReplay.addEventListener('click', () => { if (reduce) return; start(); });
  if (btnPause) {
    if (reduce) btnPause.hidden = true;
    btnPause.addEventListener('click', () => {
      paused = !paused; setPauseLabel();
      if (!paused) { t0 = performance.now() - now * 1000; lastFrame = 0; if (!raf) raf = requestAnimationFrame(frame); }
    });
  }
  if (reduce && btnReplay) btnReplay.hidden = true;

  if ('IntersectionObserver' in window) {
    new IntersectionObserver((entries) => {
      const was = visible; visible = entries[0].isIntersecting;
      if (visible && !was && !paused && !reduce) { t0 = performance.now() - now * 1000; lastFrame = 0; if (!raf) raf = requestAnimationFrame(frame); }
    }).observe(canvas);
  }
  document.addEventListener('visibilitychange', () => {
    if (document.hidden) { visible = false; return; }
    visible = true;
    if (!paused && !reduce) { t0 = performance.now() - now * 1000; lastFrame = 0; if (!raf) raf = requestAnimationFrame(frame); }
  });
  window.addEventListener('resize', () => { resize(); });

  resize();
  // ?boot-t=9.6 renders one frozen frame at that second (for screenshots and debugging), fast-forwarding the gold particles.
  const freeze = parseFloat(new URLSearchParams(location.search).get('boot-t'));
  if (freeze >= 0 && !reduce) {
    const render = () => {
      resize(); paused = true; now = freeze; setPauseLabel();
      if (freeze >= T_BOOT) { burst(); burstDone = true; for (let s = T_BOOT; s < freeze; s += 1 / 60) draw(s, 1 / 60); }
      draw(freeze, 0);
      if (freeze > 11 && stage) stage.classList.add('landed');
    };
    if (document.fonts && document.fonts.ready) document.fonts.ready.then(render); else render();
    return;
  }
  const go = () => start();
  if (document.fonts && document.fonts.ready) Promise.race([document.fonts.ready, new Promise((r) => setTimeout(r, 900))]).then(go);
  else go();
})();
