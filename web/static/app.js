// Theme toggle: switch between dark and light mode.
function toggleTheme() {
  var h = document.documentElement;
  var next = h.dataset.theme === 'light' ? 'dark' : 'light';
  h.dataset.theme = next;
  localStorage.setItem('theme', next);
}

// ---------------------------------------------------------------------------
// Global backtest form helpers (used by onclick handlers in rendered HTML)
// ---------------------------------------------------------------------------

// Collect non-empty config override inputs and submit the backtest form.
function applyConfigParams() {
  var inputs = document.querySelectorAll('.cfg-input');
  var pairs = [];
  for (var i = 0; i < inputs.length; i++) {
    var k = inputs[i].dataset.paramKey, v = inputs[i].value.trim();
    if (v !== '') pairs.push(k + '=' + v);
  }
  document.getElementById('paramsInput').value = pairs.join(',');
  // Use requestSubmit to trigger the submit event interceptor (fetch instead of reload).
  var form = document.getElementById('backtestForm');
  if (form.requestSubmit) {
    form.requestSubmit();
  } else {
    form.submit();
  }
}

// Reset all config override inputs.
function resetConfigParams() {
  var inputs = document.querySelectorAll('.cfg-input');
  for (var i = 0; i < inputs.length; i++) { inputs[i].value = ''; }
  document.getElementById('paramsInput').value = '';
}

// Copy config YAML to clipboard.
function copyConfigYAML(btn) {
  var text = document.getElementById('configYAML').textContent;
  navigator.clipboard.writeText(text).then(function() {
    btn.textContent = 'Copied!';
    setTimeout(function() { btn.textContent = 'Copy'; }, 1500);
  });
}

// Prevent FOUC: show body after fonts and styles are ready.
(function() {
  function reveal() {
    requestAnimationFrame(function() {
      document.body.classList.add('ready');
    });
  }
  if (document.fonts && document.fonts.ready) {
    document.fonts.ready.then(reveal);
  } else {
    reveal();
  }
  // Fallback: ensure body is visible even if fonts stall.
  setTimeout(function() { document.body.classList.add('ready'); }, 800);
})();

// ---------------------------------------------------------------------------
// Color helpers (ported from Go template funcs)
// ---------------------------------------------------------------------------

function wrColor(wr) {
  if (wr >= 70) return 'var(--green)';
  if (wr >= 50) return 'var(--amber)';
  return 'var(--red)';
}
function invWrColor(wr) { return wrColor(100 - wr); }
function pnlColor(v) { return v >= 0 ? 'var(--green)' : 'var(--red)'; }
function sharpeColor(v) {
  if (v >= 2) return 'var(--green)';
  if (v >= 1) return 'var(--amber)';
  return 'var(--red)';
}
function profitFactorColor(v) {
  if (v >= 1.5) return 'var(--green)';
  if (v >= 1.0) return 'var(--amber)';
  return 'var(--red)';
}
function sideColor(side) { return side === 'Up' ? 'var(--green)' : 'var(--red)'; }

// ---------------------------------------------------------------------------
// Format helpers (ported from Go template funcs)
// ---------------------------------------------------------------------------

function fmtPnL(v) { return '$' + (v >= 0 ? '+' : '') + v.toFixed(2); }
function fmtPct(v) { return v.toFixed(2) + '%'; }
function fmtPrice(v) { return v.toFixed(2); }
function fmtPrice2(v) { return '$' + v.toFixed(2); }
function fmtFloat2(v) { return v.toFixed(2); }
function fmtFloat4(v) { return '$' + (v >= 0 ? '+' : '') + v.toFixed(2); }
function fmtChange(v) { return (v >= 0 ? '+' : '') + v.toFixed(2); }
function fmtBTC(v) { return '$' + v.toFixed(2); }
function fmtBarWidth(v) { return v.toFixed(0) + '%'; }

// Escape HTML entities for safe innerHTML.
function esc(s) {
  if (s === undefined || s === null) return '';
  return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

// ---------------------------------------------------------------------------
// Badge helpers
// ---------------------------------------------------------------------------

function sideBadge(side) {
  if (side === 'Up') return '<span class="badge bw">UP</span>';
  return '<span class="badge bl">DOWN</span>';
}
function liveBadge() { return ' <span class="badge blv">LIVE</span>'; }
function winBadge() { return '<span class="badge bw">WIN</span>'; }
function lossBadge() { return '<span class="badge bl">LOSS</span>'; }
function pendingBadge() { return '<span class="badge bp">PENDING</span>'; }
function exitBadge() { return '<span class="badge be">EXIT</span>'; }

// ---------------------------------------------------------------------------
// Component renderers
// ---------------------------------------------------------------------------

function renderHeader(data, mode) {
  var lp = data.livePaper;
  var meta = data.meta;
  var s = data.summary;
  var h = '';
  h += '<header class="sticky top-0 z-50 bg-base border-b border-slate-800/60" style="padding-top:env(safe-area-inset-top)">';
  h += '<div class="hdr-wrap">';
  // Brand
  h += '<div class="flex items-center gap-1.5 flex-shrink-0">';
  h += '<span class="inline-block w-2 h-2 rounded-full bg-cyan-400 flex-shrink-0" style="animation:pulse-dot 2s ease-in-out infinite"></span>';
  h += '<span class="text-xs sm:text-sm font-semibold text-slate-300 tracking-wide">PMBOT</span>';
  if (meta.dryRun) {
    h += '<span class="badge" style="font-size:9px;background:rgba(234,179,8,0.15);color:#eab308;border:1px solid rgba(234,179,8,0.3)">DRY RUN</span>';
  }
  h += '</div>';
  // Stats
  h += '<div class="hdr-stats">';
  if (lp.hasLive) {
    var borderCls = lp.hasPaper ? ' border-r border-slate-700 pr-3 sm:pr-4' : '';
    h += '<div class="flex items-center gap-2 sm:gap-3' + borderCls + '">';
    h += '<span class="badge blv" style="font-size:9px">LIVE</span>';
    if (lp.live.hasResolved) {
      h += '<span class="m text-sm sm:text-lg font-bold" style="color:' + pnlColor(lp.live.totalPnL) + '">' + fmtPnL(lp.live.totalPnL) + '</span>';
      h += '<div class="hidden sm:flex items-baseline gap-1"><span class="m text-sm font-bold" style="color:' + wrColor(lp.live.winRate) + '">' + fmtPct(lp.live.winRate) + '</span><span class="text-[10px] uppercase tracking-wider text-slate-500">Win</span></div>';
      h += '<div class="hidden sm:flex items-baseline gap-1"><span class="m text-sm font-semibold text-slate-200">' + lp.live.trades + '</span><span class="text-[10px] uppercase tracking-wider text-slate-500">Trades</span></div>';
    } else {
      h += '<span class="text-xs text-slate-500">Waiting</span>';
    }
    h += '</div>';
  }
  if (lp.hasPaper) {
    h += '<div class="flex items-center gap-2 sm:gap-3">';
    h += '<span class="badge bp" style="font-size:9px">PAPER</span>';
    if (lp.paper.hasResolved) {
      h += '<span class="m text-sm sm:text-lg font-bold" style="color:' + pnlColor(lp.paper.totalPnL) + '">' + fmtPnL(lp.paper.totalPnL) + '</span>';
      h += '<div class="hidden sm:flex items-baseline gap-1"><span class="m text-sm font-bold" style="color:' + wrColor(lp.paper.winRate) + '">' + fmtPct(lp.paper.winRate) + '</span><span class="text-[10px] uppercase tracking-wider text-slate-500">Win</span></div>';
      h += '<div class="hidden sm:flex items-baseline gap-1"><span class="m text-sm font-semibold text-slate-200">' + lp.paper.trades + '</span><span class="text-[10px] uppercase tracking-wider text-slate-500">Trades</span></div>';
    } else {
      h += '<span class="text-xs text-slate-500">Waiting</span>';
    }
    h += '</div>';
  }
  if (!lp.hasLive && !lp.hasPaper) {
    if (s.hasResolved) {
      h += '<div class="flex items-baseline gap-1"><span class="m text-sm sm:text-lg font-bold" style="color:' + pnlColor(s.totalPnL) + '">' + fmtPnL(s.totalPnL) + '</span></div>';
      h += '<div class="flex items-baseline gap-1"><span class="m text-sm font-bold" style="color:' + wrColor(s.winRate) + '">' + fmtPct(s.winRate) + '</span><span class="hidden sm:inline text-[10px] uppercase tracking-wider text-slate-500">Win</span></div>';
    } else {
      h += '<span class="text-xs text-slate-500">Waiting</span>';
    }
    h += '<div class="hidden sm:flex items-baseline gap-1.5"><span class="m text-sm font-semibold text-slate-200">' + s.tradeCount + '</span><span class="text-[10px] uppercase tracking-wider text-slate-500">Trades</span></div>';
  }
  h += '<div class="hidden sm:flex items-baseline gap-1.5"><span class="m text-sm font-semibold text-slate-200" data-live="duration">' + esc(s.duration) + '</span><span class="text-[10px] uppercase tracking-wider text-slate-500">Uptime</span></div>';
  h += '</div>';
  // Nav + Toggle
  h += '<div class="flex items-center gap-2 sm:gap-3 flex-shrink-0 ml-auto">';
  var isBacktestPage = window.location.pathname === '/backtest';
  var navActive = 'text-slate-200 font-medium';
  var navInactive = 'text-slate-500 hover:text-slate-300 transition-colors';
  h += '<nav class="flex items-center gap-2 sm:gap-3 text-xs sm:text-sm sm:border-l sm:border-slate-700 sm:pl-4">';
  h += '<a href="/paper" class="' + (isBacktestPage ? navInactive : navActive) + '">Paper</a>';
  h += '<a href="/backtest" class="' + (isBacktestPage ? navActive : navInactive) + '">Backtest</a>';
  h += '<a href="/data" class="' + navInactive + '">Data</a>';
  h += '</nav>';
  h += '<button class="theme-toggle flex-shrink-0" onclick="toggleTheme()" title="Toggle theme">';
  h += '<svg class="theme-icon-sun" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="5"/><path d="M12 1v2M12 21v2M4.22 4.22l1.42 1.42M18.36 18.36l1.42 1.42M1 12h2M21 12h2M4.22 19.78l1.42-1.42M18.36 5.64l1.42-1.42"/></svg>';
  h += '<svg class="theme-icon-moon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/></svg>';
  h += '</button>';
  h += '</div>';
  h += '</div>';
  h += '</header>';
  return h;
}

function renderSummaryCards(s) {
  var h = '<div class="cards stagger">';
  h += '<div class="card"><div class="v" data-live="duration">' + esc(s.duration) + '</div><div class="l">Duration</div></div>';
  h += '<div class="card"><div class="v">' + s.tradeCount + '</div><div class="l">Trades</div></div>';
  h += '<div class="card"><div class="v g">' + s.wins + '</div><div class="l">Wins</div></div>';
  h += '<div class="card"><div class="v r">' + s.losses + '</div><div class="l">Losses</div></div>';
  if (s.pending > 0) {
    h += '<div class="card"><div class="v y">' + s.pending + '</div><div class="l">Pending</div></div>';
  }
  if (s.hasResolved) {
    h += '<div class="card"><div class="v" style="color:' + wrColor(s.winRate) + '">' + fmtPct(s.winRate) + '</div><div class="l">Win Rate</div></div>';
    h += '<div class="card card-pnl" style="border-left-color:' + pnlColor(s.totalPnL) + '"><div class="v" style="color:' + pnlColor(s.totalPnL) + '">' + fmtPnL(s.totalPnL) + '</div><div class="l">Total P&amp;L</div></div>';
    h += '<div class="card"><div class="v" style="color:' + pnlColor(s.avgPnL) + '">' + fmtPnL(s.avgPnL) + '</div><div class="l">Avg P&amp;L</div></div>';
  }
  h += '</div>';
  return h;
}

function renderRiskCards(m) {
  var h = '<div class="mb-5"><h2 class="sec-title">Risk Metrics</h2><div class="cards stagger">';
  h += '<div class="card"><div class="v" style="color:' + sharpeColor(m.sharpeRatio) + '">' + fmtFloat2(m.sharpeRatio) + '</div><div class="l">Sharpe Ratio</div></div>';
  h += '<div class="card"><div class="v r">' + fmtPrice2(m.maxDrawdown) + '</div><div class="l">Max Drawdown</div></div>';
  if (m.maxDrawdownPct > 0) {
    h += '<div class="card"><div class="v r">' + fmtPct(m.maxDrawdownPct) + '</div><div class="l">Max DD %</div></div>';
  }
  h += '<div class="card"><div class="v" style="color:' + profitFactorColor(m.profitFactor) + '">' + fmtFloat2(m.profitFactor) + '</div><div class="l">Profit Factor</div></div>';
  h += '<div class="card"><div class="v" style="color:' + pnlColor(m.expectancy) + '">' + fmtPnL(m.expectancy) + '</div><div class="l">Expectancy</div></div>';
  h += '<div class="card"><div class="v" style="color:' + profitFactorColor(m.winLossRatio) + '">' + fmtFloat2(m.winLossRatio) + '</div><div class="l">Win/Loss Ratio</div></div>';
  h += '<div class="card"><div class="v g">' + fmtPrice2(m.avgWin) + '</div><div class="l">Avg Win</div></div>';
  h += '<div class="card"><div class="v r">' + fmtPrice2(m.avgLoss) + '</div><div class="l">Avg Loss</div></div>';
  h += '<div class="card"><div class="v r">' + m.maxConsecLoss + '</div><div class="l">Max Consec Loss</div></div>';
  h += '<div class="card"><div class="v g">' + m.maxConsecWins + '</div><div class="l">Max Consec Win</div></div>';
  if (m.recoveryFactor !== 0) {
    h += '<div class="card"><div class="v" style="color:' + profitFactorColor(m.recoveryFactor) + '">' + fmtFloat2(m.recoveryFactor) + '</div><div class="l">Recovery Factor</div></div>';
  }
  h += '</div></div>';
  return h;
}

function renderHoldReasons(holdReasons, totalHolds, evalCount) {
  if (!holdReasons || holdReasons.length === 0) return '';
  var h = '<div class="mb-7"><details data-detail-id="hold-reasons"><summary class="text-sm text-slate-400 pb-1.5 border-b border-slate-700 list-none flex items-center gap-2">';
  h += '<svg class="w-3 h-3 transition-transform duration-200 details-arrow" viewBox="0 0 12 12" fill="none" stroke="currentColor" stroke-width="2"><path d="M4 2l4 4-4 4"/></svg>';
  h += 'Hold Reasons <span class="text-slate-500">&mdash; ' + holdReasons.length + ' types, ' + totalHolds + ' total (' + evalCount + ' evals)</span></summary>';
  h += '<table class="mt-2.5"><tr><th>Reason</th><th>Count</th><th>%</th><th></th></tr>';
  for (var i = 0; i < holdReasons.length; i++) {
    var r = holdReasons[i];
    h += '<tr><td>' + esc(r.reason) + '</td><td class="m">' + r.count + '</td><td class="m">' + fmtPct(r.pct) + '</td>';
    h += '<td><div class="bar"><span style="width:' + fmtBarWidth(r.pct) + ';background:var(--text-muted)"></span></div></td></tr>';
  }
  h += '</table></details></div>';
  return h;
}

function renderBucketTable(title, rows) {
  if (!rows || rows.length === 0) return '';
  var h = '<div class="mb-7 overflow-x-auto"><h2 class="sec-title">' + esc(title) + '</h2>';
  // Desktop table
  h += '<div class="hidden md:block">';
  h += '<table><tr><th>Range</th><th>Trades</th><th>Wins</th><th>Losses</th><th>Win Rate</th><th>P&amp;L</th><th class="hide-mob"></th></tr>';
  for (var i = 0; i < rows.length; i++) {
    var r = rows[i];
    h += '<tr><td class="m">' + esc(r.label) + '</td><td>' + r.trades + '</td><td class="g">' + r.wins + '</td><td class="r">' + r.losses + '</td>';
    h += '<td style="color:' + wrColor(r.winRate) + '">' + fmtPct(r.winRate) + '</td><td class="m" style="color:' + pnlColor(r.totalPnL) + '">' + fmtPnL(r.totalPnL) + '</td>';
    h += '<td class="hide-mob"><div class="bar"><span style="width:' + fmtBarWidth(r.winRate) + ';background:' + wrColor(r.winRate) + '"></span></div></td></tr>';
  }
  h += '</table></div>';
  // Mobile cards
  h += '<div class="md:hidden space-y-2">';
  for (var i = 0; i < rows.length; i++) {
    var r = rows[i];
    h += '<div class="mob-card">';
    h += '<div class="flex justify-between items-center mb-1.5"><span class="m text-sm">' + esc(r.label) + '</span><span style="color:' + wrColor(r.winRate) + '" class="m font-bold text-sm">' + fmtPct(r.winRate) + '</span></div>';
    h += '<div class="flex justify-between items-center text-xs mb-2">';
    h += '<span><span class="g">' + r.wins + '</span>W / <span class="r">' + r.losses + '</span>L</span>';
    h += '<span class="m" style="color:' + pnlColor(r.totalPnL) + '">' + fmtPnL(r.totalPnL) + '</span>';
    h += '</div>';
    h += '<div class="w-full h-1.5 rounded-full overflow-hidden" style="background:var(--bar-track)"><span class="block h-full rounded-full transition-all duration-300" style="width:' + fmtBarWidth(r.winRate) + ';background:' + wrColor(r.winRate) + '"></span></div>';
    h += '</div>';
  }
  h += '</div></div>';
  return h;
}

function renderProfitability(sim) {
  if (!sim || !sim.rows || sim.rows.length === 0) return '';
  var h = '<div class="mb-7"><h2 class="sec-title">Profitability by Buy Price</h2>';
  h += '<p class="text-slate-500 text-[13px] mb-3">Based on ' + sim.rows.length + ' price points (win rate ' + fmtPct(sim.observedWR) + '). Breakeven price = <span class="text-amber-500 font-bold">' + fmtPrice2(sim.breakevenPrice) + '</span>. Each row simulates $1 cost per trade at that price.</p>';
  // Desktop table
  h += '<div class="hidden md:block overflow-x-auto">';
  h += '<table><tr><th>Buy Price</th><th>Win Profit</th><th>Loss Cost</th><th>Need WR</th><th>Total P&amp;L</th><th>Per Trade</th><th class="hide-mob"></th></tr>';
  for (var i = 0; i < sim.rows.length; i++) {
    var r = sim.rows[i];
    var bgStyle = r.isProfitable ? ' style="background:var(--profit-row-bg)"' : '';
    h += '<tr' + bgStyle + '><td class="m">' + fmtPrice2(r.buyPrice) + (r.isBreakeven ? ' &#9664; BE' : '') + '</td><td class="m g">' + fmtFloat4(r.winProfit) + '</td><td class="m r">-$1.00</td>';
    h += '<td class="m" style="color:' + invWrColor(r.needWR) + '">' + fmtPct(r.needWR) + '</td>';
    h += '<td class="m" style="color:' + pnlColor(r.totalPnL) + '">' + fmtPnL(r.totalPnL) + '</td>';
    h += '<td class="m" style="color:' + pnlColor(r.perTrade) + '">' + fmtFloat4(r.perTrade) + '</td>';
    h += '<td class="hide-mob"><div class="bar"><span style="width:' + fmtBarWidth(r.barWidth) + ';background:' + pnlColor(r.totalPnL) + '"></span></div></td></tr>';
  }
  h += '</table></div>';
  // Mobile cards
  h += '<div class="md:hidden space-y-2">';
  for (var i = 0; i < sim.rows.length; i++) {
    var r = sim.rows[i];
    var mobBg = r.isProfitable ? ' style="background:var(--profit-row-bg)"' : '';
    h += '<div class="mob-card"' + mobBg + '>';
    h += '<div class="flex justify-between items-center mb-1.5"><span class="m font-bold text-sm">' + fmtPrice2(r.buyPrice) + (r.isBreakeven ? ' <span class="text-amber-500">&#9664; BE</span>' : '') + '</span><span class="m font-bold" style="color:' + pnlColor(r.totalPnL) + '">' + fmtPnL(r.totalPnL) + '</span></div>';
    h += '<div class="flex flex-wrap gap-x-3 gap-y-1 text-xs">';
    h += '<span>Win <span class="g m">' + fmtFloat4(r.winProfit) + '</span></span>';
    h += '<span>Need WR <span class="m" style="color:' + invWrColor(r.needWR) + '">' + fmtPct(r.needWR) + '</span></span>';
    h += '<span>Per Trade <span class="m" style="color:' + pnlColor(r.perTrade) + '">' + fmtFloat4(r.perTrade) + '</span></span>';
    h += '</div></div>';
  }
  h += '</div></div>';
  return h;
}

// ---------------------------------------------------------------------------
// SVG equity curve renderers (ported from Go)
// ---------------------------------------------------------------------------

function renderEquitySVG(equity) {
  var ep = equity.points;
  var pp = equity.peaks;
  if (!ep || ep.length < 2) return '';
  var n = ep.length;

  var chartW = 800, chartH = 240;
  var padL = 60, padR = 20, padT = 10, padB = 30;
  var plotW = chartW - padL - padR;
  var plotH = chartH - padT - padB;

  // Y range
  var minY = 0, maxY = 0;
  var i;
  for (i = 0; i < n; i++) {
    if (ep[i] < minY) minY = ep[i];
    if (ep[i] > maxY) maxY = ep[i];
  }
  if (pp) {
    for (i = 0; i < pp.length; i++) {
      if (pp[i] > maxY) maxY = pp[i];
    }
  }
  var spread = maxY - minY;
  if (spread === 0) spread = 1;
  minY -= spread * 0.1;
  maxY += spread * 0.1;
  var yRange = maxY - minY;

  function toX(idx) { return padL + idx / (n - 1) * plotW; }
  function toY(v) { return padT + (1 - (v - minY) / yRange) * plotH; }

  var h = '<div class="mb-7"><h2 class="sec-title">Equity Curve &amp; Drawdown</h2>';
  h += '<svg viewBox="0 0 ' + chartW + ' ' + chartH + '" class="w-full h-auto rounded-lg border border-slate-800/50" style="max-width:' + chartW + 'px;background:var(--bg-surface)">';

  // Gradient definition
  var lineVar = ep[n - 1] < 0 ? 'var(--red)' : 'var(--green)';
  h += '<defs><linearGradient id="eqGrad" x1="0" y1="0" x2="0" y2="1">';
  h += '<stop offset="0%" style="stop-color:' + lineVar + ';stop-opacity:0.3"/>';
  h += '<stop offset="100%" style="stop-color:' + lineVar + ';stop-opacity:0.02"/>';
  h += '</linearGradient></defs>';

  // Grid lines
  var gridSteps = 4;
  for (i = 1; i < gridSteps; i++) {
    var gridY = minY + yRange * i / gridSteps;
    var py = toY(gridY);
    h += '<line x1="' + padL + '" y1="' + py.toFixed(1) + '" x2="' + (chartW - padR) + '" y2="' + py.toFixed(1) + '" style="stroke:var(--chart-grid)" stroke-width="0.5"/>';
    h += '<text x="' + (padL - 6) + '" y="' + py.toFixed(1) + '" style="fill:var(--text-dim)" font-size="9" text-anchor="end" dominant-baseline="middle">$' + (gridY >= 0 ? '+' : '') + gridY.toFixed(0) + '</text>';
  }

  // Zero line
  var zeroY = toY(0);
  h += '<line x1="' + padL + '" y1="' + zeroY.toFixed(1) + '" x2="' + (chartW - padR) + '" y2="' + zeroY.toFixed(1) + '" style="stroke:var(--chart-grid-zero)" stroke-width="1" stroke-dasharray="4,4"/>';
  h += '<text x="' + (padL - 6) + '" y="' + zeroY.toFixed(1) + '" style="fill:var(--text-dim)" font-size="10" text-anchor="end" dominant-baseline="middle">$0</text>';

  // Drawdown fill (between peak and equity)
  if (pp && pp.length === n) {
    var dd = 'M' + toX(0).toFixed(1) + ',' + toY(pp[0]).toFixed(1);
    for (i = 1; i < n; i++) { dd += ' L' + toX(i).toFixed(1) + ',' + toY(pp[i]).toFixed(1); }
    for (i = n - 1; i >= 0; i--) { dd += ' L' + toX(i).toFixed(1) + ',' + toY(ep[i]).toFixed(1); }
    dd += ' Z';
    h += '<path d="' + dd + '" style="fill:var(--red);fill-opacity:0.1"/>';
  }

  // Area fill under equity line
  var area = 'M' + toX(0).toFixed(1) + ',' + toY(ep[0]).toFixed(1);
  for (i = 1; i < n; i++) { area += ' L' + toX(i).toFixed(1) + ',' + toY(ep[i]).toFixed(1); }
  area += ' L' + toX(n - 1).toFixed(1) + ',' + toY(minY).toFixed(1) + ' L' + toX(0).toFixed(1) + ',' + toY(minY).toFixed(1) + ' Z';
  h += '<path d="' + area + '" fill="url(#eqGrad)"/>';

  // Peak line (dashed)
  if (pp && pp.length === n) {
    var peak = 'M' + toX(0).toFixed(1) + ',' + toY(pp[0]).toFixed(1);
    for (i = 1; i < n; i++) { peak += ' L' + toX(i).toFixed(1) + ',' + toY(pp[i]).toFixed(1); }
    h += '<path d="' + peak + '" fill="none" style="stroke:var(--text-dim)" stroke-width="1" stroke-dasharray="3,3"/>';
  }

  // Equity line
  var eq = 'M' + toX(0).toFixed(1) + ',' + toY(ep[0]).toFixed(1);
  for (i = 1; i < n; i++) { eq += ' L' + toX(i).toFixed(1) + ',' + toY(ep[i]).toFixed(1); }
  h += '<path d="' + eq + '" fill="none" style="stroke:' + lineVar + '" stroke-width="2"/>';

  // Trade dots
  for (i = 0; i < n; i++) {
    var c = 'var(--green)';
    if (i > 0 && ep[i] < ep[i - 1]) c = 'var(--red)';
    h += '<circle cx="' + toX(i).toFixed(1) + '" cy="' + toY(ep[i]).toFixed(1) + '" r="3" style="fill:' + c + '" opacity="0.8"><title>#' + (i + 1) + ' $' + (ep[i] >= 0 ? '+' : '') + ep[i].toFixed(2) + '</title></circle>';
  }

  // X axis labels
  h += '<text x="' + toX(0).toFixed(1) + '" y="' + (chartH - 5) + '" style="fill:var(--text-dim)" font-size="10" text-anchor="middle">#1</text>';
  h += '<text x="' + toX(n - 1).toFixed(1) + '" y="' + (chartH - 5) + '" style="fill:var(--text-dim)" font-size="10" text-anchor="middle">#' + n + '</text>';

  // Legend
  h += '<text x="' + (padL + 4) + '" y="' + (padT + 12) + '" style="fill:' + lineVar + '" font-size="10">\u2014 equity</text>';
  h += '<text x="' + (padL + 70) + '" y="' + (padT + 12) + '" style="fill:var(--text-dim)" font-size="10">--- peak</text>';
  h += '<rect x="' + (padL + 120) + '" y="' + (padT + 3) + '" width="10" height="10" style="fill:var(--red);fill-opacity:0.12"/>';
  h += '<text x="' + (padL + 134) + '" y="' + (padT + 12) + '" style="fill:var(--text-dim)" font-size="10">drawdown</text>';

  h += '</svg></div>';
  return h;
}

function renderCompactEquitySVG(equity) {
  var ep = equity.points;
  var pp = equity.peaks;
  if (!ep || ep.length < 2) return '';
  var n = ep.length;

  var chartW = 700, chartH = 160;
  var padL = 50, padR = 15, padT = 8, padB = 24;
  var plotW = chartW - padL - padR;
  var plotH = chartH - padT - padB;

  var minY = 0, maxY = 0;
  var i;
  for (i = 0; i < n; i++) {
    if (ep[i] < minY) minY = ep[i];
    if (ep[i] > maxY) maxY = ep[i];
  }
  if (pp) {
    for (i = 0; i < pp.length; i++) {
      if (pp[i] > maxY) maxY = pp[i];
    }
  }
  var spread = maxY - minY;
  if (spread === 0) spread = 1;
  minY -= spread * 0.1;
  maxY += spread * 0.1;
  var yRange = maxY - minY;

  function toX(idx) { return padL + idx / (n - 1) * plotW; }
  function toY(v) { return padT + (1 - (v - minY) / yRange) * plotH; }

  var lineVar = ep[n - 1] < 0 ? 'var(--red)' : 'var(--green)';
  var h = '<svg viewBox="0 0 ' + chartW + ' ' + chartH + '" class="w-full h-auto rounded-lg border border-slate-800/50 mt-2" style="max-width:' + chartW + 'px;background:var(--bg-surface)">';
  h += '<defs><linearGradient id="eqGrad2" x1="0" y1="0" x2="0" y2="1">';
  h += '<stop offset="0%" style="stop-color:' + lineVar + ';stop-opacity:0.25"/>';
  h += '<stop offset="100%" style="stop-color:' + lineVar + ';stop-opacity:0.02"/>';
  h += '</linearGradient></defs>';

  // Grid lines (3 steps, draw 2 lines)
  for (i = 1; i < 3; i++) {
    var gridY = minY + yRange * i / 3.0;
    var py = toY(gridY);
    h += '<line x1="' + padL + '" y1="' + py.toFixed(1) + '" x2="' + (chartW - padR) + '" y2="' + py.toFixed(1) + '" style="stroke:var(--chart-grid)" stroke-width="0.5"/>';
  }

  // Zero line
  var zeroY = toY(0);
  h += '<line x1="' + padL + '" y1="' + zeroY.toFixed(1) + '" x2="' + (chartW - padR) + '" y2="' + zeroY.toFixed(1) + '" style="stroke:var(--chart-grid-zero)" stroke-width="1" stroke-dasharray="3,3"/>';
  h += '<text x="' + (padL - 4) + '" y="' + zeroY.toFixed(1) + '" style="fill:var(--text-dim)" font-size="9" text-anchor="end" dominant-baseline="middle">$0</text>';

  // Drawdown fill
  if (pp && pp.length === n) {
    var dd = 'M' + toX(0).toFixed(1) + ',' + toY(pp[0]).toFixed(1);
    for (i = 1; i < n; i++) { dd += ' L' + toX(i).toFixed(1) + ',' + toY(pp[i]).toFixed(1); }
    for (i = n - 1; i >= 0; i--) { dd += ' L' + toX(i).toFixed(1) + ',' + toY(ep[i]).toFixed(1); }
    dd += ' Z';
    h += '<path d="' + dd + '" style="fill:var(--red);fill-opacity:0.10"/>';
  }

  // Area fill
  var area = 'M' + toX(0).toFixed(1) + ',' + toY(ep[0]).toFixed(1);
  for (i = 1; i < n; i++) { area += ' L' + toX(i).toFixed(1) + ',' + toY(ep[i]).toFixed(1); }
  area += ' L' + toX(n - 1).toFixed(1) + ',' + toY(minY).toFixed(1) + ' L' + toX(0).toFixed(1) + ',' + toY(minY).toFixed(1) + ' Z';
  h += '<path d="' + area + '" fill="url(#eqGrad2)"/>';

  // Equity line
  var eq = 'M' + toX(0).toFixed(1) + ',' + toY(ep[0]).toFixed(1);
  for (i = 1; i < n; i++) { eq += ' L' + toX(i).toFixed(1) + ',' + toY(ep[i]).toFixed(1); }
  h += '<path d="' + eq + '" fill="none" style="stroke:' + lineVar + '" stroke-width="1.5"/>';

  // End label
  h += '<text x="' + (toX(n - 1) + 4).toFixed(1) + '" y="' + toY(ep[n - 1]).toFixed(1) + '" style="fill:' + lineVar + '" font-size="9" text-anchor="start" dominant-baseline="middle">$' + (ep[n - 1] >= 0 ? '+' : '') + ep[n - 1].toFixed(2) + '</text>';

  h += '</svg>';
  return h;
}

// ---------------------------------------------------------------------------
// Trade history renderer
// ---------------------------------------------------------------------------

function renderTradeRows(trades, showSlotLabel) {
  // Shared renderer for trade rows (desktop table + mobile cards).
  // showSlotLabel: if true, add Slot column (used in history trade details).
  if (!trades || trades.length === 0) return '';

  // Desktop table
  var h = '<div class="hidden md:block overflow-x-auto"><table><tr><th>#</th><th>Time</th>';
  if (showSlotLabel) h += '<th class="hide-mob">Slot</th>';
  h += '<th>Side</th><th>Price</th><th class="hide-mob hide-tablet">Chg5m</th><th class="hide-mob hide-tablet">Remaining</th><th>Result</th><th>P&amp;L</th></tr>';
  for (var i = 0; i < trades.length; i++) {
    var t = trades[i];
    if (t.resolved) {
      h += '<tr><td>' + t.number + '</td><td class="m">' + esc(t.time) + '</td>';
      if (showSlotLabel) h += '<td class="m hide-mob">' + esc(t.slotLabel) + '</td>';
      if (showSlotLabel) {
        h += '<td style="color:' + sideColor(t.side) + '">' + esc(t.side) + (t.live ? liveBadge() : '') + '</td>';
      } else {
        h += '<td>' + sideBadge(t.side) + (t.live ? liveBadge() : '') + '</td>';
      }
      h += '<td class="m">' + fmtPrice(t.buyPrice) + '</td>';
      h += '<td class="m hide-mob hide-tablet">' + fmtChange(t.change5m) + '</td><td class="m hide-mob hide-tablet">' + t.remaining + 's</td>';
      if (t.finalDir === 'early_exit') {
        h += '<td>' + exitBadge() + ' &rarr; <span class="m">' + fmtPrice(t.sellPrice) + '</span></td>';
      } else {
        h += '<td>' + (t.won ? winBadge() : lossBadge()) + ' &rarr; ';
        if (showSlotLabel) {
          h += esc(t.finalDir);
        } else {
          h += (t.finalDir === 'Up' ? sideBadge('Up') : sideBadge('Down'));
        }
        h += '</td>';
      }
      h += '<td class="m" style="color:' + pnlColor(t.pnl) + '">' + fmtPnL(t.pnl) + '</td></tr>';
    } else {
      h += '<tr><td>' + t.number + '</td><td class="m">' + esc(t.time) + '</td>';
      if (showSlotLabel) h += '<td class="m hide-mob">' + esc(t.slotLabel) + '</td>';
      if (showSlotLabel) {
        h += '<td style="color:' + sideColor(t.side) + '">' + esc(t.side) + (t.live ? liveBadge() : '') + '</td>';
      } else {
        h += '<td>' + sideBadge(t.side) + (t.live ? liveBadge() : '') + '</td>';
      }
      h += '<td class="m">' + fmtPrice(t.buyPrice) + '</td>';
      h += '<td class="m hide-mob hide-tablet">' + fmtChange(t.change5m) + '</td><td class="m hide-mob hide-tablet">' + t.remaining + 's</td>';
      h += '<td>' + pendingBadge() + '</td><td class="dim">&mdash;</td></tr>';
    }
  }
  h += '</table></div>';

  // Mobile cards
  h += '<div class="md:hidden space-y-2">';
  for (var i = 0; i < trades.length; i++) {
    var t = trades[i];
    h += '<div class="mob-card">';
    h += '<div class="flex items-center justify-between mb-1.5">';
    h += '<div class="flex items-center gap-1.5 text-xs">';
    h += '<span class="text-slate-500">#' + t.number + '</span>';
    if (showSlotLabel) {
      h += '<span style="color:' + sideColor(t.side) + '">' + esc(t.side) + '</span>';
    } else {
      h += sideBadge(t.side);
    }
    if (t.live) h += liveBadge();
    h += '</div>';
    h += '<span class="m text-xs">' + fmtPrice(t.buyPrice) + '</span>';
    h += '</div>';
    h += '<div class="flex items-center justify-between text-xs">';
    h += '<div class="flex items-center gap-1.5">';
    if (t.resolved) {
      if (t.finalDir === 'early_exit') {
        h += exitBadge() + '<span class="text-slate-500">&rarr;</span><span class="m">' + fmtPrice(t.sellPrice) + '</span>';
      } else if (showSlotLabel) {
        h += (t.won ? winBadge() : lossBadge()) + '<span class="text-slate-500">&rarr; ' + esc(t.finalDir) + '</span>';
      } else {
        h += (t.won ? winBadge() : lossBadge()) + '<span class="text-slate-500">&rarr;</span>' + (t.finalDir === 'Up' ? sideBadge('Up') : sideBadge('Down'));
      }
    } else {
      h += pendingBadge();
    }
    h += '</div>';
    h += '<div class="flex items-center gap-2">';
    h += '<span class="m text-[11px] text-slate-400">' + esc(t.time) + '</span>';
    if (t.resolved) {
      h += '<span class="m font-semibold" style="color:' + pnlColor(t.pnl) + '">' + fmtPnL(t.pnl) + '</span>';
    } else {
      h += '<span class="dim">&mdash;</span>';
    }
    h += '</div></div></div>';
  }
  h += '</div>';
  return h;
}

function renderTradeHistory(trades) {
  if (!trades || trades.length === 0) return '';
  var h = '<div class="mb-7"><h2 class="sec-title">Trade History</h2>';
  h += renderTradeRows(trades, false);
  h += '</div>';
  return h;
}

// ---------------------------------------------------------------------------
// Window results renderer
// ---------------------------------------------------------------------------

function renderWindowResults(windows) {
  if (!windows || !windows.results || windows.results.length === 0) return '';
  var h = '<div class="mb-7"><details data-detail-id="window-results">';
  h += '<summary class="text-sm text-slate-400 pb-1.5 border-b border-slate-700 cursor-pointer list-none flex items-center gap-2">';
  h += '<svg class="w-3 h-3 transition-transform duration-200 details-arrow" viewBox="0 0 12 12" fill="none" stroke="currentColor" stroke-width="2"><path d="M4 2l4 4-4 4"/></svg>';
  h += 'Window Results <span class="text-slate-500">&mdash; ' + windows.count + ' windows (<span class="g">' + windows.upCount + ' Up</span> / <span class="r">' + windows.downCount + ' Down</span>)</span>';
  h += '</summary>';

  // Desktop table
  h += '<div class="hidden md:block overflow-x-auto mt-2.5">';
  h += '<table><tr><th>End Time</th><th>Direction</th><th>Signal</th><th>BTC Open</th><th>BTC Close</th><th>Change</th><th>Traded</th></tr>';
  for (var i = 0; i < windows.results.length; i++) {
    var r = windows.results[i];
    h += '<tr><td class="m">' + esc(r.endTime) + '</td>';
    h += '<td>' + sideBadge(r.direction) + '</td>';
    if (r.marketSignal === 'Up') { h += '<td>' + sideBadge('Up') + '</td>'; }
    else if (r.marketSignal === 'Down') { h += '<td>' + sideBadge('Down') + '</td>'; }
    else { h += '<td><span class="dim">&mdash;</span></td>'; }
    h += '<td class="m">' + fmtBTC(r.btcOpen) + '</td><td class="m">' + fmtBTC(r.btcClose) + '</td>';
    h += '<td class="m ' + (r.change < 0 ? 'r' : 'g') + '">' + fmtChange(r.change) + '</td>';
    h += '<td>';
    if (r.tradedSlots && r.tradedSlots.length > 0) {
      h += '<span class="inline-flex flex-wrap gap-1">';
      for (var j = 0; j < r.tradedSlots.length; j++) {
        var s = r.tradedSlots[j];
        h += '<span class="badge ' + (s.won ? 'bw' : 'bl') + '">' + esc(s.label) + ' ' + (s.won ? 'W' : 'L') + '</span>';
      }
      h += '</span>';
    } else {
      h += '<span class="dim">&mdash;</span>';
    }
    h += '</td></tr>';
  }
  h += '</table></div>';

  // Mobile cards
  h += '<div class="md:hidden space-y-2 mt-2.5">';
  for (var i = 0; i < windows.results.length; i++) {
    var r = windows.results[i];
    h += '<div class="mob-card win-mob">';
    h += '<div class="flex justify-between items-center">';
    h += '<span class="m text-[11px] text-slate-400">' + esc(r.endTime) + '</span>';
    h += '<div class="flex items-center gap-1">';
    h += '<span class="win-label">dir</span>' + sideBadge(r.direction);
    if (r.marketSignal) {
      h += '<span class="win-label" style="margin-left:4px">sig</span>' + sideBadge(r.marketSignal);
    }
    h += '</div></div>';
    h += '<div class="win-price-row">';
    h += '<span class="m text-slate-300">' + fmtBTC(r.btcOpen) + '</span><span class="text-slate-600">&rarr;</span><span class="m text-slate-300">' + fmtBTC(r.btcClose) + '</span>';
    h += '<span class="m font-semibold ' + (r.change < 0 ? 'r' : 'g') + ' ml-auto">' + fmtChange(r.change) + '</span>';
    h += '</div>';
    if (r.tradedSlots && r.tradedSlots.length > 0) {
      h += '<div class="win-slots">';
      for (var j = 0; j < r.tradedSlots.length; j++) {
        var s = r.tradedSlots[j];
        h += '<span class="badge ' + (s.won ? 'bw' : 'bl') + '">' + esc(s.label) + ' ' + (s.won ? 'W' : 'L') + '</span>';
      }
      h += '</div>';
    }
    h += '</div>';
  }
  h += '</div></details></div>';
  return h;
}

// ---------------------------------------------------------------------------
// Price comparison (multi mode)
// ---------------------------------------------------------------------------

function renderLastTradeTd(lt) {
  if (!lt) return '<td class="dim">&mdash;</td>';
  var h = '<td class="m" style="font-size:11px;white-space:nowrap">';
  h += esc(lt.time) + ' ' + sideBadge(lt.side) + ' @' + fmtFloat2(lt.buyPrice) + ' ';
  if (lt.resolved) {
    h += (lt.won ? '<span class="badge bw">W</span>' : '<span class="badge bl">L</span>') + ' <span style="color:' + pnlColor(lt.pnl) + '">' + fmtPnL(lt.pnl) + '</span>';
  } else {
    h += '<span class="badge bp">P</span>';
  }
  h += '</td>';
  return h;
}

function renderPriceComparison(slots) {
  if (!slots || slots.length === 0) return '';
  var h = '<div class="mb-7 overflow-x-auto"><h2 class="sec-title">Price Comparison</h2>';

  // Desktop table
  h += '<div class="hidden md:block">';
  h += '<table class="w-full table-auto"><tr><th class="whitespace-nowrap">Entry Price</th><th class="whitespace-nowrap">Trades</th><th class="whitespace-nowrap">Wins</th><th class="whitespace-nowrap">Losses</th><th class="whitespace-nowrap">Win Rate</th><th class="whitespace-nowrap">Total P&amp;L</th><th class="whitespace-nowrap">Avg P&amp;L</th><th class="whitespace-nowrap">Sharpe</th><th class="whitespace-nowrap">Max DD</th><th class="whitespace-nowrap">Profit Factor</th><th class="whitespace-nowrap">Expectancy</th><th class="whitespace-nowrap">Last Trade</th><th></th></tr>';
  for (var i = 0; i < slots.length; i++) {
    var s = slots[i];
    if (s.hasResolved) {
      h += '<tr' + (s.isBest ? ' class="best"' : '') + '>';
      h += '<td class="m" style="white-space:nowrap"><span class="inline-flex items-center gap-1.5">' + esc(s.label) + (s.live ? liveBadge() : '') + (s.isBest ? ' <span class="badge bw">BEST</span>' : '') + '</span></td>';
      h += '<td>' + s.trades + '</td><td class="g">' + s.wins + '</td><td class="r">' + s.losses + '</td>';
      h += '<td style="color:' + wrColor(s.winRate) + '">' + fmtPct(s.winRate) + '</td>';
      h += '<td class="m" style="color:' + pnlColor(s.totalPnL) + '">' + fmtPnL(s.totalPnL) + '</td>';
      h += '<td class="m" style="color:' + pnlColor(s.avgPnL) + '">' + fmtPnL(s.avgPnL) + '</td>';
      h += '<td class="m" style="color:' + sharpeColor(s.metrics.sharpeRatio) + '">' + fmtFloat2(s.metrics.sharpeRatio) + '</td>';
      h += '<td class="m r">' + fmtPrice2(s.metrics.maxDrawdown) + '</td>';
      h += '<td class="m" style="color:' + profitFactorColor(s.metrics.profitFactor) + '">' + fmtFloat2(s.metrics.profitFactor) + '</td>';
      h += '<td class="m" style="color:' + pnlColor(s.metrics.expectancy) + '">' + fmtPnL(s.metrics.expectancy) + '</td>';
      h += renderLastTradeTd(s.lastTrade);
      h += '<td><div class="bar"><span style="width:' + fmtBarWidth(Math.min(s.winRate, 100)) + ';background:' + wrColor(s.winRate) + '"></span></div></td></tr>';
    } else {
      h += '<tr><td class="m" style="white-space:nowrap"><span class="inline-flex items-center gap-1.5">' + esc(s.label) + (s.live ? liveBadge() : '') + '</span></td>';
      h += '<td>' + s.trades + '</td><td class="dim" colspan="10">no resolved trades</td>';
      h += renderLastTradeTd(s.lastTrade);
      h += '</tr>';
    }
  }
  h += '</table></div>';

  // Mobile cards
  h += '<div class="md:hidden space-y-2">';
  for (var i = 0; i < slots.length; i++) {
    var s = slots[i];
    h += '<div class="mob-card" style="border-radius:12px">';
    h += '<div class="flex justify-between items-center mb-2">';
    h += '<div class="flex items-center gap-1.5"><span class="text-sm font-bold text-slate-100">' + esc(s.label) + '</span>' + (s.live ? liveBadge() : '') + (s.isBest ? '<span class="badge bw">BEST</span>' : '') + '</div>';
    if (s.hasResolved) {
      h += '<span class="m font-bold" style="color:' + pnlColor(s.totalPnL) + ';font-size:15px">' + fmtPnL(s.totalPnL) + '</span>';
    }
    h += '</div>';
    if (s.hasResolved) {
      h += '<div class="flex flex-wrap gap-x-3 gap-y-1 text-xs mb-1.5">';
      h += '<span><span class="g">' + s.wins + '</span>W / <span class="r">' + s.losses + '</span>L</span>';
      h += '<span style="color:' + wrColor(s.winRate) + '">' + fmtPct(s.winRate) + '</span>';
      h += '<span class="m" style="color:' + pnlColor(s.avgPnL) + '">avg ' + fmtPnL(s.avgPnL) + '</span>';
      h += '<span class="m" style="color:' + profitFactorColor(s.metrics.profitFactor) + '">PF ' + fmtFloat2(s.metrics.profitFactor) + '</span>';
      h += '</div>';
    } else {
      h += '<div class="text-xs mb-1.5"><span class="dim">' + s.trades + ' trades &middot; no resolved</span></div>';
    }
    if (s.lastTrade) {
      var lt = s.lastTrade;
      h += '<div class="text-[11px] text-slate-300 flex flex-wrap items-center gap-1 gap-x-1.5 mt-1.5 pt-1.5 border-t border-slate-700/50">';
      h += '<span style="color:var(--text-muted);font-weight:600;font-size:9px">LAST</span>';
      if (lt.resolved) {
        h += '<span class="m">' + esc(lt.time) + '</span>' + sideBadge(lt.side) + '<span>@<span class="m">' + fmtFloat2(lt.buyPrice) + '</span></span>' + (lt.won ? '<span class="badge bw">W</span>' : '<span class="badge bl">L</span>') + '<span class="m" style="color:' + pnlColor(lt.pnl) + '">' + fmtPnL(lt.pnl) + '</span>';
      } else {
        h += '<span class="m">' + esc(lt.time) + '</span>' + sideBadge(lt.side) + '<span>@<span class="m">' + fmtFloat2(lt.buyPrice) + '</span></span><span class="badge bp">P</span>';
      }
      h += '</div>';
    }
    h += '</div>';
  }
  h += '</div></div>';
  return h;
}

// ---------------------------------------------------------------------------
// Slot detail (multi mode)
// ---------------------------------------------------------------------------

function renderSlotDetail(detail) {
  var h = '<div class="slot-panel">';
  h += '<h3 class="slot-title">' + esc(detail.label) + (detail.live ? liveBadge() : '') + '</h3>';

  // Last trade info
  if (detail.lastTrade) {
    var lt = detail.lastTrade;
    h += '<div class="mb-3 text-[12px] sm:text-[13px] text-slate-300 flex flex-wrap items-center gap-1 gap-x-1.5 sm:gap-x-2">';
    h += '<span class="text-[10px] sm:text-xs font-medium text-slate-500">Last</span>';
    if (lt.resolved) {
      h += '<span class="m">' + esc(lt.time) + '</span>' + sideBadge(lt.side) + '<span>@ <span class="m">' + fmtPrice(lt.buyPrice) + '</span></span>' + (lt.won ? winBadge() : lossBadge()) + '<span>&rarr;</span> ' + (lt.finalDir === 'Up' ? sideBadge('Up') : sideBadge('Down')) + '<span class="m" style="color:' + pnlColor(lt.pnl) + '">' + fmtPnL(lt.pnl) + '</span><span class="dim">' + esc(lt.ago) + ' ago</span>';
    } else {
      h += '<span class="m">' + esc(lt.time) + '</span>' + sideBadge(lt.side) + '<span>@ <span class="m">' + fmtPrice(lt.buyPrice) + '</span></span>' + pendingBadge() + '<span class="dim">' + esc(lt.ago) + ' ago</span>';
    }
    h += '</div>';
  }

  // Summary cards
  h += '<div class="cards stagger">';
  h += '<div class="card"><div class="v">' + detail.trades + '</div><div class="l">Trades</div></div>';
  h += '<div class="card"><div class="v g">' + detail.wins + '</div><div class="l">Wins</div></div>';
  h += '<div class="card"><div class="v r">' + detail.losses + '</div><div class="l">Losses</div></div>';
  if (detail.hasResolved) {
    h += '<div class="card"><div class="v" style="color:' + wrColor(detail.winRate) + '">' + fmtPct(detail.winRate) + '</div><div class="l">Win Rate</div></div>';
    h += '<div class="card card-pnl" style="border-left-color:' + pnlColor(detail.totalPnL) + '"><div class="v" style="color:' + pnlColor(detail.totalPnL) + '">' + fmtPnL(detail.totalPnL) + '</div><div class="l">Total P&amp;L</div></div>';
    h += '<div class="card"><div class="v" style="color:' + pnlColor(detail.avgPnL) + '">' + fmtPnL(detail.avgPnL) + '</div><div class="l">Avg P&amp;L</div></div>';
    h += '<div class="card"><div class="v" style="color:' + sharpeColor(detail.metrics.sharpeRatio) + '">' + fmtFloat2(detail.metrics.sharpeRatio) + '</div><div class="l">Sharpe</div></div>';
    h += '<div class="card"><div class="v r">' + fmtPrice2(detail.metrics.maxDrawdown) + '</div><div class="l">Max DD</div></div>';
    h += '<div class="card"><div class="v" style="color:' + profitFactorColor(detail.metrics.profitFactor) + '">' + fmtFloat2(detail.metrics.profitFactor) + '</div><div class="l">Profit Factor</div></div>';
    h += '<div class="card"><div class="v" style="color:' + pnlColor(detail.metrics.expectancy) + '">' + fmtPnL(detail.metrics.expectancy) + '</div><div class="l">Expectancy</div></div>';
  }
  h += '</div>';

  // Compact equity SVG
  if (detail.equity && detail.equity.points && detail.equity.points.length >= 2) {
    h += renderCompactEquitySVG(detail.equity);
  }

  // Bucket tables
  if (detail.hasResolved) {
    h += renderBucketTable('Win Rate by Direction', detail.directionBuckets);
    h += renderBucketTable('Win Rate by Entry Timing', detail.timingBuckets);
  }

  // Collapsible trade history
  if (detail.tradeHistory && detail.tradeHistory.length > 0) {
    h += '<details class="mt-2" data-detail-id="slot-trades-' + esc(detail.label) + '"><summary class="text-xs text-slate-400 py-1.5">Trade History <span class="text-slate-500">(' + detail.tradeHistory.length + ')</span></summary>';
    h += renderTradeRows(detail.tradeHistory, false);
    h += '</details>';
  }

  h += '</div>';
  return h;
}

// ---------------------------------------------------------------------------
// History renderer
// ---------------------------------------------------------------------------

function renderHistory(sessions) {
  if (!sessions || sessions.length === 0) return '';
  var h = '<div class="mb-7"><h2 class="sec-title">Historical Sessions</h2>';

  for (var si = 0; si < sessions.length; si++) {
    var s = sessions[si];
    h += '<details class="mb-2 bg-surface border border-slate-800/50 rounded-lg px-3 py-1" data-detail-id="history-' + esc(s.sessionID) + '">';
    h += '<summary class="py-2.5 text-slate-100 list-none">';

    // Desktop summary
    h += '<div class="hidden sm:block text-[13px]">';
    h += '<span class="m text-slate-300">' + esc(s.sessionID) + '</span> <span class="text-slate-600">&middot;</span> <span class="m text-slate-300">' + esc(s.startedAt) + '</span> <span class="text-slate-600">&middot;</span> <span class="m text-slate-300">' + esc(s.duration) + '</span> <span class="text-slate-600">&middot;</span> <span class="text-slate-400">' + s.trades + ' trades</span> <span class="text-slate-600">&middot;</span> <span class="g">' + s.wins + '</span>W/<span class="r">' + s.losses + '</span>L <span class="text-slate-600">&middot;</span> <span style="color:' + wrColor(s.winRate) + '">' + fmtPct(s.winRate) + '</span> <span class="text-slate-600">&middot;</span> <span class="m" style="color:' + pnlColor(s.totalPnL) + '">' + fmtPnL(s.totalPnL) + '</span>';
    h += '</div>';

    // Mobile summary
    h += '<div class="sm:hidden">';
    h += '<div class="flex justify-between items-center mb-1"><span class="m text-xs text-slate-300">' + esc(s.sessionID) + '</span><span class="m text-sm font-bold" style="color:' + pnlColor(s.totalPnL) + '">' + fmtPnL(s.totalPnL) + '</span></div>';
    h += '<div class="flex flex-wrap gap-x-2 gap-y-0.5 text-[11px]"><span class="m text-slate-400">' + esc(s.startedAt) + '</span><span class="m text-slate-400">' + esc(s.duration) + '</span><span><span class="g">' + s.wins + '</span>W/<span class="r">' + s.losses + '</span>L</span><span style="color:' + wrColor(s.winRate) + '">' + fmtPct(s.winRate) + '</span></div>';
    h += '</div>';
    h += '</summary>';

    if (s.fetchError) {
      h += '<p class="text-slate-500 py-2">Failed to load trades.</p>';
    } else {
      // Per-slot summary
      if (s.slotSummaries && s.slotSummaries.length > 0) {
        h += '<div class="py-2"><span class="text-xs font-medium text-slate-400">Per-Slot Summary</span>';

        // Desktop table
        h += '<div class="hidden md:block">';
        h += '<table class="mt-1.5"><tr><th>Slot</th><th>Trades</th><th>Wins</th><th>Losses</th><th>Win Rate</th><th>P&amp;L</th></tr>';
        for (var j = 0; j < s.slotSummaries.length; j++) {
          var ss = s.slotSummaries[j];
          h += '<tr><td class="m">' + esc(ss.slot) + (ss.live ? liveBadge() : '') + '</td><td>' + ss.trades + '</td><td class="g">' + ss.wins + '</td><td class="r">' + ss.losses + '</td>';
          h += '<td style="color:' + wrColor(ss.winRate) + '">' + fmtPct(ss.winRate) + '</td>';
          h += '<td class="m" style="color:' + pnlColor(ss.pnl) + '">' + fmtPnL(ss.pnl) + '</td></tr>';
        }
        h += '</table></div>';

        // Mobile cards
        h += '<div class="md:hidden space-y-2 mt-1.5">';
        for (var j = 0; j < s.slotSummaries.length; j++) {
          var ss = s.slotSummaries[j];
          h += '<div class="mob-card">';
          h += '<div class="flex justify-between items-center mb-1"><span class="m text-sm">' + esc(ss.slot) + (ss.live ? liveBadge() : '') + '</span><span class="m font-bold" style="color:' + pnlColor(ss.pnl) + '">' + fmtPnL(ss.pnl) + '</span></div>';
          h += '<div class="flex gap-3 text-xs">';
          h += '<span><span class="g">' + ss.wins + '</span>W / <span class="r">' + ss.losses + '</span>L</span>';
          h += '<span style="color:' + wrColor(ss.winRate) + '">' + fmtPct(ss.winRate) + '</span>';
          h += '<span class="text-slate-500">' + ss.trades + ' trades</span>';
          h += '</div></div>';
        }
        h += '</div></div>';
      }

      // Trade details (collapsible)
      if (s.tradeDetails && s.tradeDetails.length > 0) {
        h += '<details class="pb-2" data-detail-id="history-trades-' + esc(s.sessionID) + '"><summary class="text-xs text-slate-400 py-1.5">Trade Details <span class="text-slate-500">(' + s.tradeDetails.length + ')</span></summary>';
        // Desktop table
        h += '<div class="hidden md:block">';
        h += '<table class="mt-1.5"><tr><th>#</th><th>Time</th><th class="hide-mob">Slot</th><th>Side</th><th>Price</th><th class="hide-mob hide-tablet">Chg5m</th><th class="hide-mob hide-tablet">Remaining</th><th>Result</th><th>P&amp;L</th></tr>';
        for (var k = 0; k < s.tradeDetails.length; k++) {
          var t = s.tradeDetails[k];
          if (t.resolved) {
            h += '<tr><td>' + t.number + '</td><td class="m">' + esc(t.time) + '</td><td class="m hide-mob">' + esc(t.slotLabel) + '</td>';
            h += '<td style="color:' + sideColor(t.side) + '">' + esc(t.side) + (t.live ? liveBadge() : '') + '</td>';
            h += '<td class="m">' + fmtPrice(t.buyPrice) + '</td>';
            h += '<td class="m hide-mob hide-tablet">' + fmtChange(t.change5m) + '</td><td class="m hide-mob hide-tablet">' + t.remaining + 's</td>';
            h += '<td>' + (t.won ? winBadge() : lossBadge()) + ' &rarr; ' + esc(t.finalDir) + '</td>';
            h += '<td class="m" style="color:' + pnlColor(t.pnl) + '">' + fmtPnL(t.pnl) + '</td></tr>';
          } else {
            h += '<tr><td>' + t.number + '</td><td class="m">' + esc(t.time) + '</td><td class="m hide-mob">' + esc(t.slotLabel) + '</td>';
            h += '<td style="color:' + sideColor(t.side) + '">' + esc(t.side) + (t.live ? liveBadge() : '') + '</td>';
            h += '<td class="m">' + fmtPrice(t.buyPrice) + '</td>';
            h += '<td class="m hide-mob hide-tablet">' + fmtChange(t.change5m) + '</td><td class="m hide-mob hide-tablet">' + t.remaining + 's</td>';
            h += '<td>' + pendingBadge() + '</td><td class="dim">&mdash;</td></tr>';
          }
        }
        h += '</table></div>';

        // Mobile cards
        h += '<div class="md:hidden space-y-2 mt-1.5">';
        for (var k = 0; k < s.tradeDetails.length; k++) {
          var t = s.tradeDetails[k];
          h += '<div class="mob-card">';
          h += '<div class="flex items-center justify-between mb-1.5">';
          h += '<div class="flex items-center gap-1.5 text-xs">';
          h += '<span class="text-slate-500">#' + t.number + '</span>';
          h += '<span style="color:' + sideColor(t.side) + '">' + esc(t.side) + '</span>';
          if (t.live) h += liveBadge();
          h += '</div>';
          h += '<span class="m text-xs">' + fmtPrice(t.buyPrice) + '</span>';
          h += '</div>';
          h += '<div class="flex items-center justify-between text-xs">';
          h += '<div class="flex items-center gap-1.5">';
          if (t.resolved) {
            h += (t.won ? winBadge() : lossBadge()) + '<span class="text-slate-500">&rarr; ' + esc(t.finalDir) + '</span>';
          } else {
            h += pendingBadge();
          }
          h += '</div>';
          h += '<div class="flex items-center gap-2">';
          h += '<span class="m text-[11px] text-slate-400">' + esc(t.time) + '</span>';
          if (t.resolved) {
            h += '<span class="m font-semibold" style="color:' + pnlColor(t.pnl) + '">' + fmtPnL(t.pnl) + '</span>';
          } else {
            h += '<span class="dim">&mdash;</span>';
          }
          h += '</div></div></div>';
        }
        h += '</div></details>';
      }
    }
    h += '</details>';
  }
  h += '</div>';
  return h;
}

// ---------------------------------------------------------------------------
// Tab panel renderer (single mode)
// ---------------------------------------------------------------------------

function renderTabs(d) {
  var h = '<div class="relative">';
  h += '<input type="radio" name="tabs" id="tab1" class="tab-radio" checked>';
  h += '<input type="radio" name="tabs" id="tab2" class="tab-radio">';
  h += '<input type="radio" name="tabs" id="tab3" class="tab-radio">';
  h += '<input type="radio" name="tabs" id="tab4" class="tab-radio">';
  h += '<div class="tab-bar">';
  h += '<label for="tab1" class="tab-label">Analysis</label>';
  h += '<label for="tab2" class="tab-label">Trades</label>';
  h += '<label for="tab3" class="tab-label">Windows</label>';
  h += '<label for="tab4" class="tab-label">History</label>';
  h += '</div>';
  h += '<div class="border-b border-slate-800/50 mb-4 md:mb-6"></div>';
  h += '<div class="tab-panels">';

  // Panel 1: Analysis
  h += '<div class="panel panel-1">';
  if (d.summary.hasResolved) {
    h += renderBucketTable('Win Rate by Buy Price', d.buckets.price);
    h += renderBucketTable('Win Rate by |Change5m|', d.buckets.change);
    h += renderBucketTable('Win Rate by Direction', d.buckets.direction);
    h += renderBucketTable('Win Rate by Entry Timing', d.buckets.timing);
    h += renderProfitability(d.profitSim);
  }
  h += renderEquitySVG(d.equity);
  h += '</div>';

  // Panel 2: Trades
  h += '<div class="panel panel-2">';
  h += renderTradeHistory(d.trades);
  h += '</div>';

  // Panel 3: Windows
  h += '<div class="panel panel-3">';
  h += renderWindowResults(d.windows);
  h += '</div>';

  // Panel 4: History
  h += '<div class="panel panel-4">';
  h += renderHistory(d.history);
  h += '</div>';

  h += '</div></div>';
  return h;
}

// ---------------------------------------------------------------------------
// Page composers
// ---------------------------------------------------------------------------

function renderSinglePage(d) {
  var h = renderHeader(d, 'single');
  h += '<main class="max-w-7xl mx-auto px-3 sm:px-4 md:px-6 py-4 md:py-8">';

  // Title + meta row
  h += '<div class="mb-4 md:mb-8">';
  h += '<h1 class="text-base sm:text-lg md:text-2xl font-bold text-slate-100 mb-2 md:mb-1.5">' + esc(d.meta.title) + '</h1>';
  // Desktop meta row
  h += '<div class="hidden sm:flex flex-wrap items-center gap-x-3 gap-y-1 text-sm">';
  h += '<span class="text-slate-400">Started <span class="m text-slate-200" data-live="start">' + esc(d.meta.startTime) + '</span></span>';
  h += '<span class="text-slate-600">&middot;</span>';
  h += '<span class="text-slate-400">Now <span class="m text-slate-200" data-live="clock">' + esc(d.meta.startTime) + '</span></span>';
  h += '<span class="text-slate-600">&middot;</span>';
  h += '<span class="text-slate-400"><span class="m text-slate-200">' + d.meta.evalCount + '</span> evals</span>';
  h += '<span class="text-slate-600">&middot;</span>';
  h += '<span class="text-slate-500 text-xs" data-live="tz"></span>';
  h += '</div>';
  // Mobile meta grid
  h += '<div class="sm:hidden grid grid-cols-3 gap-1.5">';
  h += '<div class="meta-pill"><span class="meta-k">Started</span><span class="m meta-v" data-live="start">' + esc(d.meta.startTime) + '</span></div>';
  h += '<div class="meta-pill"><span class="meta-k">Now</span><span class="m meta-v" data-live="clock">' + esc(d.meta.startTime) + '</span></div>';
  h += '<div class="meta-pill"><span class="meta-k">Evals</span><span class="m meta-v">' + d.meta.evalCount + '</span></div>';
  h += '</div>';
  h += '<div class="sm:hidden text-xs text-slate-500 mt-1 ml-1" data-live="tz"></div>';
  h += '</div>';

  // Summary cards
  h += renderSummaryCards(d.summary);

  // Risk metrics (only if resolved)
  if (d.summary.hasResolved) {
    h += renderRiskCards(d.metrics);
  }

  // Hold reasons
  h += renderHoldReasons(d.holdReasons, d.totalHolds, d.meta.evalCount);

  // Tabs
  h += renderTabs(d);

  h += '</main>';
  return h;
}

function renderMultiPage(d) {
  var h = renderHeader(d, 'multi');
  h += '<main class="max-w-7xl mx-auto px-3 sm:px-4 md:px-6 py-4 md:py-8">';

  // Title + meta row
  h += '<div class="mb-4 md:mb-8">';
  h += '<h1 class="text-base sm:text-lg md:text-2xl font-bold text-slate-100 mb-2 md:mb-1.5">Multi-Price Paper Trading</h1>';
  // Desktop meta row
  h += '<div class="hidden sm:flex flex-wrap items-center gap-x-3 gap-y-1 text-sm">';
  h += '<span class="text-slate-400">Started <span class="m text-slate-200" data-live="start">' + esc(d.meta.startTime) + '</span></span>';
  h += '<span class="text-slate-600">&middot;</span>';
  h += '<span class="text-slate-400">Now <span class="m text-slate-200" data-live="clock">' + esc(d.meta.startTime) + '</span></span>';
  h += '<span class="text-slate-600">&middot;</span>';
  h += '<span class="text-slate-400"><span class="m text-slate-200">' + d.meta.slotCount + '</span> slots</span>';
  h += '<span class="text-slate-600">&middot;</span>';
  h += '<span class="text-slate-500 text-xs" data-live="tz"></span>';
  h += '</div>';
  // Mobile meta grid
  h += '<div class="sm:hidden grid grid-cols-3 gap-1.5">';
  h += '<div class="meta-pill"><span class="meta-k">Started</span><span class="m meta-v" data-live="start">' + esc(d.meta.startTime) + '</span></div>';
  h += '<div class="meta-pill"><span class="meta-k">Now</span><span class="m meta-v" data-live="clock">' + esc(d.meta.startTime) + '</span></div>';
  h += '<div class="meta-pill"><span class="meta-k">Slots</span><span class="m meta-v">' + d.meta.slotCount + '</span></div>';
  h += '</div>';
  h += '<div class="sm:hidden text-xs text-slate-500 mt-1 ml-1" data-live="tz"></div>';
  h += '</div>';

  // Window results
  h += renderWindowResults(d.windows);

  // Price comparison
  h += renderPriceComparison(d.slots);

  // Slot details
  if (d.details) {
    for (var i = 0; i < d.details.length; i++) {
      h += renderSlotDetail(d.details[i]);
    }
  }

  // History
  h += renderHistory(d.history);

  h += '</main>';
  return h;
}

// ---------------------------------------------------------------------------
// Backtest page renderers
// ---------------------------------------------------------------------------

function renderBacktestForm(form) {
  var inputCls = 'w-full bg-elevated border rounded-lg px-3 py-2 text-sm text-slate-200 placeholder-slate-600 font-mono focus:outline-none focus:border-slate-500 transition-colors';
  var labelCls = 'block text-xs text-slate-500 mb-1.5 uppercase tracking-wider font-medium';
  var h = '<form method="GET" action="/backtest" id="backtestForm" class="bg-surface border rounded-xl p-3 md:p-4 mb-5 md:mb-6">';
  h += '<input type="hidden" name="params" id="paramsInput" value="' + esc(form.paramsSpec) + '">';
  h += '<div class="flex flex-col sm:flex-row gap-3 sm:items-end">';

  // sweep input
  h += '<div class="flex-1 min-w-0">';
  h += '<label class="' + labelCls + '">Sweep</label>';
  h += '<input type="text" name="sweep" value="' + esc(form.sweepSpec) + '" placeholder="e.g. 0.50:0.60:0.01" class="' + inputCls + '">';
  h += '</div>';

  // split input
  h += '<div class="w-full sm:w-28">';
  h += '<label class="' + labelCls + '">Split %</label>';
  h += '<input type="text" name="split" value="' + esc(form.splitStr) + '" placeholder="e.g. 70" class="' + inputCls + '">';
  h += '</div>';

  // from date input
  h += '<div class="w-full sm:w-36">';
  h += '<label class="' + labelCls + '">From</label>';
  h += '<input type="text" name="from" value="' + esc(form.fromStr) + '" placeholder="2024-01-01" class="' + inputCls + '">';
  h += '</div>';

  // to date input
  h += '<div class="w-full sm:w-36">';
  h += '<label class="' + labelCls + '">To</label>';
  h += '<input type="text" name="to" value="' + esc(form.toStr) + '" placeholder="2024-12-31" class="' + inputCls + '">';
  h += '</div>';

  // wf input
  h += '<div class="w-full sm:w-28">';
  h += '<label class="' + labelCls + '">WF</label>';
  h += '<input type="text" name="wf" value="' + esc(form.wfSpec) + '" placeholder="e.g. 5:2:1" class="' + inputCls + '">';
  h += '</div>';

  // Run button
  h += '<div class="flex-shrink-0">';
  h += '<button type="submit" class="w-full sm:w-auto px-5 py-2 bg-cyan-600 hover:bg-cyan-500 text-white text-sm font-medium rounded-lg transition-colors">Run</button>';
  h += '</div>';

  h += '</div>';

  // ParamGroups collapsible
  if (form.paramGroups && form.paramGroups.length > 0) {
    h += '<details class="mt-3">';
    h += '<summary class="text-xs text-slate-500 cursor-pointer list-none flex items-center gap-2">';
    h += '<svg class="w-3 h-3 transition-transform duration-200 details-arrow" viewBox="0 0 12 12" fill="none" stroke="currentColor" stroke-width="2"><path d="M4 2l4 4-4 4"/></svg>';
    h += 'Strategy Config Overrides</summary>';
    h += '<div class="mt-3 grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">';
    for (var gi = 0; gi < form.paramGroups.length; gi++) {
      var grp = form.paramGroups[gi];
      h += '<div>';
      h += '<h4 class="text-xs text-slate-400 font-semibold mb-2 uppercase tracking-wider">' + esc(grp.name) + '</h4>';
      h += '<div class="space-y-2">';
      for (var pi = 0; pi < grp.params.length; pi++) {
        var p = grp.params[pi];
        h += '<div>';
        h += '<label class="' + labelCls + '">' + esc(p.label) + '</label>';
        if (p.toggleKey) {
          h += '<select class="cfg-input ' + inputCls + '" data-param-key="' + esc(p.key) + '">';
          h += '<option value="">default</option>';
          h += '<option value="true"' + (p.value === 'true' ? ' selected' : '') + '>true</option>';
          h += '<option value="false"' + (p.value === 'false' ? ' selected' : '') + '>false</option>';
          h += '</select>';
        } else {
          h += '<input type="text" class="cfg-input ' + inputCls + '" data-param-key="' + esc(p.key) + '" value="' + esc(p.value) + '" placeholder="' + esc(p.placeholder) + '">';
        }
        h += '</div>';
      }
      h += '</div></div>';
    }
    h += '</div>';
    h += '<div class="mt-3 flex gap-2">';
    h += '<button type="button" onclick="applyConfigParams()" class="px-3 py-1.5 bg-cyan-600 hover:bg-cyan-500 text-white text-xs font-medium rounded-lg transition-colors">Apply &amp; Run</button>';
    h += '<button type="button" onclick="resetConfigParams()" class="px-3 py-1.5 bg-elevated hover:bg-slate-700 text-slate-300 text-xs font-medium rounded-lg border transition-colors">Reset</button>';
    h += '</div>';
    h += '</details>';
  }

  h += '</form>';
  return h;
}

function renderConfigYAML(config) {
  if (!config || !config.yaml) return '';
  var h = '<details class="bg-surface border rounded-xl p-3 md:p-4 mb-5 md:mb-6" data-detail-id="config-yaml">';
  h += '<summary class="text-sm text-slate-400 cursor-pointer list-none flex items-center gap-2">';
  h += '<svg class="w-3 h-3 transition-transform duration-200 details-arrow" viewBox="0 0 12 12" fill="none" stroke="currentColor" stroke-width="2"><path d="M4 2l4 4-4 4"/></svg>';
  h += 'Effective Config (YAML)</summary>';
  h += '<div class="mt-3 relative">';
  h += '<button onclick="copyConfigYAML(this)" class="absolute top-2 right-2 px-2 py-1 text-xs bg-elevated hover:bg-slate-700 text-slate-400 rounded border transition-colors">Copy</button>';
  h += '<pre id="configYAML" class="bg-elevated rounded-lg p-3 text-xs text-slate-300 font-mono overflow-x-auto whitespace-pre">' + esc(config.yaml) + '</pre>';
  h += '</div></details>';
  return h;
}

function renderSplitBanner(split) {
  if (!split || !split.hasSplit) return '';
  var h = '<div class="bg-surface border border-amber-500/30 rounded-xl p-3 md:p-4 mb-5 md:mb-6">';
  h += '<div class="flex items-center gap-2 mb-1">';
  h += '<span class="text-amber-500 text-sm font-semibold">Train/Test Split</span>';
  h += '</div>';
  h += '<div class="text-sm text-slate-400">';
  h += 'Split ratio: <span class="m text-slate-200">' + fmtPct(split.splitRatio * 100) + '</span>';
  h += ' &middot; Train: <span class="m text-slate-200">' + split.trainWindows + '</span> windows';
  h += ' &middot; Test: <span class="m text-slate-200">' + split.testWindows + '</span> windows';
  h += '</div></div>';
  return h;
}

function renderBacktestSummary(summary, hasSplit) {
  if (!summary) return '';
  var h = '<div class="cards stagger mb-5 md:mb-6">';
  h += '<div class="card"><div class="v">' + summary.configCount + '</div><div class="l">Configs</div></div>';
  h += '<div class="card"><div class="v">' + summary.totalTrades + '</div><div class="l">Total Trades</div></div>';
  h += '<div class="card card-pnl" style="border-left-color:' + pnlColor(summary.bestPnL) + '"><div class="v" style="color:' + pnlColor(summary.bestPnL) + '">' + fmtPnL(summary.bestPnL) + '</div><div class="l">Best P&amp;L</div></div>';
  h += '<div class="card"><div class="v" style="color:' + wrColor(summary.bestWinRate) + '">' + fmtPct(summary.bestWinRate) + '</div><div class="l">Best WR</div></div>';
  h += '<div class="card"><div class="v" style="color:' + sharpeColor(summary.bestSharpe) + '">' + fmtFloat2(summary.bestSharpe) + '</div><div class="l">Best Sharpe</div></div>';
  var expStr = (summary.bestExpectancy >= 0 ? '+' : '') + summary.bestExpectancy.toFixed(3);
  h += '<div class="card"><div class="v" style="color:' + pnlColor(summary.bestExpectancy) + '">' + expStr + '</div><div class="l">Best E[T]</div></div>';
  h += '</div>';
  return h;
}

function renderBacktestResults(results, hasSplit) {
  if (!results || results.length === 0) return '';
  var h = '<div class="mb-5 md:mb-6 overflow-x-auto">';
  h += '<h2 class="sec-title">Sweep Results</h2>';

  // Desktop table
  h += '<div class="hidden md:block">';
  h += '<table><tr>';
  h += '<th>#</th><th>Config</th><th>Trades</th><th>Wins</th><th>Win Rate</th>';
  h += '<th>P&amp;L</th><th>Sharpe</th><th>Max DD</th><th>PF</th><th>E[T]</th>';
  h += '<th>Avg Win</th><th>Avg Loss</th><th>W/L</th><th>CW</th><th>CL</th>';
  if (hasSplit) {
    h += '<th>OOS Trades</th><th>OOS WR</th><th>OOS P&amp;L</th><th>OOS Sharpe</th><th>OOS E[T]</th><th>Degrad</th>';
  }
  h += '</tr>';
  for (var i = 0; i < results.length; i++) {
    var r = results[i];
    var rowStyle = r.isBest ? ' style="background:var(--profit-row-bg)"' : '';
    h += '<tr' + rowStyle + '>';
    h += '<td>' + r.index + '</td>';
    h += '<td class="m" style="white-space:nowrap">' + esc(r.label) + (r.isBest ? ' <span class="badge bw">BEST</span>' : '') + '</td>';
    h += '<td>' + r.trades + '</td>';
    h += '<td class="g">' + r.wins + '</td>';
    h += '<td style="color:' + wrColor(r.winRate) + '">' + fmtPct(r.winRate) + '</td>';
    h += '<td class="m" style="color:' + pnlColor(r.totalPnL) + '">' + fmtPnL(r.totalPnL) + '</td>';
    h += '<td class="m" style="color:' + sharpeColor(r.sharpe) + '">' + fmtFloat2(r.sharpe) + '</td>';
    h += '<td class="m r">' + fmtPrice2(r.maxDD) + '</td>';
    h += '<td class="m" style="color:' + profitFactorColor(parseFloat(r.profitFactor)) + '">' + esc(r.profitFactor) + '</td>';
    var eStr = (r.expectancy >= 0 ? '+' : '') + r.expectancy.toFixed(3);
    h += '<td class="m" style="color:' + pnlColor(r.expectancy) + '">' + eStr + '</td>';
    h += '<td class="m g">' + fmtPrice2(r.avgWin) + '</td>';
    h += '<td class="m r">' + fmtPrice2(r.avgLoss) + '</td>';
    h += '<td class="m">' + fmtFloat2(r.winLossRatio) + '</td>';
    h += '<td class="m g">' + r.maxConsecWins + '</td>';
    h += '<td class="m r">' + r.maxConsecLoss + '</td>';
    if (hasSplit) {
      h += '<td>' + r.oosTrades + '</td>';
      h += '<td style="color:' + wrColor(r.oosWinRate) + '">' + fmtPct(r.oosWinRate) + '</td>';
      h += '<td class="m" style="color:' + pnlColor(r.oosPnL) + '">' + fmtPnL(r.oosPnL) + '</td>';
      h += '<td class="m" style="color:' + sharpeColor(r.oosSharpe) + '">' + fmtFloat2(r.oosSharpe) + '</td>';
      var oosEStr = (r.oosExpectancy >= 0 ? '+' : '') + r.oosExpectancy.toFixed(3);
      h += '<td class="m" style="color:' + pnlColor(r.oosExpectancy) + '">' + oosEStr + '</td>';
      h += '<td class="m" style="color:' + pnlColor(-r.pnlDegradation) + '">' + fmtPct(r.pnlDegradation) + '</td>';
    }
    h += '</tr>';
  }
  h += '</table></div>';

  // Mobile cards
  h += '<div class="md:hidden space-y-2">';
  for (var i = 0; i < results.length; i++) {
    var r = results[i];
    var mobBg = r.isBest ? ' style="background:var(--profit-row-bg)"' : '';
    h += '<div class="mob-card"' + mobBg + '>';
    h += '<div class="flex justify-between items-center mb-1.5">';
    h += '<span class="m text-sm">' + esc(r.label) + (r.isBest ? ' <span class="badge bw">BEST</span>' : '') + '</span>';
    h += '<span class="m font-bold" style="color:' + pnlColor(r.totalPnL) + '">' + fmtPnL(r.totalPnL) + '</span>';
    h += '</div>';
    h += '<div class="flex flex-wrap gap-x-3 gap-y-1 text-xs">';
    h += '<span><span class="g">' + r.wins + '</span>/' + r.trades + '</span>';
    h += '<span style="color:' + wrColor(r.winRate) + '">' + fmtPct(r.winRate) + '</span>';
    h += '<span>Sharpe <span class="m" style="color:' + sharpeColor(r.sharpe) + '">' + fmtFloat2(r.sharpe) + '</span></span>';
    var mEStr = (r.expectancy >= 0 ? '+' : '') + r.expectancy.toFixed(3);
    h += '<span>E[T] <span class="m" style="color:' + pnlColor(r.expectancy) + '">' + mEStr + '</span></span>';
    if (hasSplit) {
      h += '<span>OOS <span class="m" style="color:' + pnlColor(r.oosPnL) + '">' + fmtPnL(r.oosPnL) + '</span></span>';
    }
    h += '</div></div>';
  }
  h += '</div></div>';
  return h;
}

function renderWalkForwardBanner(wf) {
  if (!wf) return '';
  var h = '<div class="bg-surface border border-purple-500/30 rounded-xl p-3 md:p-4 mb-5 md:mb-6">';
  h += '<div class="flex items-center gap-2 mb-1">';
  h += '<span class="text-purple-400 text-sm font-semibold">Walk-Forward Validation</span>';
  h += '</div>';
  h += '<div class="text-sm text-slate-400">';
  h += 'Train: <span class="m text-slate-200">' + wf.trainSize + '</span> windows';
  h += ' &middot; Test: <span class="m text-slate-200">' + wf.testSize + '</span> windows';
  h += ' &middot; Step: <span class="m text-slate-200">' + wf.stepSize + '</span>';
  h += ' &middot; Folds: <span class="m text-slate-200">' + wf.foldCount + '</span>';
  h += '</div></div>';
  return h;
}

function renderWFSummaryCards(wf) {
  if (!wf) return '';
  var h = '<div class="cards stagger mb-5 md:mb-6">';
  h += '<div class="card"><div class="v">' + wf.foldCount + '</div><div class="l">Folds</div></div>';
  h += '<div class="card"><div class="v">' + wf.totalOOSTrades + '</div><div class="l">OOS Trades</div></div>';
  h += '<div class="card card-pnl" style="border-left-color:' + pnlColor(wf.totalOOSPnL) + '"><div class="v" style="color:' + pnlColor(wf.totalOOSPnL) + '">' + fmtPnL(wf.totalOOSPnL) + '</div><div class="l">Total OOS P&amp;L</div></div>';
  h += '<div class="card"><div class="v" style="color:' + wrColor(wf.avgOOSWinRate) + '">' + fmtPct(wf.avgOOSWinRate) + '</div><div class="l">Avg OOS WR</div></div>';
  h += '<div class="card"><div class="v" style="color:' + sharpeColor(wf.avgOOSSharpe) + '">' + fmtFloat2(wf.avgOOSSharpe) + '</div><div class="l">Avg OOS Sharpe</div></div>';
  h += '<div class="card"><div class="v" style="color:' + wrColor(wf.paramStability * 100) + '">' + fmtPct(wf.paramStability * 100) + '</div><div class="l">Param Stability</div></div>';
  h += '</div>';
  return h;
}

function renderWFFolds(wf) {
  if (!wf || !wf.folds || wf.folds.length === 0) return '';
  var h = '<div class="mb-5 md:mb-6 overflow-x-auto">';
  h += '<h2 class="sec-title">Walk-Forward Folds</h2>';

  // Desktop table
  h += '<div class="hidden md:block">';
  h += '<table><tr>';
  h += '<th>Fold</th><th>Train Period</th><th>Test Period</th><th>Best Config</th>';
  h += '<th>Train P&amp;L</th><th>Test Trades</th><th>Test Wins</th><th>Test WR</th>';
  h += '<th>Test P&amp;L</th><th>Test Sharpe</th><th>Stable</th>';
  h += '</tr>';
  for (var i = 0; i < wf.folds.length; i++) {
    var f = wf.folds[i];
    h += '<tr>';
    h += '<td>' + f.index + '</td>';
    h += '<td class="m text-xs">' + esc(f.trainPeriod) + '</td>';
    h += '<td class="m text-xs">' + esc(f.testPeriod) + '</td>';
    h += '<td class="m" style="white-space:nowrap">' + esc(f.bestLabel) + '</td>';
    h += '<td class="m" style="color:' + pnlColor(f.trainPnL) + '">' + fmtPnL(f.trainPnL) + '</td>';
    h += '<td>' + f.testTrades + '</td>';
    h += '<td class="g">' + f.testWins + '</td>';
    h += '<td style="color:' + wrColor(f.testWinRate) + '">' + fmtPct(f.testWinRate) + '</td>';
    h += '<td class="m" style="color:' + pnlColor(f.testPnL) + '">' + fmtPnL(f.testPnL) + '</td>';
    h += '<td class="m" style="color:' + sharpeColor(f.testSharpe) + '">' + fmtFloat2(f.testSharpe) + '</td>';
    h += '<td>' + (f.paramStable ? '<span class="g">Yes</span>' : '<span class="r">No</span>') + '</td>';
    h += '</tr>';
  }
  h += '</table></div>';

  // Mobile cards
  h += '<div class="md:hidden space-y-2">';
  for (var i = 0; i < wf.folds.length; i++) {
    var f = wf.folds[i];
    h += '<div class="mob-card">';
    h += '<div class="flex justify-between items-center mb-1.5">';
    h += '<span class="m text-sm">Fold ' + f.index + '</span>';
    h += '<span class="m font-bold" style="color:' + pnlColor(f.testPnL) + '">' + fmtPnL(f.testPnL) + '</span>';
    h += '</div>';
    h += '<div class="text-xs text-slate-500 mb-1">' + esc(f.testPeriod) + '</div>';
    h += '<div class="flex flex-wrap gap-x-3 gap-y-1 text-xs">';
    h += '<span>Best: <span class="m text-slate-200">' + esc(f.bestLabel) + '</span></span>';
    h += '<span style="color:' + wrColor(f.testWinRate) + '">' + fmtPct(f.testWinRate) + '</span>';
    h += '<span>Sharpe <span class="m" style="color:' + sharpeColor(f.testSharpe) + '">' + fmtFloat2(f.testSharpe) + '</span></span>';
    h += '<span>' + (f.paramStable ? '<span class="g">Stable</span>' : '<span class="r">Unstable</span>') + '</span>';
    h += '</div></div>';
  }
  h += '</div></div>';
  return h;
}

function renderBacktestPage(d) {
  var h = renderHeader(d, 'backtest');
  h += '<main class="max-w-7xl mx-auto px-3 sm:px-4 md:px-6 py-4 md:py-8">';

  // Title + meta
  h += '<div class="mb-4 md:mb-6">';
  h += '<h1 class="text-base sm:text-lg md:text-2xl font-bold text-slate-100 mb-1">' + esc(d.meta.title) + '</h1>';
  h += '<div class="flex flex-wrap items-center gap-x-3 gap-y-1 text-sm">';
  h += '<span class="text-slate-400"><span class="m text-slate-200">' + d.meta.windowCount + '</span> windows</span>';
  h += '<span class="text-slate-600">&middot;</span>';
  h += '<span class="text-slate-400"><span class="m text-slate-200">' + esc(d.meta.period) + '</span></span>';
  h += '<span class="text-slate-600">&middot;</span>';
  h += '<span class="text-slate-400"><span class="m text-slate-200">' + esc(d.meta.timestamp) + '</span></span>';
  h += '</div></div>';

  // Form
  h += renderBacktestForm(d.form);

  // Config YAML
  if (d.config) h += renderConfigYAML(d.config);

  // Split banner
  if (d.split && d.split.hasSplit) h += renderSplitBanner(d.split);

  // WF banner + cards
  if (d.walkForward) {
    h += renderWalkForwardBanner(d.walkForward);
    h += renderWFSummaryCards(d.walkForward);
  }

  // Summary (if no WF)
  if (!d.walkForward && d.summary) {
    h += renderBacktestSummary(d.summary, !!(d.split && d.split.hasSplit));
  }

  // Results (if no WF)
  if (!d.walkForward && d.results && d.results.length > 0) {
    h += renderBacktestResults(d.results, !!(d.split && d.split.hasSplit));
  }

  // WF folds table
  if (d.walkForward) h += renderWFFolds(d.walkForward);

  // No results message
  if (!d.summary && !d.walkForward) {
    h += '<p class="text-slate-500 text-sm">No results. Submit the form above or visit /backtest without params to run with current config.</p>';
  }

  h += '</main>';
  return h;
}

// ---------------------------------------------------------------------------
// Main init: fetch, SSE, clock ticker
// ---------------------------------------------------------------------------

(function() {
  var app = document.getElementById('app');
  if (!app) return;

  var state = { tab: 'tab1', openDetails: {} };

  // Save UI state before re-rendering.
  function saveState() {
    // Save active tab
    var radios = app.querySelectorAll('input[name="tabs"]');
    for (var i = 0; i < radios.length; i++) {
      if (radios[i].checked) {
        state.tab = radios[i].id;
        break;
      }
    }
    // Save open <details> elements
    state.openDetails = {};
    var details = app.querySelectorAll('details[data-detail-id]');
    for (var i = 0; i < details.length; i++) {
      if (details[i].open) {
        state.openDetails[details[i].getAttribute('data-detail-id')] = true;
      }
    }
  }

  // Restore UI state after re-rendering.
  function restoreState() {
    // Restore active tab
    var radio = document.getElementById(state.tab);
    if (radio) radio.checked = true;
    // Restore open <details> elements
    var details = app.querySelectorAll('details[data-detail-id]');
    for (var i = 0; i < details.length; i++) {
      var id = details[i].getAttribute('data-detail-id');
      if (state.openDetails[id]) {
        details[i].open = true;
      }
    }
  }

  function applyData(resp) {
    saveState();
    if (resp.mode === 'single' && resp.single) {
      app.innerHTML = renderSinglePage(resp.single);
    } else if (resp.mode === 'multi' && resp.multi) {
      app.innerHTML = renderMultiPage(resp.multi);
    }
    restoreState();
    document.body.classList.add('ready');
  }

  var isBacktest = window.location.pathname === '/backtest';

  if (isBacktest) {
    // Backtest page: fetch with loading indicator, no SSE, no clock ticker.

    // Show a loading spinner while waiting for API response.
    function showLoading() {
      app.innerHTML = '<div style="display:flex;justify-content:center;align-items:center;min-height:60vh">' +
        '<div style="text-align:center">' +
        '<div class="spinner" style="width:32px;height:32px;border:3px solid var(--border);border-top-color:var(--accent,#38bdf8);border-radius:50%;animation:spin 0.8s linear infinite;margin:0 auto 12px"></div>' +
        '<div class="text-sm text-slate-400">Computing...</div>' +
        '</div></div>' +
        '<style>@keyframes spin{to{transform:rotate(360deg)}}</style>';
      document.body.classList.add('ready');
    }

    // Fetch backtest data and render page.
    function fetchBacktest(search) {
      showLoading();
      fetch('/api/backtest/data' + search)
        .then(function(r) { return r.json(); })
        .then(function(resp) {
          saveState();
          app.innerHTML = renderBacktestPage(resp);
          restoreState();
          // Intercept form submission to avoid full page reload.
          var form = document.getElementById('backtestForm');
          if (form) {
            form.addEventListener('submit', function(e) {
              e.preventDefault();
              var fd = new FormData(form);
              var params = [];
              fd.forEach(function(v, k) {
                if (v) params.push(encodeURIComponent(k) + '=' + encodeURIComponent(v));
              });
              var qs = params.length > 0 ? '?' + params.join('&') : '';
              history.pushState(null, '', '/backtest' + qs);
              fetchBacktest(qs);
            });
          }
        })
        .catch(function(err) {
          console.error('fetch error', err);
          app.innerHTML = '<div class="text-center text-red-400 py-20">Failed to load backtest data.</div>';
        });
    }

    // Handle browser back/forward navigation.
    window.addEventListener('popstate', function() {
      fetchBacktest(window.location.search);
    });

    fetchBacktest(window.location.search);
  } else {
    // Paper page: initial fetch + SSE + clock ticker
    fetch('/api/paper/data')
      .then(function(r) { return r.json(); })
      .then(applyData)
      .catch(function(err) { console.error('fetch error', err); });

    // SSE
    var es = new EventSource('/api/paper/stream');
    es.onmessage = function(e) {
      var resp = JSON.parse(e.data);
      applyData(resp);
    };
    es.onerror = function() {
      // Browser auto-reconnects EventSource on error.
    };

    // Client-side time updater: updates clock, start time and duration every second
    // so the backend only needs to push on actual data events.
    // All displayed times use the browser's local timezone.
    var startTs = parseInt(app.dataset.startTs, 10);
    var serverTZ = app.dataset.serverTz || '';

    function pad(n) { return n < 10 ? '0' + n : '' + n; }

    function fmtClock(d) {
      return d.getFullYear() + '-' + pad(d.getMonth() + 1) + '-' + pad(d.getDate()) + ' ' +
             pad(d.getHours()) + ':' + pad(d.getMinutes()) + ':' + pad(d.getSeconds());
    }

    function browserTZ() {
      var off = -new Date().getTimezoneOffset();
      if (off === 0) return 'UTC';
      var sign = off > 0 ? '+' : '-';
      var abs = Math.abs(off);
      var hr = Math.floor(abs / 60);
      var mn = abs % 60;
      return mn ? 'UTC' + sign + hr + ':' + pad(mn) : 'UTC' + sign + hr;
    }

    function fmtDuration(ms) {
      var s = Math.floor(ms / 1000);
      if (s < 0) s = 0;
      var hr = Math.floor(s / 3600); s %= 3600;
      var mn = Math.floor(s / 60); s %= 60;
      if (hr > 0) return hr + 'h' + mn + 'm' + s + 's';
      if (mn > 0) return mn + 'm' + s + 's';
      return s + 's';
    }

    if (startTs) {
      var startDate = new Date(startTs * 1000);
      var startStr = fmtClock(startDate);
      var btz = browserTZ();
      var tzLabel = btz + (serverTZ && serverTZ !== btz ? ' \u00b7 Server ' + serverTZ : '');

      function tick() {
        var now = new Date();
        var elapsed = now.getTime() - startTs * 1000;

        var starts = document.querySelectorAll('[data-live="start"]');
        for (var i = 0; i < starts.length; i++) { starts[i].textContent = startStr; }

        var clocks = document.querySelectorAll('[data-live="clock"]');
        for (var i = 0; i < clocks.length; i++) { clocks[i].textContent = fmtClock(now); }

        var durations = document.querySelectorAll('[data-live="duration"]');
        for (var i = 0; i < durations.length; i++) { durations[i].textContent = fmtDuration(elapsed); }

        var tzs = document.querySelectorAll('[data-live="tz"]');
        for (var i = 0; i < tzs.length; i++) { tzs[i].textContent = tzLabel; }
      }

      tick();
      setInterval(tick, 1000);
    }
  }
})();
