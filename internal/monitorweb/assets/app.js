'use strict';
const $ = id => document.getElementById(id);
const reduceMotion = matchMedia('(prefers-reduced-motion: reduce)').matches;
const finePointer = matchMedia('(pointer: fine)').matches;
const PROVIDERS = {
  codex:{name:'Codex',color:'#5b8def',palette:['#5b8def','#38c6d9','#8fb4ff','#2fa3c7','#7c9cf0','#56d4c4']},
  claude_code:{name:'Claude',color:'#e07b53',palette:['#f2a93b','#ec6f5a','#f6c97a','#e3875f','#d9a066','#f4b183']},
};
const NEUTRAL = ['#a78bfa','#4ade80','#f472b6','#facc15'];
const TYPES = [['input','Fresh input','#4ade80'],['cache_read','Cache read','#8b7cf6'],['cache_write','Cache write','#c4b5fd'],['output','Output','#f472b6']];
const RANGES = {'1h':'last hour','6h':'last 6 hours','24h':'last 24 hours','7d':'last 7 days','30d':'last 30 days'};
const MON = ['Jan','Feb','Mar','Apr','May','Jun','Jul','Aug','Sep','Oct','Nov','Dec'];
const DAY = ['Sun','Mon','Tue','Wed','Thu','Fri','Sat'];
const FONT = '"Inter Web","Inter Variable",Inter,system-ui,sans-serif';
const formats = new Map();
const nf = digits => formats.get(digits) || formats.set(digits, new Intl.NumberFormat('en',{maximumFractionDigits:digits})).get(digits);
const pad = n => String(n).padStart(2,'0');
const key = p => p.provider + '|' + p.model;
const sum = (rows, field) => rows.reduce((total, row) => total + row[field], 0);
const percent = value => value > 0 && value < 0.1 ? '<0.1%' : nf(1).format(value) + '%';

// Splits a token count into a short number and a unit: 1.03 · B.
function parts(value) {
  const abs = Math.abs(value), short = x => nf(x < 10 ? 2 : x < 100 ? 1 : 0).format(x);
  if (abs >= 1e9) return [short(value/1e9),'B'];
  if (abs >= 1e6) return [short(value/1e6),'M'];
  if (abs >= 1e3) return [short(value/1e3),'K'];
  return [nf(0).format(value),''];
}
const short = value => parts(value).join('');

const state = {range:'24h', provider:'all', model:'all'};
const charts = new Map(), colors = new Map();
let snapshot = null, connected = false, busy = false, revision = 0, mobile = innerWidth < 760, zoomed = false;
let expanded = null, opener = null, cardPinned = false;

function readURL() {
  const query = new URLSearchParams(location.search);
  if (RANGES[query.get('range')]) state.range = query.get('range');
  if (PROVIDERS[query.get('provider')]) state.provider = query.get('provider');
  if (query.get('model')) state.model = query.get('model');
}
function writeURL() {
  const query = new URLSearchParams();
  if (state.range !== '24h') query.set('range', state.range);
  if (state.provider !== 'all') query.set('provider', state.provider);
  if (state.model !== 'all') query.set('model', state.model);
  const search = query.toString();
  history.replaceState(null, '', search ? '?' + search : location.pathname);
}

function element(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}
function swatch(color) {
  const node = element('span','sw');
  node.style.background = color;
  return node;
}

// Colors stay fixed per model for the whole session: Codex models in blues, Claude in warm tones.
// New models take the first free color of their provider, in name order.
function assignColors(models) {
  for (const model of [...models].sort((a,b) => a.key.localeCompare(b.key))) {
    if (colors.has(model.key)) continue;
    const palette = PROVIDERS[model.provider]?.palette || NEUTRAL;
    const used = new Set([...colors].filter(([id]) => id.startsWith(model.provider + '|')).map(([, color]) => color));
    colors.set(model.key, palette.find(color => !used.has(color)) || palette[used.size % palette.length]);
  }
}
const colorOf = id => colors.get(id) || NEUTRAL[0];

function totals(points) {
  const models = new Map();
  for (const point of points) {
    const id = key(point);
    if (!models.has(id)) models.set(id,{key:id,name:point.model,provider:point.provider,input:0,output:0,cache_read:0,cache_write:0,total:0});
    const total = models.get(id);
    for (const field of ['input','output','cache_read','cache_write','total']) total[field] += point[field];
  }
  return [...models.values()].sort((a,b) => b.total - a.total);
}

function connection(ok, message) {
  connected = ok;
  $('connection').className = `live ${ok ? 'ok' : 'offline'}`;
  $('connection-label').textContent = ok ? 'Live' : 'Offline';
  $('connection').setAttribute('aria-label', ok ? 'Live' : 'Offline');
  $('notice').hidden = !message;
  $('notice').textContent = message || '';
  document.body.classList.toggle('stale', !ok && snapshot !== null);
}
const age = value => {
  if (!value) return 'no data';
  const seconds = Math.max(0,(Date.now()-Date.parse(value))/1000);
  return seconds < 60 ? `${Math.floor(seconds)}s ago` : seconds < 3600 ? `${Math.floor(seconds/60)} min ago` : `${Math.floor(seconds/3600)} h ago`;
};
function quotaReset(value) {
  if (!value) return 'No reset time';
  const minutes = Math.ceil((Date.parse(value)-Date.now())/60000);
  if (minutes <= 0) return 'Waiting for refresh';
  return minutes >= 1440 ? `Resets in ${Math.floor(minutes/1440)}d ${Math.floor(minutes/60)%24}h` : `Resets in ${Math.floor(minutes/60)}h ${minutes%60}m`;
}

// ---------- Chart plumbing ----------
const TOOLTIP = {renderMode:'html',confine:true,backgroundColor:'rgba(22,23,27,.97)',borderColor:'#2a2d34',borderWidth:1,padding:[10,12],
  extraCssText:'border-radius:12px;box-shadow:0 18px 40px rgba(0,0,0,.55);',textStyle:{color:'#edeef0',fontFamily:FONT}};
const base = () => ({backgroundColor:'transparent',textStyle:{fontFamily:FONT},animation:!reduceMotion,animationDuration:800,animationEasing:'cubicOut',animationDurationUpdate:400,animationEasingUpdate:'cubicOut'});
const legend = extra => ({type:'plain',bottom:mobile?8:12,left:'center',width:'92%',icon:'circle',itemWidth:10,itemHeight:10,itemGap:mobile?12:18,
  textStyle:{color:'#b4b8bf',fontSize:mobile?12:13,fontFamily:FONT},inactiveColor:'#3a3d44',...extra});
function tipRow(color, name, value, className) {
  const row = element('div', 'tt-r' + (className ? ' ' + className : ''));
  row.append(color ? swatch(color) : element('span'), element('span','',name), element('b','',value));
  return row;
}
function tip(title, rows) {
  const node = element('div','tt');
  node.append(element('div','tt-h',title), ...rows);
  return node;
}
function bucketTitle(ms) {
  const from = new Date(ms), to = new Date(ms + snapshot.analytics.bucket_seconds*1000);
  return `${DAY[from.getDay()]}, ${MON[from.getMonth()]} ${from.getDate()} · ${pad(from.getHours())}:${pad(from.getMinutes())}–${pad(to.getHours())}:${pad(to.getMinutes())}`;
}
function emptyText(empty) {
  return {id:'empty',type:'text',left:'center',top:'42%',invisible:!empty,silent:true,style:{text:'No usage recorded in this period',fill:'#6b717b',font:`13px ${FONT}`}};
}
function timeBase(times, empty, noTotal) {
  return {...base(),
    grid:{left:mobile?50:58,right:mobile?14:22,top:26,bottom:mobile?118:84},
    legend:legend(),
    // The drag-to-zoom brush only works when the toolbox renders, so it sits off-canvas.
    toolbox:{show:true,left:-200,top:-200,itemSize:1,showTitle:false,feature:{dataZoom:{yAxisIndex:'none',filterMode:'none',brushStyle:{color:'rgba(109,156,245,.14)',borderColor:'rgba(109,156,245,.55)',borderWidth:1}}}},
    graphic:{elements:[emptyText(empty)]},
    tooltip:{...TOOLTIP,trigger:'axis',axisPointer:{type:'line',lineStyle:{color:'#4a4e57',type:'dashed'}},
      formatter:params => {
        const list = Array.isArray(params) ? params : [params];
        const rows = list.filter(p => p.value[1] > 0).sort((a,b) => b.value[1]-a.value[1]);
        if (!rows.length) return tip(bucketTitle(list[0].value[0]), [element('span','tt-empty','No usage')]);
        const nodes = rows.map(p => tipRow(p.color, p.seriesName, short(p.value[1])));
        if (rows.length > 1 && !noTotal) nodes.push(element('div','tt-sep'), tipRow(null,'Total',short(rows.reduce((n,p) => n+p.value[1],0)),'total'));
        return tip(bucketTitle(list[0].value[0]), nodes);
      }},
    xAxis:{type:'time',min:times[0]*1000,max:Date.parse(snapshot.analytics.until),
      axisLine:{lineStyle:{color:'#26282e'}},axisTick:{show:false},splitLine:{show:true,lineStyle:{color:'#17181c'}},
      axisLabel:{hideOverlap:true,margin:12,
        formatter:value => {
          const date = new Date(value), time = `${pad(date.getHours())}:${pad(date.getMinutes())}`;
          return date.getHours()===0 && date.getMinutes()===0 ? `{t|${time}}\n{d|${MON[date.getMonth()]} ${date.getDate()}}` : `{t|${time}}`;
        },
        rich:{t:{color:'#9aa0a9',fontSize:12,lineHeight:18,fontFamily:FONT},d:{color:'#6b717b',fontSize:11,lineHeight:16,fontFamily:FONT}}}},
    yAxis:{type:'value',min:0,axisLabel:{color:'#7d838c',fontSize:12,formatter:short,fontFamily:FONT},splitLine:{lineStyle:{color:'#1b1d22',type:'dashed'}}}};
}
function chart(id) {
  let instance = charts.get(id);
  if (instance) return instance;
  instance = echarts.init($(id), null, {renderer:'canvas'});
  charts.set(id, instance);
  if (id === 'c-types' || id === 'c-models' || id === 'c-rate') {
    instance.group = 'time';
    instance.on('datazoom', event => {
      const zoom = event.batch?.[0] || event;
      zoomed = zoom.startValue !== undefined || !(zoom.start === 0 && zoom.end === 100);
      $('zoom-hint').textContent = zoomed ? 'Double-click a chart to see the whole period' : 'Drag across a time chart to zoom in · double-click to reset';
    });
    instance.getZr().on('dblclick', () => { if (zoomed) instance.dispatchAction({type:'dataZoom',start:0,end:100}); });
  }
  return instance;
}
function put(id, option) {
  const instance = chart(id);
  instance.setOption(option, {replaceMerge:['series']});
  if (instance.group === 'time' && finePointer) instance.dispatchAction({type:'takeGlobalCursor',key:'dataZoomSelect',dataZoomSelectActive:true});
  if (expanded === id) renderExpanded();
  return instance;
}

// ---------- Quotas ----------
function renderQuotas() {
  const container = $('quotas');
  if (container.dataset.ids !== snapshot.providers.map(p => p.id).join()) {
    for (const [id, instance] of charts) if (id.startsWith('g-')) { instance.dispose(); charts.delete(id); }
    container.replaceChildren(...snapshot.providers.map(provider => {
      const card = element('button',`qcard ${provider.id}`); card.type = 'button'; card.dataset.provider = provider.id;
      const head = element('div','qhead'), name = element('span','qname');
      name.append(element('i'), document.createTextNode(provider.name));
      head.append(name, element('span','qmeta'));
      const body = element('div','qbody');
      provider.quotas.forEach((_, index) => {
        const cell = element('div'), gauge = element('div','gauge'), reset = element('div','qreset');
        gauge.id = `g-${provider.id}-${index}`;
        reset.innerHTML = '<svg class="i" viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="10"/><path d="M12 6v6l4 2"/></svg><span></span>';
        cell.append(gauge, reset); body.append(cell);
      });
      card.append(head, body);
      card.addEventListener('click', () => setProvider(state.provider === provider.id ? 'all' : provider.id));
      return card;
    }));
    container.dataset.ids = snapshot.providers.map(p => p.id).join();
  }
  for (const provider of snapshot.providers) {
    const card = container.querySelector(`[data-provider="${provider.id}"]`), meta = card.querySelector('.qmeta');
    const bad = provider.status === 'AUTH_REQUIRED' || provider.status === 'ERROR';
    meta.textContent = provider.status === 'AUTH_REQUIRED' ? 'Sign in required' : provider.status === 'ERROR' ? (provider.detail === 'rate_limited' ? 'Rate limited · retrying' : 'Quota unavailable') : `Measured ${age(provider.updated_at)}`;
    meta.classList.toggle('bad', bad);
    card.setAttribute('aria-pressed', String(state.provider === provider.id));
    card.setAttribute('aria-label', `${provider.name}: ${provider.quotas.map(q => `${q.label} ${q.used_percent === null ? 'no data' : Math.round(q.used_percent) + '% used'}`).join(', ')}. Press to filter.`);
    provider.quotas.forEach((quota, index) => {
      card.querySelectorAll('.qreset span')[index].textContent = quotaReset(quota.resets_at);
      const value = quota.used_percent, color = value >= 95 ? '#f27474' : value >= 80 ? '#f5c451' : PROVIDERS[provider.id]?.color || NEUTRAL[0];
      put(`g-${provider.id}-${index}`, {...base(),series:[{id:'q',type:'gauge',startAngle:210,endAngle:-30,min:0,max:100,radius:mobile?'92%':'88%',center:['50%','58%'],silent:true,
        progress:{show:value !== null && value > 0.5,width:mobile?9:11,roundCap:true,itemStyle:{color:new echarts.graphic.LinearGradient(0,0,1,0,[{offset:0,color:color+'99'},{offset:1,color}]),shadowBlur:12,shadowColor:color+'55'}},
        axisLine:{roundCap:true,lineStyle:{width:mobile?9:11,color:[[1,'#1d1f25']]}},pointer:{show:false},axisTick:{show:false},splitLine:{show:false},axisLabel:{show:false},anchor:{show:false},
        title:{offsetCenter:[0,'44%'],color:'#8d929b',fontSize:mobile?12:13,fontFamily:FONT},
        detail:{offsetCenter:[0,'-4%'],valueAnimation:!reduceMotion,formatter:() => value === null ? '{v|—}' : `{v|${Math.round(value)}}{u|%}`,
          rich:{v:{fontSize:mobile?26:30,fontWeight:700,color:'#f3f4f6',fontFamily:FONT},u:{fontSize:mobile?13:15,color:'#9aa0a9',padding:[0,0,4,2],fontFamily:FONT}}},
        data:[{value:value ?? 0,name:quota.label}]}]});
    });
  }
}

// ---------- Donut ----------
function donutGeometry() {
  const node = $('c-donut'), width = node.clientWidth, height = node.clientHeight, half = Math.min(width, height)/2;
  return {cx:width/2, cy:height*(mobile ? .38 : .45), half, top:node.offsetTop, left:node.offsetLeft};
}
function renderDonut(models, points) {
  const geometry = donutGeometry(), selected = models.find(m => m.key === state.model);
  const value = selected ? selected.total : sum(points,'total');
  const [number, unit] = parts(value);
  const empty = models.length === 0;
  const names = models.map(m => m.name);
  const instance = put('c-donut', {...base(),
    tooltip:{...TOOLTIP,trigger:'item',formatter:params => {
      if (params.seriesId === 'types') return tip(params.name, [tipRow(params.color,'Tokens',short(params.value)), tipRow(null,'Share',percent(params.percent),'total')]);
      const model = models.find(m => m.key === params.data.key);
      return tip(`${PROVIDERS[model.provider]?.name || model.provider} · ${percent(params.percent)} of total`,
        [tipRow(params.color, model.name, short(model.total)), element('div','tt-sep'), ...TYPES.map(([field,name,color]) => tipRow(color,name,short(model[field])))]);
    }},
    legend:legend({data:names,selected:Object.fromEntries(names.map(n => [n,true])),
      formatter:name => mobile ? `${name}  ${short(models.find(m => m.name === name)?.total || 0)}` : name}),
    graphic:{elements:[
      {id:'center',type:'text',x:geometry.cx,y:geometry.cy,silent:true,invisible:empty,$action:'replace',
        style:{text:`{n|${number}}{u|${unit ? ' ' + unit : ''}}\n{s|${selected ? selected.name : 'total tokens'}}`,align:'center',verticalAlign:'middle',
          rich:{n:{fontSize:mobile?32:42,fontWeight:700,fill:'#f3f4f6',fontFamily:FONT},u:{fontSize:mobile?15:18,fontWeight:500,fill:'#9aa0a9',padding:[0,0,mobile?4:6,2],fontFamily:FONT},
            s:{fontSize:13,fill:'#7d838c',padding:[6,0,0,0],fontFamily:FONT,width:geometry.half*.7,overflow:'truncate'}}}},
      emptyText(empty)]},
    series:[
      {id:'models',type:'pie',name:'Models',radius:mobile?['50%','68%']:['52%','69%'],center:['50%',mobile?'38%':'45%'],padAngle:models.length > 1 ? 2 : 0,minAngle:4,
        selectedMode:'single',selectedOffset:12,avoidLabelOverlap:true,itemStyle:{borderRadius:6,borderColor:'#111215',borderWidth:2},
        label:mobile ? {show:false} : {show:true,position:'outside',formatter:p => `{n|${p.name}}\n{v|${short(p.value)}}  {p|${percent(p.percent)}}`,
          rich:{n:{fontSize:13,color:'#c9ccd2',lineHeight:20,fontFamily:FONT},v:{fontSize:14,fontWeight:700,color:'#f3f4f6',fontFamily:FONT},p:{fontSize:12,color:'#7d838c',fontFamily:FONT}}},
        labelLine:{length:16,length2:22,lineStyle:{color:'#4a4e57',width:1}},labelLayout:{moveOverlap:'shiftY'},emphasis:{scale:true,scaleSize:6},
        data:models.map(m => ({name:m.name,value:m.total,key:m.key,selected:m.key === state.model,itemStyle:{color:colorOf(m.key),opacity:state.model !== 'all' && m.key !== state.model ? .22 : 1}}))},
      {id:'types',type:'pie',name:'Token types',radius:mobile?['42%','45%']:['44%','47%'],center:['50%',mobile?'38%':'45%'],padAngle:1.5,minAngle:3,label:{show:false},labelLine:{show:false},
        itemStyle:{borderRadius:3,borderColor:'#111215',borderWidth:1},emphasis:{scaleSize:3},
        data:empty ? [] : TYPES.map(([field,name,color]) => ({name,value:selected ? selected[field] : sum(points,field),itemStyle:{color}}))}
    ]});
  const center = $('donut-center'), radius = geometry.half * (mobile ? .4 : .42);
  center.hidden = empty;
  center.style.width = center.style.height = radius*2 + 'px';
  center.style.left = geometry.left + geometry.cx - radius + 'px';
  center.style.top = geometry.top + geometry.cy - radius + 'px';
  if (!$('breakdown').hidden) renderBreakdown();
  return instance;
}
function renderBreakdown() {
  const points = currentPoints(), selected = state.model !== 'all' ? totals(points)[0] : null;
  const values = Object.fromEntries(TYPES.map(([field]) => [field, sum(points, field)]));
  const total = sum(points,'total') || 1;
  const card = $('breakdown'), title = element('div','hc-title'), amount = element('div','hc-total num',nf(0).format(sum(points,'total'))), bar = element('div','hc-bar');
  title.append(element('span','',selected ? selected.name : state.provider !== 'all' ? `${PROVIDERS[state.provider].name} usage` : 'Total usage'), element('span','',RANGES[state.range]));
  amount.append(element('span','','tokens'));
  const rows = TYPES.map(([field,name,color]) => {
    if (values[field] > 0) { const piece = element('i'); piece.style.flex = String(values[field]); piece.style.background = color; bar.append(piece); }
    const row = element('div','hc-row');
    row.append(swatch(color), element('span','',name), element('b','num',short(values[field])), element('em','num',percent(values[field]/total*100)));
    return row;
  });
  card.replaceChildren(title, amount, bar, ...rows);
}
function showBreakdown(pin) {
  if (pin !== undefined) cardPinned = pin;
  renderBreakdown();
  $('breakdown').hidden = false;
  $('donut-center').setAttribute('aria-expanded','true');
}
function hideBreakdown() {
  cardPinned = false;
  $('breakdown').hidden = true;
  $('donut-center').setAttribute('aria-expanded','false');
}

// ---------- Time charts ----------
function renderTimeCharts(points, times) {
  const step = snapshot.analytics.bucket_seconds, empty = points.length === 0;
  const byBucket = new Map();
  for (const point of points) (byBucket.get(point.at) || byBucket.set(point.at, []).get(point.at)).push(point);
  const bucket = (at, value) => (byBucket.get(at) || []).reduce((n, p) => n + value(p), 0);

  const types = timeBase(times, empty);
  types.series = TYPES.map(([field,name,color]) => ({id:field,name,type:'bar',stack:'tokens',barWidth:'58%',itemStyle:{color,borderRadius:[2,2,0,0]},emphasis:{focus:'series'},
    data:times.map(at => [at*1000, bucket(at, p => p[field])])}));
  put('c-types', types);

  const models = timeBase(times, empty);
  models.series = totals(points).map(model => ({id:model.key,name:model.name,type:'bar',stack:'models',barWidth:'58%',itemStyle:{color:colorOf(model.key),borderRadius:[2,2,0,0]},emphasis:{focus:'series'},
    data:times.map(at => [at*1000, bucket(at, p => key(p) === model.key ? p.total : 0)])}));
  put('c-models', models);

  // Output is ~100x smaller than input, so each line gets its own axis.
  const rate = timeBase(times, empty, true), lines = [['in','Input (incl. cache)','#5b8def',p => p.input+p.cache_read+p.cache_write],['out','Output','#f472b6',p => p.output]];
  rate.grid.right = mobile ? 46 : 58;
  rate.yAxis = lines.map(([,,color],index) => ({type:'value',min:0,position:index ? 'right' : 'left',axisLabel:{color,fontSize:12,formatter:short,fontFamily:FONT},splitLine:{show:!index,lineStyle:{color:'#1b1d22',type:'dashed'}}}));
  rate.series = lines.map(([id,name,color,value],index) => ({id,name,type:'line',yAxisIndex:index,smooth:.35,showSymbol:false,symbolSize:7,lineStyle:{width:2.2,color},itemStyle:{color},
    areaStyle:{color:new echarts.graphic.LinearGradient(0,0,0,1,[{offset:0,color:color+'55'},{offset:1,color:color+'00'}])},emphasis:{focus:'series'},
    data:times.map(at => [at*1000, Math.round(bucket(at, value)*60/step)])}));
  put('c-rate', rate);
}

// ---------- Filters and page state ----------
const providerPoints = () => snapshot.analytics.points.filter(p => state.provider === 'all' || p.provider === state.provider);
const currentPoints = () => providerPoints().filter(p => state.model === 'all' || key(p) === state.model);
function renderFilters(models) {
  // Models grouped by provider so the selected value shows only the model name.
  const select = $('model'), groups = new Map();
  for (const model of [...models].sort((a,b) => a.name.localeCompare(b.name))) (groups.get(model.provider) || groups.set(model.provider, []).get(model.provider)).push(model);
  const signature = [...groups].map(([provider, list]) => provider + ':' + list.map(m => m.key).join(',')).join(';');
  if (select.dataset.signature !== signature) {
    select.replaceChildren(new Option('All','all'), ...[...groups].map(([provider, list]) => {
      const group = document.createElement('optgroup');
      group.label = PROVIDERS[provider]?.name || provider;
      group.append(...list.map(m => new Option(m.name, m.key)));
      return group;
    }));
    select.dataset.signature = signature;
  }
  if (state.model !== 'all' && !models.some(m => m.key === state.model)) state.model = 'all';
  select.value = state.model;
  $('provider').value = state.provider;
  for (const [name, active] of [['provider', state.provider !== 'all'], ['model', state.model !== 'all']]) {
    $('var-' + name).classList.toggle('active', active);
    document.querySelector(`[data-clear="${name}"]`).hidden = !active;
  }
  $('model-dot').hidden = state.model === 'all';
  if (state.model !== 'all') $('model-dot').style.background = colorOf(state.model);
  for (const button of document.querySelectorAll('[data-range]')) button.setAttribute('aria-pressed', String(button.dataset.range === state.range));
}
function render() {
  if (!snapshot) return;
  const analytics = snapshot.analytics, step = analytics.bucket_seconds;
  assignColors(totals(analytics.points));
  const models = totals(providerPoints());
  renderFilters(models);
  const points = currentPoints();
  const times = [];
  for (let at = Math.floor(Date.parse(analytics.since)/1000/step)*step; at <= Date.parse(analytics.until)/1000; at += step) times.push(at);
  const date = value => { const d = new Date(value); return `${MON[d.getMonth()]} ${d.getDate()}, ${pad(d.getHours())}:${pad(d.getMinutes())}`; };
  $('range-dates').textContent = `${date(analytics.since)}  —  ${date(analytics.until)}`;
  const updated = new Date(snapshot.updated_at);
  $('updated').textContent = `Last read · ${pad(updated.getHours())}:${pad(updated.getMinutes())}:${pad(updated.getSeconds())}`;
  const count = totals(points).length, meta = $('group-meta'), detail = element('span','n',` · ${count} ${count === 1 ? 'model' : 'models'} · ${RANGES[state.range]}`);
  meta.replaceChildren(element('b','num',points.length ? short(sum(points,'total')) : '—'), document.createTextNode(' tokens'), detail);
  renderQuotas();
  if ($('grid').hidden) return;
  renderDonut(models, points);
  renderTimeCharts(points, times);
}
function setProvider(provider) {
  state.provider = provider;
  state.model = 'all';
  writeURL(); render();
}
function setModel(model) {
  state.model = model;
  writeURL(); render();
}
function clearFilters() {
  state.provider = 'all'; state.model = 'all';
  writeURL(); render();
}

async function refresh() {
  if (busy || document.hidden) return;
  busy = true; $('refresh').disabled = true; $('refresh').classList.add('spin');
  const requestRevision = revision;
  try {
    const response = await fetch('/api/dashboard?range=' + encodeURIComponent(state.range), {cache:'no-store',credentials:'same-origin',signal:AbortSignal.timeout(12000)});
    if (!response.ok) throw new Error('No disponible');
    const next = await response.json();
    if (requestRevision !== revision) return;
    snapshot = next;
    document.body.classList.remove('loading');
    connection(true, (snapshot.warnings || []).join(' '));
    render();
  } catch (error) {
    if (requestRevision !== revision) return;
    connection(false, snapshot ? 'Offline. Charts show the last reading received.' : 'Could not load the charts. Check the connection to the computer.');
  } finally {
    busy = false; $('refresh').disabled = false;
    setTimeout(() => $('refresh').classList.remove('spin'), 400);
    if (requestRevision !== revision) void refresh();
  }
}
function setRange(range) {
  if (range === state.range) return;
  state.range = range; writeURL();
  revision++; snapshot = null; zoomed = false;
  for (const [id, instance] of charts) if (!id.startsWith('g-')) instance.clear();
  for (const button of document.querySelectorAll('[data-range]')) button.setAttribute('aria-pressed', String(button.dataset.range === range));
  hideBreakdown();
  document.body.classList.add('loading');
  $('group-meta').textContent = ''; $('range-dates').textContent = '—';
  connection(false, 'Loading period…');
  void refresh();
}

// ---------- Expanded chart ----------
function renderExpanded() {
  const source = charts.get(expanded);
  if (!source) return;
  let instance = charts.get('expanded-chart');
  if (!instance) { instance = echarts.init($('expanded-chart')); charts.set('expanded-chart', instance); }
  const option = source.getOption();
  delete option.graphic;
  instance.setOption(option, {notMerge:true}); instance.resize();
}
function closeDialog() {
  $('chart-dialog').hidden = true; expanded = null; document.body.style.overflow = ''; opener?.focus();
}

// ---------- Events ----------
for (const button of document.querySelectorAll('[data-expand]')) button.addEventListener('click', () => {
  expanded = button.dataset.expand; opener = button;
  $('dialog-title').textContent = button.closest('.panel').querySelector('h2').textContent;
  $('chart-dialog').hidden = false; document.body.style.overflow = 'hidden';
  renderExpanded(); $('close-dialog').focus();
});
$('close-dialog').addEventListener('click', closeDialog);
for (const button of document.querySelectorAll('[data-range]')) button.addEventListener('click', () => setRange(button.dataset.range));
for (const button of document.querySelectorAll('[data-clear]')) button.addEventListener('click', event => {
  event.preventDefault();
  if (button.dataset.clear === 'provider') setProvider('all'); else setModel('all');
});
$('provider').addEventListener('change', () => setProvider($('provider').value));
$('model').addEventListener('change', () => setModel($('model').value));
$('refresh').addEventListener('click', refresh);
$('collapse').addEventListener('click', () => {
  const open = $('grid').hidden;
  $('grid').hidden = !open; $('zoom-hint').hidden = !open || !finePointer;
  $('collapse').setAttribute('aria-expanded', String(open));
  try { localStorage.setItem('am-collapsed', open ? '0' : '1'); } catch {}
  if (open) { render(); for (const instance of charts.values()) instance.resize(); }
});
const center = $('donut-center');
center.addEventListener('mouseenter', () => { if (finePointer) showBreakdown(); });
center.addEventListener('mouseleave', () => { if (!cardPinned) hideBreakdown(); });
center.addEventListener('focus', () => showBreakdown());
center.addEventListener('blur', () => { if (!cardPinned) hideBreakdown(); });
center.addEventListener('click', () => { if (cardPinned) hideBreakdown(); else showBreakdown(true); });
document.addEventListener('click', event => {
  if (!$('breakdown').hidden && !event.target.closest('#breakdown,#donut-center')) hideBreakdown();
});
document.addEventListener('keydown', event => {
  if (expanded) {
    if (event.key === 'Escape') closeDialog();
    if (event.key === 'Tab') { event.preventDefault(); $('close-dialog').focus(); }
    return;
  }
  if (event.key !== 'Escape') return;
  if (!$('breakdown').hidden) hideBreakdown();
  else if (state.provider !== 'all' || state.model !== 'all') clearFilters();
});
new ResizeObserver(() => {
  const narrow = innerWidth < 760;
  if (narrow !== mobile) { mobile = narrow; render(); }
  for (const [id, instance] of charts) if (id !== 'expanded-chart' || expanded) instance.resize();
  if (snapshot && !$('grid').hidden) {
    const geometry = donutGeometry(), radius = geometry.half * (mobile ? .4 : .42), node = $('donut-center');
    node.style.width = node.style.height = radius*2 + 'px';
    node.style.left = geometry.left + geometry.cx - radius + 'px';
    node.style.top = geometry.top + geometry.cy - radius + 'px';
    charts.get('c-donut')?.setOption({graphic:{elements:[{id:'center',x:geometry.cx,y:geometry.cy}]}});
  }
}).observe(document.body);
document.addEventListener('visibilitychange', () => { if (!document.hidden) { revision++; connection(false,'Updating…'); void refresh(); } });
addEventListener('online', refresh);
addEventListener('offline', () => connection(false,'Offline. Charts show the last reading received.'));

readURL();
try { if (localStorage.getItem('am-collapsed') === '1') { $('grid').hidden = true; $('collapse').setAttribute('aria-expanded','false'); } } catch {}
$('zoom-hint').hidden = $('grid').hidden || !finePointer;
for (const button of document.querySelectorAll('[data-range]')) button.setAttribute('aria-pressed', String(button.dataset.range === state.range));
if (typeof echarts === 'undefined') connection(false,'Could not load the chart engine. Reload the page.');
else {
  echarts.connect('time');
  chart('c-donut').on('click', params => { if (params.seriesId === 'models') setModel(state.model === params.data.key ? 'all' : params.data.key); });
  chart('c-donut').on('legendselectchanged', params => {
    const model = totals(providerPoints()).find(m => m.name === params.name);
    if (model) setModel(state.model === model.key ? 'all' : model.key);
  });
  chart('c-donut').on('mouseover', params => { if (params.seriesId === 'models') charts.get('c-models')?.dispatchAction({type:'highlight',seriesId:params.data.key}); });
  chart('c-donut').on('mouseout', params => { if (params.seriesId === 'models') charts.get('c-models')?.dispatchAction({type:'downplay',seriesId:params.data.key}); });
  setInterval(refresh, 5000);
  void refresh();
}
