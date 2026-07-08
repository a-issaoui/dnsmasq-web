/* dnsmasq-web console — vanilla JS, no dependencies.
 * One SSE stream drives all realtime updates; the directive registry from
 * /api/schema drives every form. */
(() => {
  'use strict';

  /* ── tiny DOM/util helpers ─────────────────────────────────────────── */
  const $ = (s, r) => (r || document).querySelector(s);
  const $$ = (s, r) => [...(r || document).querySelectorAll(s)];
  const esc = s => String(s ?? '').replace(/[&<>"']/g,
    c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  const debounce = (fn, ms) => { let t; return (...a) => { clearTimeout(t); t = setTimeout(() => fn(...a), ms); }; };

  function toast(msg, type = 'info', dur = 3800) {
    const icons = { success: '✓', error: '✕', warning: '⚠', info: 'ℹ' };
    const el = document.createElement('div');
    el.className = `toast toast-${type}`;
    el.innerHTML = `<span class="toast-icon">${icons[type] || 'ℹ'}</span><span>${esc(msg)}</span>`;
    $('#toasts').appendChild(el);
    requestAnimationFrame(() => el.classList.add('in'));
    setTimeout(() => { el.classList.add('out'); setTimeout(() => el.remove(), 350); }, dur);
  }

  async function api(method, url, body) {
    let r;
    try {
      r = await fetch(url, {
        method,
        headers: body !== undefined ? { 'Content-Type': 'application/json' } : undefined,
        body: body !== undefined ? JSON.stringify(body) : undefined,
      });
    } catch (e) {
      throw new Error('Network error — is dnsmasq-web still running?');
    }
    let data = null;
    try { data = await r.json(); } catch { /* empty body */ }
    if (!r.ok) {
      const err = new Error((data && data.error) || `Request failed (${r.status})`);
      err.status = r.status;
      throw err;
    }
    return data;
  }

  /* ── modal ─────────────────────────────────────────────────────────── */
  const modal = {
    open({ title, body, footer, wide }) {
      $('#modal-title').textContent = title;
      const mb = $('#modal-body'); mb.innerHTML = '';
      if (typeof body === 'string') mb.innerHTML = body; else mb.appendChild(body);
      const mf = $('#modal-footer'); mf.innerHTML = '';
      (footer || []).forEach(b => mf.appendChild(b));
      $('#modal .modal-box').classList.toggle('wide', !!wide);
      const m = $('#modal'); m.hidden = false;
      requestAnimationFrame(() => m.classList.add('open'));
      document.body.style.overflow = 'hidden';
      const first = mb.querySelector('input,select,textarea');
      if (first) setTimeout(() => first.focus(), 60);
    },
    close() {
      const m = $('#modal'); m.classList.remove('open');
      document.body.style.overflow = '';
      setTimeout(() => { m.hidden = true; }, 180);
    },
  };
  document.addEventListener('click', e => { if (e.target.closest('[data-close-modal]')) modal.close(); });
  document.addEventListener('keydown', e => { if (e.key === 'Escape' && !$('#modal').hidden) modal.close(); });

  function btn(label, cls, onClick) {
    const b = document.createElement('button');
    b.className = 'btn ' + cls; b.innerHTML = label;
    if (onClick) b.addEventListener('click', () => onClick(b));
    return b;
  }

  function confirmDialog(title, text, confirmLabel = 'Confirm', danger = true) {
    return new Promise(resolve => {
      const done = v => { modal.close(); resolve(v); };
      modal.open({
        title,
        body: `<p class="confirm-text">${esc(text)}</p>`,
        footer: [
          btn('Cancel', 'btn-ghost', () => done(false)),
          btn(confirmLabel, danger ? 'btn-danger' : 'btn-primary', () => done(true)),
        ],
      });
    });
  }

  async function guarded(b, fn) {
    const orig = b ? b.innerHTML : null;
    if (b) { b.disabled = true; b.innerHTML = '<span class="spinner"></span>'; }
    try { await fn(); }
    catch (e) { toast(e.message, e.status === 409 ? 'warning' : 'error', 6000); }
    finally { if (b) { b.disabled = false; b.innerHTML = orig; } }
  }

  /* ── state ─────────────────────────────────────────────────────────── */
  const S = {
    page: document.body.dataset.page || 'index',
    schema: null, dir: {}, cats: [],
    conf: null, status: null, leases: [],
    paused: false,
  };

  const confEntries = key => (S.conf ? S.conf.lines.filter(l => l.key === key) : []);
  const confScalar = key => { const e = confEntries(key); return e.length ? e[e.length - 1].value : null; };
  const confHas = key => confEntries(key).length > 0;

  async function loadSchema() {
    S.schema = await api('GET', '/api/schema');
    S.cats = S.schema.categories;
    S.dir = {};
    S.schema.directives.forEach(d => { S.dir[d.key] = d; });
  }
  async function loadConf() { S.conf = await api('GET', '/api/conf'); }
  async function loadLeases() { S.leases = await api('GET', '/api/dhcp/leases') || []; }

  /* ── value splitting that respects quotes ──────────────────────────── */
  function splitCSV(v) {
    const out = []; let cur = '', q = false;
    for (const ch of String(v || '')) {
      if (ch === '"') { q = !q; cur += ch; }
      else if (ch === ',' && !q) { out.push(cur); cur = ''; }
      else cur += ch;
    }
    out.push(cur);
    return out.map(s => s.trim());
  }
  const joinCSV = parts => parts.filter(p => p !== '' && p != null).join(',');

  const isIP4 = s => /^\d{1,3}(\.\d{1,3}){3}$/.test(s);
  const isIP6 = s => /^[0-9a-fA-F:]+$/.test(s) && s.includes(':') && !s.includes('.');
  const isMAC = s => /^([0-9a-fA-F]{2}[:-]|\*[:-]){5}([0-9a-fA-F]{2}|\*)$/.test(s) || /^([0-9a-fA-F*]{1,2}:){2,}[0-9a-fA-F*]{1,2}$/.test(s);
  const isLease = s => /^(\d+[smhdw]?|infinite|deprecated)$/.test(s);
  const V6MODES = ['ra-only', 'slaac', 'ra-names', 'ra-stateless', 'ra-advrouter', 'off-link'];
  const isMode = s => s === 'static' || s === 'proxy' || V6MODES.includes(s);

  /* ── directive composers ───────────────────────────────────────────── */
  /* Each composer: fields[] + parse(value)→obj + build(obj)→value.
   * `_extra` collects anything parse couldn't classify so a round-trip
   * never drops data. Unlisted keys get the generic single-value form. */
  const F = (name, label, ph = '', opts = {}) => ({ name, label, ph, ...opts });

  function splitDomainsValue(v) {
    // "/dom1/dom2/rest" → {domains:[...], rest}
    if (!v.startsWith('/')) return { domains: [], rest: v };
    const parts = v.split('/');
    // parts[0] === '', last part is rest (may be '')
    const rest = parts[parts.length - 1];
    return { domains: parts.slice(1, -1).filter(Boolean), rest };
  }

  function takeTags(parts, prefixes = ['tag:', 'set:']) {
    const tags = [], rest = [];
    for (const p of parts) {
      if (prefixes.some(pre => p.startsWith(pre))) tags.push(p); else rest.push(p);
    }
    return { tags, rest };
  }

  const COMPOSERS = {
    'server': {
      fields: [
        F('addr', 'Server address', '1.1.1.1  ·  9.9.9.9#5353', { width: 2 }),
        F('domains', 'Only for domains (optional)', 'corp.example.com', { help: 'Comma-separated. Makes this a conditional forwarder.' }),
      ],
      parse(v) {
        const d = splitDomainsValue(v);
        return { domains: d.domains.join(', '), addr: d.rest };
      },
      build(o) {
        const doms = splitCSV(o.domains).filter(Boolean);
        return (doms.length ? '/' + doms.join('/') + '/' : '') + (o.addr || '').trim();
      },
    },
    'local': {
      fields: [F('domains', 'Domains', 'home.lan', { help: 'Comma-separated list of local-only domains.' })],
      parse(v) { const d = splitDomainsValue(v); return { domains: d.domains.join(', ') }; },
      build(o) {
        const doms = splitCSV(o.domains).filter(Boolean);
        if (!doms.length) throw new Error('at least one domain is required');
        return '/' + doms.join('/') + '/';
      },
    },
    'address': {
      fields: [
        F('domains', 'Domains', 'ads.example.com', { width: 2, help: 'Comma-separated; matches the domains and every subdomain.' }),
        F('ip', 'Address', '0.0.0.0 (empty = NXDOMAIN)'),
      ],
      parse(v) { const d = splitDomainsValue(v); return { domains: d.domains.join(', '), ip: d.rest }; },
      build(o) {
        const doms = splitCSV(o.domains).filter(Boolean);
        if (!doms.length) throw new Error('at least one domain is required');
        return '/' + doms.join('/') + '/' + (o.ip || '').trim();
      },
    },
    'ipset': {
      fields: [
        F('domains', 'Domains', 'netflix.com', { width: 2 }),
        F('sets', 'Set name(s)', 'vpn_bypass'),
      ],
      parse(v) { const d = splitDomainsValue(v); return { domains: d.domains.join(', '), sets: d.rest }; },
      build(o) {
        const doms = splitCSV(o.domains).filter(Boolean);
        if (!doms.length || !(o.sets || '').trim()) throw new Error('domains and set name are required');
        return '/' + doms.join('/') + '/' + o.sets.trim();
      },
    },
    'nftset': {
      fields: [
        F('domains', 'Domains', 'example.com', { width: 2 }),
        F('sets', 'Set spec', '4#ip#filter#allowed'),
      ],
      parse(v) { const d = splitDomainsValue(v); return { domains: d.domains.join(', '), sets: d.rest }; },
      build(o) {
        const doms = splitCSV(o.domains).filter(Boolean);
        if (!doms.length || !(o.sets || '').trim()) throw new Error('domains and set spec are required');
        return '/' + doms.join('/') + '/' + o.sets.trim();
      },
    },
    'rebind-domain-ok': {
      fields: [F('domains', 'Domains', 'plex.direct')],
      parse(v) {
        const d = splitDomainsValue(v);
        return { domains: d.domains.length ? d.domains.join(', ') : v };
      },
      build(o) {
        const doms = splitCSV(o.domains).filter(Boolean);
        if (!doms.length) throw new Error('a domain is required');
        return '/' + doms.join('/') + '/';
      },
    },
    'host-record': {
      fields: [
        F('names', 'Name(s)', 'nas.home.lan', { width: 2, help: 'Comma-separated names sharing the addresses.' }),
        F('ip4', 'IPv4', '192.168.1.10'),
        F('ip6', 'IPv6', 'fd00::10'),
        F('ttl', 'TTL', '', { type: 'number' }),
      ],
      parse(v) {
        const parts = splitCSV(v), o = { names: [], ip4: '', ip6: '', ttl: '' };
        for (const p of parts) {
          if (isIP4(p)) o.ip4 = p;
          else if (isIP6(p)) o.ip6 = p;
          else if (/^\d+$/.test(p) && o.names.length) o.ttl = p;
          else if (p) o.names.push(p);
        }
        return { ...o, names: o.names.join(', ') };
      },
      build(o) {
        const names = splitCSV(o.names).filter(Boolean);
        if (!names.length) throw new Error('a name is required');
        if (!o.ip4 && !o.ip6) throw new Error('an IPv4 or IPv6 address is required');
        return joinCSV([...names, o.ip4, o.ip6, o.ttl]);
      },
    },
    'cname': {
      fields: [
        F('aliases', 'Alias(es)', 'www.home.lan', { width: 2 }),
        F('target', 'Target', 'nas.home.lan'),
        F('ttl', 'TTL', '', { type: 'number' }),
      ],
      parse(v) {
        const parts = splitCSV(v).filter(Boolean);
        let ttl = '';
        if (parts.length > 2 && /^\d+$/.test(parts[parts.length - 1])) ttl = parts.pop();
        const target = parts.pop() || '';
        return { aliases: parts.join(', '), target, ttl };
      },
      build(o) {
        const aliases = splitCSV(o.aliases).filter(Boolean);
        if (!aliases.length || !(o.target || '').trim()) throw new Error('alias and target are required');
        return joinCSV([...aliases, o.target.trim(), o.ttl]);
      },
    },
    'srv-host': {
      fields: [
        F('service', 'Service', '_ldap._tcp.example.com', { width: 2 }),
        F('target', 'Target host', 'ldap.example.com'),
        F('port', 'Port', '389', { type: 'number' }),
        F('priority', 'Priority', '', { type: 'number' }),
        F('weight', 'Weight', '', { type: 'number' }),
      ],
      parse(v) {
        const p = splitCSV(v);
        return { service: p[0] || '', target: p[1] || '', port: p[2] || '', priority: p[3] || '', weight: p[4] || '' };
      },
      build(o) {
        if (!(o.service || '').trim()) throw new Error('service is required');
        return joinCSV([o.service.trim(), o.target, o.port, o.priority, o.weight]);
      },
    },
    'txt-record': {
      fields: [
        F('name', 'Name', 'example.com'),
        F('text', 'Text', '"v=spf1 a -all"', { width: 2 }),
      ],
      parse(v) {
        const i = v.indexOf(',');
        return i < 0 ? { name: v, text: '' } : { name: v.slice(0, i).trim(), text: v.slice(i + 1).trim() };
      },
      build(o) {
        if (!(o.name || '').trim()) throw new Error('name is required');
        return o.text ? o.name.trim() + ',' + o.text : o.name.trim();
      },
    },
    'ptr-record': {
      fields: [F('name', 'PTR name', '10.1.168.192.in-addr.arpa', { width: 2 }), F('target', 'Target', 'nas.home.lan')],
      parse(v) { const p = splitCSV(v); return { name: p[0] || '', target: p[1] || '' }; },
      build(o) { if (!(o.name || '').trim()) throw new Error('name is required'); return joinCSV([o.name.trim(), o.target]); },
    },
    'mx-host': {
      fields: [
        F('name', 'MX domain', 'example.com'),
        F('host', 'Mail host', 'mail.example.com'),
        F('pref', 'Preference', '10', { type: 'number' }),
      ],
      parse(v) {
        const p = splitCSV(v);
        if (p.length === 2 && /^\d+$/.test(p[1])) return { name: p[0], host: '', pref: p[1] };
        return { name: p[0] || '', host: p[1] || '', pref: p[2] || '' };
      },
      build(o) { if (!(o.name || '').trim()) throw new Error('MX domain is required'); return joinCSV([o.name.trim(), o.host, o.pref]); },
    },
    'caa-record': {
      fields: [
        F('name', 'Name', 'example.com'),
        F('flags', 'Flags', '0', { type: 'number' }),
        F('tag', 'Tag', 'issue'),
        F('value', 'Value', 'letsencrypt.org'),
      ],
      parse(v) { const p = splitCSV(v); return { name: p[0] || '', flags: p[1] || '', tag: p[2] || '', value: p.slice(3).join(',') }; },
      build(o) {
        if (!(o.name || '').trim()) throw new Error('name is required');
        return joinCSV([o.name.trim(), o.flags, o.tag, o.value]);
      },
    },
    'dns-rr': {
      fields: [
        F('name', 'Name', 'example.com'),
        F('type', 'RR type number', '257', { type: 'number' }),
        F('hex', 'Hex data', '00 05 69 73 73 75 65', { width: 2 }),
      ],
      parse(v) { const p = splitCSV(v); return { name: p[0] || '', type: p[1] || '', hex: p.slice(2).join(',') }; },
      build(o) {
        if (!(o.name || '').trim() || !(o.type || '').trim()) throw new Error('name and type are required');
        return joinCSV([o.name.trim(), o.type, o.hex]);
      },
    },
    'interface-name': {
      fields: [F('name', 'DNS name', 'router.home.lan', { width: 2 }), F('iface', 'Interface', 'eth0')],
      parse(v) { const p = splitCSV(v); return { name: p[0] || '', iface: p[1] || '' }; },
      build(o) {
        if (!(o.name || '').trim() || !(o.iface || '').trim()) throw new Error('name and interface are required');
        return o.name.trim() + ',' + o.iface.trim();
      },
    },
    'trust-anchor': {
      fields: [
        F('domain', 'Domain', '.'),
        F('keytag', 'Key tag', '20326', { type: 'number' }),
        F('alg', 'Algorithm', '8', { type: 'number' }),
        F('digtype', 'Digest type', '2', { type: 'number' }),
        F('digest', 'Digest (hex)', 'E06D44B8…', { width: 3 }),
      ],
      parse(v) {
        let p = splitCSV(v);
        if (p.length >= 6) p = p.slice(1); // optional leading class
        return { domain: p[0] || '', keytag: p[1] || '', alg: p[2] || '', digtype: p[3] || '', digest: p[4] || '' };
      },
      build(o) {
        for (const k of ['domain', 'keytag', 'alg', 'digtype', 'digest'])
          if (!(o[k] || '').trim()) throw new Error('all trust-anchor fields are required');
        return joinCSV([o.domain.trim(), o.keytag, o.alg, o.digtype, o.digest.replace(/\s+/g, '')]);
      },
    },
    'rev-server': {
      fields: [F('subnet', 'Subnet', '192.168.1.0/24', { width: 2 }), F('server', 'Server', '192.168.1.1')],
      parse(v) { const p = splitCSV(v); return { subnet: p[0] || '', server: p.slice(1).join(',') }; },
      build(o) { if (!(o.subnet || '').trim()) throw new Error('subnet is required'); return joinCSV([o.subnet.trim(), o.server]); },
    },
    'bogus-nxdomain': {
      fields: [F('addr', 'Address[/prefix]', '64.94.110.11')],
      parse(v) { return { addr: v }; }, build(o) { if (!(o.addr || '').trim()) throw new Error('address required'); return o.addr.trim(); },
    },
    'ignore-address': {
      fields: [F('addr', 'Address[/prefix]', '192.0.2.1')],
      parse(v) { return { addr: v }; }, build(o) { if (!(o.addr || '').trim()) throw new Error('address required'); return o.addr.trim(); },
    },
    'domain': {
      fields: [
        F('domain', 'Domain', 'home.lan'),
        F('range', 'Subnet / range (optional)', '192.168.2.0/24', { width: 2 }),
        F('localflag', 'Also add local=/domain/', '', { type: 'checkbox' }),
      ],
      parse(v) {
        const p = splitCSV(v);
        return { domain: p[0] || '', range: p.slice(1).filter(x => x !== 'local').join(','), localflag: p.includes('local') };
      },
      build(o) {
        if (!(o.domain || '').trim()) throw new Error('domain is required');
        return joinCSV([o.domain.trim(), o.range, o.localflag ? 'local' : '']);
      },
    },
    'dhcp-range': {
      fields: [
        F('tags', 'Tags', 'tag:guest, set:lan', { width: 3, help: 'Optional tag:/set: qualifiers, comma-separated.' }),
        F('start', 'Start address', '192.168.1.100'),
        F('end', 'End / mode', '192.168.1.200  ·  static  ·  ra-names', { help: 'End address, or a mode: static, proxy, ra-only, slaac, ra-names, ra-stateless…' }),
        F('netmask', 'Netmask / prefix-len', '255.255.255.0'),
        F('broadcast', 'Broadcast', ''),
        F('lease', 'Lease time', '12h'),
      ],
      parse(v) {
        const { tags, rest } = takeTags(splitCSV(v));
        const o = { tags: tags.join(', '), start: '', end: '', netmask: '', broadcast: '', lease: '', _extra: [] };
        const modes = [], leftovers = [];
        for (const p of rest) {
          if (!p) continue;
          if (!o.start && (isIP4(p) || isIP6(p) || p.startsWith('constructor:'))) { o.start = p; continue; }
          if (isMode(p)) { modes.push(p); continue; }
          if (isLease(p) && !o.lease && (o.start || o.end)) {
            // careful: netmask/prefix-len is a bare number too for v6
            if (/^\d+$/.test(p) && Number(p) <= 128 && !o.netmask && (isIP6(o.start) || o.start.startsWith('constructor:'))) { o.netmask = p; continue; }
            o.lease = p; continue;
          }
          if (!o.end && (isIP4(p) || isIP6(p))) { o.end = p; continue; }
          if (!o.netmask && isIP4(p)) { o.netmask = p; continue; }
          if (!o.broadcast && isIP4(p)) { o.broadcast = p; continue; }
          leftovers.push(p);
        }
        if (modes.length) o.end = o.end ? o.end + ',' + modes.join(',') : modes.join(',');
        o._extra = leftovers;
        return o;
      },
      build(o) {
        if (!(o.start || '').trim()) throw new Error('start address is required');
        const tags = splitCSV(o.tags).filter(Boolean);
        for (const t of tags) if (!/^(tag|set):/.test(t)) throw new Error(`tag "${t}" must start with tag: or set:`);
        return joinCSV([...tags, o.start.trim(), o.end, o.netmask, o.broadcast, o.lease, ...(o._extra || [])]);
      },
    },
    'dhcp-host': {
      fields: [
        F('mac', 'MAC / client-id', 'aa:bb:cc:dd:ee:ff', { width: 2, help: 'One or more MACs (comma-separated), id:<client-id>, or id:* to match any.' }),
        F('ip', 'IP address', '192.168.1.50'),
        F('hostname', 'Hostname', 'nas'),
        F('lease', 'Lease time', 'infinite'),
        F('tags', 'Set tags', 'set:trusted', { help: 'set:<tag> to apply, tag:<tag> to match.' }),
        F('ignore', 'Ignore this host', '', { type: 'checkbox', help: 'Never offer this host a lease.' }),
      ],
      parse(v) {
        const parts = splitCSV(v);
        const o = { mac: [], ip: '', hostname: '', lease: '', tags: [], ignore: false, _extra: [] };
        for (const p of parts) {
          if (!p) continue;
          if (p === 'ignore') o.ignore = true;
          else if (p.startsWith('set:') || p.startsWith('tag:')) o.tags.push(p);
          else if (p.startsWith('id:')) o.mac.push(p);
          else if (isMAC(p)) o.mac.push(p);
          else if (isIP4(p) || /^\[.*\]$/.test(p)) o.ip = p;
          else if (isLease(p) && o.hostname) o.lease = p;
          else if (!o.hostname) o.hostname = p;
          else o._extra.push(p);
        }
        return { ...o, mac: o.mac.join(', '), tags: o.tags.join(', ') };
      },
      build(o) {
        const macs = splitCSV(o.mac).filter(Boolean);
        const tags = splitCSV(o.tags).filter(Boolean);
        if (!macs.length && !(o.hostname || '').trim()) throw new Error('a MAC/client-id or hostname is required');
        return joinCSV([...macs, ...tags, o.ip, o.hostname, o.lease, o.ignore ? 'ignore' : '', ...(o._extra || [])]);
      },
    },
    'dhcp-option': {
      fields: [
        F('tags', 'Tags', 'tag:guest', { help: 'Only send to clients matching all tags.' }),
        F('opt', 'Option', 'option:router  ·  6  ·  option6:dns-server', { width: 2 }),
        F('value', 'Value', '192.168.1.1', { width: 3 }),
      ],
      parse(v) {
        const parts = splitCSV(v);
        const o = { tags: [], opt: '', value: [], _pre: [] };
        for (const p of parts) {
          if (p.startsWith('tag:')) o.tags.push(p);
          else if (/^(encap|vi-encap|vendor):/.test(p) && !o.opt) o._pre.push(p);
          else if (!o.opt) o.opt = p;
          else o.value.push(p);
        }
        return { tags: o.tags.join(', '), opt: [...o._pre, o.opt].filter(Boolean).join(','), value: o.value.join(',') };
      },
      build(o) {
        if (!(o.opt || '').trim()) throw new Error('option number or name is required');
        const tags = splitCSV(o.tags).filter(Boolean);
        return joinCSV([...tags, o.opt.trim(), o.value]);
      },
    },
    'dhcp-boot': {
      fields: [
        F('tags', 'Tags', 'tag:efi-x86_64'),
        F('file', 'Boot file', 'pxelinux.0', { width: 2 }),
        F('server', 'Server name', 'boot-server'),
        F('addr', 'Server address', '192.168.1.5'),
      ],
      parse(v) {
        const { tags, rest } = takeTags(splitCSV(v), ['tag:']);
        return { tags: tags.join(', '), file: rest[0] || '', server: rest[1] || '', addr: rest.slice(2).join(',') };
      },
      build(o) {
        if (!(o.file || '').trim()) throw new Error('boot file is required');
        const tags = splitCSV(o.tags).filter(Boolean);
        return joinCSV([...tags, o.file.trim(), o.server, o.addr]);
      },
    },
    'pxe-prompt': {
      fields: [
        F('tags', 'Tags', ''),
        F('prompt', 'Prompt', '"Press F8 for boot menu"', { width: 2 }),
        F('timeout', 'Timeout (s)', '10', { type: 'number' }),
      ],
      parse(v) {
        const { tags, rest } = takeTags(splitCSV(v), ['tag:']);
        let timeout = '';
        if (rest.length > 1 && /^\d+$/.test(rest[rest.length - 1])) timeout = rest.pop();
        return { tags: tags.join(', '), prompt: rest.join(','), timeout };
      },
      build(o) {
        if (!(o.prompt || '').trim()) throw new Error('prompt is required');
        const tags = splitCSV(o.tags).filter(Boolean);
        return joinCSV([...tags, o.prompt.trim(), o.timeout]);
      },
    },
    'pxe-service': {
      fields: [
        F('tags', 'Tags', ''),
        F('csa', 'Client arch (CSA)', 'x86PC', { help: 'x86PC, X86-64_EFI, ARM64_EFI, BC_EFI…' }),
        F('menu', 'Menu text', '"Install Linux"', { width: 2 }),
        F('rest', 'Basename / type [, server]', 'pxelinux', { width: 2 }),
      ],
      parse(v) {
        const { tags, rest } = takeTags(splitCSV(v), ['tag:']);
        return { tags: tags.join(', '), csa: rest[0] || '', menu: rest[1] || '', rest: rest.slice(2).join(',') };
      },
      build(o) {
        if (!(o.csa || '').trim() || !(o.menu || '').trim()) throw new Error('client arch and menu text are required');
        const tags = splitCSV(o.tags).filter(Boolean);
        return joinCSV([...tags, o.csa.trim(), o.menu.trim(), o.rest]);
      },
    },
    'dhcp-relay': {
      fields: [
        F('local', 'Local address', '192.168.1.1'),
        F('server', 'Target server', '10.0.0.1'),
        F('iface', 'Interface', 'eth0'),
      ],
      parse(v) { const p = splitCSV(v); return { local: p[0] || '', server: p[1] || '', iface: p[2] || '' }; },
      build(o) {
        if (!(o.local || '').trim()) throw new Error('local address is required');
        return joinCSV([o.local.trim(), o.server, o.iface]);
      },
    },
    'ra-param': {
      fields: [
        F('iface', 'Interface', 'eth0'),
        F('rest', 'Parameters', 'high,60,1800', { width: 2, help: '[mtu:N,][high|low,]<interval>[,<lifetime>]' }),
      ],
      parse(v) { const p = splitCSV(v); return { iface: p[0] || '', rest: p.slice(1).join(',') }; },
      build(o) { if (!(o.iface || '').trim()) throw new Error('interface is required'); return joinCSV([o.iface.trim(), o.rest]); },
    },
    'tftp-root': {
      fields: [F('dir', 'Directory', '/srv/tftp', { width: 2 }), F('iface', 'Interface (optional)', '')],
      parse(v) { const p = splitCSV(v); return { dir: p[0] || '', iface: p[1] || '' }; },
      build(o) { if (!(o.dir || '').trim()) throw new Error('directory is required'); return joinCSV([o.dir.trim(), o.iface]); },
    },
    'conf-dir': {
      fields: [F('dir', 'Directory', '/etc/dnsmasq.d', { width: 2 }), F('ext', 'Extension filter', '*.conf')],
      parse(v) { const p = splitCSV(v); return { dir: p[0] || '', ext: p.slice(1).join(',') }; },
      build(o) { if (!(o.dir || '').trim()) throw new Error('directory is required'); return joinCSV([o.dir.trim(), o.ext]); },
    },
  };
  // simple "set:<tag>,value" composers
  for (const [key, valLabel, ph] of [
    ['dhcp-mac', 'MAC pattern', '00:11:22:*:*:*'],
    ['dhcp-vendorclass', 'Vendor class', 'android-dhcp'],
    ['dhcp-userclass', 'User class', 'kiosk'],
    ['dhcp-circuitid', 'Circuit ID', '00:01'],
    ['dhcp-remoteid', 'Remote ID', 'aa:bb'],
    ['dhcp-subscrid', 'Subscriber ID', 'customer-1'],
    ['dhcp-name-match', 'Name pattern', 'iphone*'],
    ['dhcp-match', 'Option match', 'option:client-arch,7'],
  ]) {
    COMPOSERS[key] = {
      fields: [F('tag', 'Set tag', 'set:mytag'), F('value', valLabel, ph, { width: 2 })],
      parse(v) {
        const p = splitCSV(v);
        return p[0] && p[0].startsWith('set:')
          ? { tag: p[0], value: p.slice(1).join(',') }
          : { tag: '', value: v };
      },
      build(o) {
        let tag = (o.tag || '').trim();
        if (!tag) throw new Error('a set: tag is required');
        if (!tag.startsWith('set:')) tag = 'set:' + tag;
        if (!(o.value || '').trim()) throw new Error('a value is required');
        return tag + ',' + o.value.trim();
      },
    };
  }
  for (const key of ['dhcp-ignore', 'dhcp-broadcast', 'dhcp-ignore-names']) {
    COMPOSERS[key] = {
      fields: [F('tags', 'Tags', 'tag:!known', { width: 2, help: 'tag:<name>, prefix with ! to negate.' })],
      parse(v) { return { tags: splitCSV(v).join(', ') }; },
      build(o) {
        const tags = splitCSV(o.tags).filter(Boolean);
        if (!tags.length && key === 'dhcp-ignore') throw new Error('at least one tag is required');
        for (const t of tags) if (!t.startsWith('tag:')) throw new Error(`"${t}" must start with tag:`);
        return tags.join(',');
      },
    };
  }

  function composerFor(key) {
    if (COMPOSERS[key]) return COMPOSERS[key];
    const d = S.dir[key] || {};
    return {
      fields: [F('value', 'Value', d.placeholder || '', { width: 3 })],
      parse: v => ({ value: v }),
      build: o => {
        if (!(o.value || '').trim()) throw new Error('a value is required');
        return o.value.trim();
      },
    };
  }

  /* ── directive section rendering ───────────────────────────────────── */
  function fieldInput(f, value) {
    if (f.type === 'checkbox') {
      return `<label class="check-inline field-check"><input type="checkbox" name="${f.name}" ${value ? 'checked' : ''}> ${esc(f.label)}</label>` +
        (f.help ? `<div class="form-hint">${esc(f.help)}</div>` : '');
    }
    return `<label class="form-label">${esc(f.label)}</label>
      <input class="form-input" name="${f.name}" type="text" value="${esc(value ?? '')}"
        placeholder="${esc(f.ph || '')}" autocomplete="off" spellcheck="false">` +
      (f.help ? `<div class="form-hint">${esc(f.help)}</div>` : '');
  }

  function openEntryModal(key, line, prefill) {
    const d = S.dir[key] || { label: key };
    const comp = composerFor(key);
    const obj = line ? comp.parse(line.value) : (prefill || {});
    const form = document.createElement('form');
    form.className = 'entry-form';
    form.innerHTML =
      (d.syntax ? `<div class="syntax-hint"><code>${esc(key)}=${esc(d.syntax)}</code></div>` : '') +
      (d.help ? `<p class="entry-help">${esc(d.help)}</p>` : '') +
      `<div class="entry-grid">` +
      comp.fields.map(f => `<div class="form-group w${f.width || 1}">${fieldInput(f, obj[f.name])}</div>`).join('') +
      `</div>`;
    if (obj._extra && obj._extra.length) {
      form.insertAdjacentHTML('beforeend',
        `<div class="form-hint">Preserved extra parameters: <code>${esc(obj._extra.join(','))}</code></div>`);
    }

    const collect = () => {
      const o = { _extra: obj._extra || [] };
      comp.fields.forEach(f => {
        const el = form.elements[f.name];
        o[f.name] = f.type === 'checkbox' ? el.checked : el.value;
      });
      return o;
    };
    const save = b => guarded(b, async () => {
      const value = comp.build(collect());
      if (line) {
        S.conf = await api('PUT', `/api/conf/lines/${line.idx}`, { key, value, expect_raw: line.raw });
      } else {
        S.conf = await api('POST', '/api/conf/lines', { key, value });
      }
      modal.close();
      toast(`${key} ${line ? 'updated' : 'added'}`, 'success');
      rerender();
    });
    form.addEventListener('submit', e => { e.preventDefault(); save(null); });

    modal.open({
      title: `${line ? 'Edit' : 'Add'} ${d.label || key}`,
      body: form, wide: comp.fields.length > 2,
      footer: [btn('Cancel', 'btn-ghost', modal.close), btn(line ? 'Save' : 'Add', 'btn-primary', save)],
    });
  }

  async function deleteEntry(line) {
    const ok = await confirmDialog('Delete entry',
      `Remove this line from the config?\n${line.raw}`, 'Delete');
    if (!ok) return;
    try {
      S.conf = await api('DELETE', `/api/conf/lines/${line.idx}?expect_raw=${encodeURIComponent(line.raw)}`);
      toast(`${line.key} removed`, 'success');
      rerender();
    } catch (e) { toast(e.message, 'error', 6000); }
  }

  function renderMultiCard(d) {
    const comp = composerFor(d.key);
    const entries = confEntries(d.key);
    const cols = comp.fields.filter(f => f.type !== 'checkbox');
    const card = document.createElement('div');
    card.className = 'card';
    card.dataset.key = d.key;
    card.innerHTML = `
      <div class="card-header">
        <div class="card-title">${esc(d.label)}<code class="key-chip">${esc(d.key)}</code></div>
        <div class="card-tools">
          ${entries.length ? `<span class="card-hint">${entries.length} ${entries.length === 1 ? 'entry' : 'entries'}</span>` : ''}
          <button class="btn btn-secondary btn-sm" data-add>+ Add</button>
        </div>
      </div>
      <div class="card-body p0"></div>`;
    const body = $('.card-body', card);
    if (!entries.length) {
      body.innerHTML = `<div class="empty small">
        <h3>Nothing configured</h3><p>${esc(d.help || '')}</p>
        <button class="btn btn-primary btn-sm" data-add>Add ${esc(d.label.toLowerCase())}</button></div>`;
    } else {
      const table = document.createElement('table');
      table.innerHTML = `<thead><tr>${cols.map(c => `<th>${esc(c.label)}</th>`).join('')}<th class="th-actions"></th></tr></thead>`;
      const tb = document.createElement('tbody');
      entries.forEach(line => {
        const obj = comp.parse(line.value);
        const tr = document.createElement('tr');
        tr.innerHTML = cols.map((c, i) => {
          let v = obj[c.name];
          if (v === true) v = 'yes'; if (v === false || v == null) v = '';
          const flagsHtml = i === 0
            ? comp.fields.filter(f => f.type === 'checkbox' && obj[f.name]).map(f => `<span class="badge badge-yellow">${esc(f.label)}</span>`).join(' ')
            : '';
          return `<td>${i === 0 ? '<strong>' : ''}<span class="mono-sm">${esc(v)}</span>${i === 0 ? '</strong>' : ''} ${flagsHtml}</td>`;
        }).join('') +
          `<td class="td-actions">
            <button class="btn btn-ghost btn-sm" data-edit title="Edit">Edit</button>
            <button class="btn btn-ghost btn-sm danger" data-del title="Delete">✕</button>
          </td>`;
        $('[data-edit]', tr).addEventListener('click', () => openEntryModal(d.key, line));
        $('[data-del]', tr).addEventListener('click', () => deleteEntry(line));
        tb.appendChild(tr);
      });
      table.appendChild(tb);
      const wrap = document.createElement('div'); wrap.className = 'tbl-wrap';
      wrap.appendChild(table); body.appendChild(wrap);
    }
    $$('[data-add]', card).forEach(b => b.addEventListener('click', () => openEntryModal(d.key)));
    return card;
  }

  function renderOptionsCard(flags, scalars) {
    const card = document.createElement('div');
    card.className = 'card';
    card.dataset.key = 'options';
    card.innerHTML = `<div class="card-header"><div class="card-title">Options</div>
      <span class="card-hint">applied instantly · restart to take effect</span></div>
      <div class="card-body"><div class="opt-grid"></div></div>`;
    const grid = $('.opt-grid', card);

    flags.forEach(d => {
      const on = confHas(d.key);
      const row = document.createElement('div');
      row.className = 'toggle-row';
      row.innerHTML = `
        <div class="toggle-info">
          <div class="toggle-name">${esc(d.label)} <code class="key-chip">${esc(d.key)}</code></div>
          <div class="toggle-desc">${esc(d.help)}</div>
        </div>
        <label class="toggle"><input type="checkbox" ${on ? 'checked' : ''}><span class="toggle-track"></span></label>`;
      const input = $('input', row);
      input.addEventListener('change', async () => {
        input.disabled = true;
        try {
          S.conf = await api('PUT', '/api/conf/flag', { key: d.key, on: input.checked });
          toast(`${d.key} ${input.checked ? 'enabled' : 'disabled'}`, 'success', 2200);
          rerender(true);
        } catch (e) {
          input.checked = !input.checked;
          toast(e.message, 'error', 6000);
        } finally { input.disabled = false; }
      });
      grid.appendChild(row);
    });

    scalars.forEach(d => {
      const val = confScalar(d.key) ?? '';
      const row = document.createElement('div');
      row.className = 'scalar-row';
      row.innerHTML = `
        <div class="toggle-info">
          <div class="toggle-name">${esc(d.label)} <code class="key-chip">${esc(d.key)}</code></div>
          <div class="toggle-desc">${esc(d.help)}</div>
        </div>
        <div class="scalar-input">
          <input class="form-input" type="text" value="${esc(val)}" placeholder="${esc(d.placeholder || 'not set')}"
            autocomplete="off" spellcheck="false">
        </div>`;
      const input = $('input', row);
      const commit = async () => {
        const nv = input.value.trim();
        if (nv === (confScalar(d.key) ?? '')) return;
        input.disabled = true;
        try {
          S.conf = await api('PUT', '/api/conf/scalar', { key: d.key, value: nv });
          toast(nv === '' ? `${d.key} removed` : `${d.key} = ${nv}`, 'success', 2200);
          rerender(true);
        } catch (e) {
          input.value = confScalar(d.key) ?? '';
          toast(e.message, 'error', 6000);
        } finally { input.disabled = false; }
      };
      input.addEventListener('change', commit);
      input.addEventListener('keydown', e => { if (e.key === 'Enter') { e.preventDefault(); input.blur(); } });
      grid.appendChild(row);
    });
    return card;
  }

  function renderSection(catId, container) {
    container.innerHTML = '';
    const dirs = S.schema.directives.filter(d => d.cat === catId);
    const flags = dirs.filter(d => d.kind === 'flag');
    const scalars = dirs.filter(d => d.kind === 'scalar');
    const multis = dirs.filter(d => d.kind === 'multi');

    multis.forEach(d => container.appendChild(renderMultiCard(d)));
    if (flags.length || scalars.length)
      container.appendChild(renderOptionsCard(flags, scalars));

    // Grid balancing: wide tables always span both columns, and if an odd
    // number of normal cards remains, the last one spans too — no ragged
    // half-empty rows anywhere.
    const WIDE = new Set(['dhcp-range', 'dhcp-host']);
    const cards = [...container.children].filter(el => el.classList.contains('card'));
    let normal = 0;
    cards.forEach(c => {
      if (WIDE.has(c.dataset.key)) c.classList.add('span2');
      else normal++;
    });
    if (normal % 2 === 1) {
      const last = [...cards].reverse().find(c => !c.classList.contains('span2'));
      if (last) last.classList.add('span2');
    }
  }

  function renderAllSections() {
    $$('[data-sec]').forEach(el => renderSection(el.dataset.sec, el));
  }

  /* ── status / chrome ───────────────────────────────────────────────── */
  function renderStatus() {
    const st = S.status;
    const sb = $('#sb-status'), tb = $('#tb-status'), bar = $('#apply-bar');
    if (!st) return;
    const running = st.running;
    if (sb) {
      $('.pulse', sb).className = 'pulse ' + (running ? 'green' : 'red');
      $('[data-status-text]', sb).textContent = running ? 'Running' : 'Stopped';
      const pid = $('[data-status-pid]', sb);
      pid.hidden = !st.pid; if (st.pid) pid.textContent = 'PID ' + st.pid;
    }
    if (tb) {
      tb.className = 'status-chip ' + (running ? 'green' : 'red');
      tb.innerHTML = `<span class="chip-dot"></span>${running ? 'Online' : 'Offline'}`;
    }
    if (bar) {
      if (!st.stale_config) {
        sessionStorage.removeItem('applyDismissed');
        cancelAutoApply();
      } else if (autoApplyOn()) {
        scheduleAutoApply();
      }
      bar.hidden = !st.stale_config || sessionStorage.getItem('applyDismissed') === '1';
      const msg = $('#apply-bar-msg');
      if (msg) msg.textContent = autoApplyPending
        ? 'Applying changes — restarting dnsmasq…'
        : 'Unapplied changes — dnsmasq reads its config only at startup.';
    }
    if (S.page === 'index') renderDashboard();
    if (S.page === 'settings') renderBootToggle();
  }

  function renderBadges() {
    if (!S.conf) return;
    const dnsCount = ['host-record', 'cname', 'address', 'srv-host', 'txt-record', 'ptr-record', 'mx-host', 'naptr-record', 'caa-record', 'dns-rr', 'interface-name']
      .reduce((n, k) => n + confEntries(k).length, 0);
    const set = (name, val, show) => {
      const el = $(`[data-badge="${name}"]`);
      if (!el) return;
      el.hidden = !show; el.textContent = val;
    };
    set('dns', dnsCount, dnsCount > 0);
    set('leases', S.leases.length, S.leases.length > 0);
    set('tftp', 'on', confHas('enable-tftp'));
  }

  /* ── auto-apply: restart dnsmasq automatically after the last change ── */
  let autoApplyTimer = null;
  let autoApplyPending = false;
  // default ON; the checkbox in the banner persists the choice
  const autoApplyOn = () => localStorage.getItem('autoApply') !== '0';

  function scheduleAutoApply() {
    if (!autoApplyOn() || autoApplyPending) return;
    clearTimeout(autoApplyTimer);
    // debounce: fires 2.5s after the *last* change, so a burst of edits
    // results in a single restart
    autoApplyTimer = setTimeout(async () => {
      autoApplyPending = true;
      renderStatus();
      try {
        await api('POST', '/api/service/restart');
        toast('Changes applied — dnsmasq restarted', 'success');
      } catch (e) {
        toast('Auto-apply failed: ' + e.message, 'error', 7000);
      } finally {
        autoApplyPending = false;
        autoApplyTimer = null;
      }
    }, 2500);
  }

  function cancelAutoApply() {
    clearTimeout(autoApplyTimer);
    autoApplyTimer = null;
  }

  function setLive(ok) {
    const el = $('#live-dot');
    if (el) el.classList.toggle('offline', !ok);
  }

  /* ── SSE ───────────────────────────────────────────────────────────── */
  let es = null;
  function connectSSE() {
    es = new EventSource('/api/events');
    es.onopen = () => setLive(true);
    es.onerror = () => setLive(false);
    es.addEventListener('status', e => { S.status = JSON.parse(e.data); renderStatus(); });
    es.addEventListener('leases', e => {
      S.leases = JSON.parse(e.data) || [];
      renderBadges();
      if (S.page === 'dhcp') renderLeaseTable();
      if (S.page === 'index') renderRecentLeases();
    });
    es.addEventListener('config', async e => {
      const { rev } = JSON.parse(e.data);
      if (autoApplyOn() && autoApplyTimer) scheduleAutoApply(); // push the debounce out
      if (S.conf && S.conf.rev === rev) return;
      try { await loadConf(); rerender(); } catch { }
    });
    es.addEventListener('log', e => {
      if (S.page !== 'logs' || S.paused) return;
      appendLogLine(JSON.parse(e.data).line);
    });
    es.addEventListener('query', e => {
      const ev = JSON.parse(e.data);
      if (S.page === 'logs' && !S.paused) appendQueryRow(ev);
      if (S.page === 'index') appendActivity(ev, 'dns');
    });
    es.addEventListener('dhcp', e => {
      const ev = JSON.parse(e.data);
      if (S.page === 'index') appendActivity(ev, 'dhcp');
    });
    es.addEventListener('mcp', e => {
      S.mcp = JSON.parse(e.data);
      if (S.page === 'mcp') renderMCP(S.mcp);
      if (S.page === 'index') renderMcpStat();
    });
  }

  /* ── rerender orchestration ────────────────────────────────────────── */
  let rerenderQueued = false;
  function rerender(skipOptions) {
    // Re-render everything conf-derived on the current page. `skipOptions`
    // avoids rebuilding the card that owns the input the user is typing in
    // — but sections are cheap, so full rebuild is fine except focus loss;
    // we simply skip rebuild while a section input has focus.
    if (skipOptions && document.activeElement && document.activeElement.matches('.opt-grid input')) {
      renderBadges();
      return;
    }
    if (rerenderQueued) return;
    rerenderQueued = true;
    requestAnimationFrame(() => {
      rerenderQueued = false;
      renderAllSections();
      renderBadges();
      if (S.page === 'index') renderDashboard();
      if (S.page === 'config') renderConfigPage();
      if (S.page === 'network') renderNetworkExtras();
      if (S.page === 'logs') renderQueryNote();
      if (S.page === 'dns') { renderEncDNS(); renderResolverCheck(); }
    });
  }

  /* ── generic tabs ──────────────────────────────────────────────────── */
  function initTabs() {
    const tabs = $('#tabs');
    if (!tabs) return;
    tabs.addEventListener('click', e => {
      const t = e.target.closest('.tab'); if (!t) return;
      $$('.tab', tabs).forEach(x => x.classList.toggle('active', x === t));
      $$('.tab-pane').forEach(p => p.classList.toggle('active', p.dataset.pane === t.dataset.tab));
      history.replaceState(null, '', '#' + t.dataset.tab);
    });
    const want = location.hash.slice(1);
    if (want) { const t = $(`.tab[data-tab="${CSS.escape(want)}"]`, tabs); if (t) t.click(); }
  }

  /* ── dashboard ─────────────────────────────────────────────────────── */
  function statSet(id, value, trend, tone) {
    const el = $('#' + id); if (!el) return;
    el.className = 'stat-card' + (tone ? ' ' + tone : '');
    $('.stat-value', el).classList.remove('sk');
    $('.stat-value', el).textContent = value;
    if (trend != null) $('.stat-trend', el).innerHTML = trend;
  }

  function renderDashboard() {
    const st = S.status || {};
    statSet('stat-service', st.running ? 'Up' : 'Down',
      st.running ? '↑ ' + (st.uptime || 'just started') : 'not running',
      st.running ? 'green' : 'red');
    statSet('stat-mem', st.memory_mb ? st.memory_mb.toFixed(1) + ' MB' : '—', null, 'orange');

    if (S.conf) {
      const dnsCount = ['host-record', 'cname', 'address', 'srv-host', 'txt-record', 'ptr-record', 'mx-host', 'naptr-record', 'caa-record', 'dns-rr', 'interface-name']
        .reduce((n, k) => n + confEntries(k).length, 0);
      // distinguish real upstreams from domain-scoped conditional forwarders
      const servers = confEntries('server');
      const globals = servers.filter(l => !l.value.startsWith('/'));
      const conds = servers.length - globals.length;
      const encrypted = globals.some(l => l.value === '127.0.0.1#5053');
      const condTxt = conds ? ` · ${conds} conditional` : '';
      const upTrend = encrypted
        ? 'encrypted upstream (DoH)' + condTxt
        : globals.length
          ? `${globals.length} upstream server${globals.length === 1 ? '' : 's'}` + condTxt
          : 'upstream via resolv.conf' + condTxt;
      statSet('stat-dns', dnsCount, upTrend, 'cyan');
      const ranges = confEntries('dhcp-range').length;
      const hosts = confEntries('dhcp-host').length;
      statSet('stat-dhcp', ranges ? 'On' : 'Off',
        ranges ? `${ranges} range${ranges === 1 ? '' : 's'} · ${hosts} static host${hosts === 1 ? '' : 's'}` : 'no dhcp-range configured',
        ranges ? 'green' : '');
      statSet('stat-leases', S.leases.length, `cache ${confScalar('cache-size') || '150'} entries`, 'purple');
      const feats = [];
      if (encrypted) feats.push('DoH');
      if (confHas('dnssec')) feats.push('DNSSEC');
      if (confHas('enable-tftp')) feats.push('TFTP');
      if (confHas('enable-ra')) feats.push('RA');
      if (confScalar('log-queries') !== null) feats.push('QueryLog');
      statSet('stat-extras', feats.length ? feats.join(' · ') : 'base',
        feats.length ? 'enabled features' : 'DNSSEC, TFTP, RA all off');
      $('#stat-extras .stat-value').classList.toggle('list', feats.length > 0);
    }

    // service table
    const tbl = $('#svc-table');
    if (tbl && S.conf) {
      const rows = [
        ['State', st.running ? `<span class="badge badge-green">running</span>` : `<span class="badge badge-red">${esc(st.status || 'stopped')}</span>`],
        ['Version', st.version ? 'dnsmasq ' + esc(st.version) : '—'],
        ['PID', st.pid ? `<code>${st.pid}</code>` : '—'],
        ['Uptime', st.uptime ? esc(st.uptime) : '—'],
        ['Enabled at boot', st.enabled ? '<span class="badge badge-green">yes</span>' : '<span class="badge badge-gray">no</span>'],
        ['DNS port', esc(confScalar('port') || '53')],
        ['Domain', esc(confScalar('domain') || '—')],
        ['Upstream', confEntries('server').map(l => `<code class="mr4">${esc(l.value)}</code>`).join('') || '<span class="dim">resolv.conf</span>'],
      ];
      tbl.innerHTML = rows.map(([k, v]) => `<tr><td class="kv-key">${k}</td><td>${v}</td></tr>`).join('');
    }
    // badge
    const badge = $('#svc-badge');
    if (badge) badge.innerHTML = st.running ? '<span class="badge badge-green">Active</span>' : '<span class="badge badge-red">Inactive</span>';
    renderSvcActions();
    renderRecentLeases();
    renderMcpStat();
  }

  function svcAction(action, label, confirmText) {
    return async b => {
      if (confirmText && !(await confirmDialog(label, confirmText, label, action === 'stop'))) return;
      await guarded(b, async () => {
        const d = await api('POST', '/api/service/' + action);
        toast(d.message || 'Done', 'success');
      });
    };
  }

  function renderSvcActions() {
    const box = $('#svc-actions'); if (!box) return;
    const running = S.status && S.status.running;
    box.innerHTML = '';
    if (!running) {
      box.appendChild(btn('▶ Start', 'btn-success', svcAction('start', 'Start service')));
    } else {
      box.appendChild(btn('⟳ Restart', 'btn-warning', svcAction('restart', 'Restart service', 'Restart dnsmasq? Clients will see a sub-second interruption.')));
      box.appendChild(btn('↻ Reload', 'btn-outline', svcAction('reload', 'Reload')));
      box.appendChild(btn('■ Stop', 'btn-danger', svcAction('stop', 'Stop service', 'Stop dnsmasq? DNS and DHCP will go down until restarted.')));
    }
  }

  function leaseRows(leases, limit) {
    const now = Date.now() / 1000;
    return leases.slice(0, limit || leases.length).map(l => {
      const remain = l.expiry_unix - now;
      const remainTxt = l.expiry_unix === 0 ? 'infinite'
        : remain <= 0 ? 'expired'
          : remain > 86400 ? Math.floor(remain / 86400) + 'd ' + Math.floor((remain % 86400) / 3600) + 'h'
            : remain > 3600 ? Math.floor(remain / 3600) + 'h ' + Math.floor((remain % 3600) / 60) + 'm'
              : Math.floor(remain / 60) + 'm';
      return { ...l, remainTxt };
    });
  }

  function renderRecentLeases() {
    const box = $('#recent-leases'); if (!box) return;
    if (!S.leases.length) {
      box.innerHTML = `<div class="empty small"><h3>No active leases</h3><p>Devices that get an address from DHCP appear here instantly.</p></div>`;
      return;
    }
    const rows = leaseRows([...S.leases].sort((a, b) => b.expiry_unix - a.expiry_unix), 8);
    box.innerHTML = `<div class="tbl-wrap"><table>
      <thead><tr><th>IP</th><th>Host</th><th>MAC</th><th>Remaining</th></tr></thead>
      <tbody>${rows.map(l => `<tr>
        <td class="mono-sm">${esc(l.ip_address)}</td>
        <td>${l.hostname ? '<strong>' + esc(l.hostname) + '</strong>' : '<span class="dim">—</span>'}</td>
        <td class="mono-sm dim">${esc(l.mac_address)}</td>
        <td><span class="badge badge-gray">${l.remainTxt}</span></td>
      </tr>`).join('')}</tbody></table></div>`;
  }

  /* Parse a journal line into a structured activity event (mirrors the
   * server-side parser; used to backfill feeds from journal history). */
  const reJournal = /^\d{4}-\d{2}-\d{2}T(\d{2}:\d{2}:\d{2})\S*\s+\S+\s+dnsmasq(?:-dhcp)?\[\d+\]:\s*(.*)$/;
  const reQuery = /^query\[([A-Z0-9]+)\]\s+(\S+)\s+from\s+(\S+)/;
  const reFwd = /^forwarded\s+(\S+)\s+to\s+(\S+)/;
  const reReply = /^(reply|cached|config|\/etc\/hosts|DHCP)\s+(\S+)\s+is\s+(.+)$/;
  const reDHCP = /^(DHCPDISCOVER|DHCPOFFER|DHCPREQUEST|DHCPACK|DHCPNAK|DHCPRELEASE|DHCPINFORM)\(([^)]+)\)\s*(\S*)\s*(\S*)\s*(.*)$/;

  function parseJournal(raw) {
    const m = raw.match(reJournal);
    if (!m) return null;
    const ts = m[1], msg = m[2];
    let x;
    if ((x = msg.match(reQuery))) return { stream: 'query', ts, kind: 'query', rtype: x[1], name: x[2], client: x[3] };
    if ((x = msg.match(reFwd))) return { stream: 'query', ts, kind: 'forwarded', name: x[1], value: x[2] };
    if ((x = msg.match(reReply))) {
      let kind = x[1] === '/etc/hosts' ? 'hosts' : x[1] === 'DHCP' ? 'dhcp-lease' : x[1];
      return { stream: 'query', ts, kind: kind.toLowerCase(), name: x[2], value: x[3] };
    }
    if ((x = msg.match(reDHCP))) {
      return { stream: 'dhcp', ts, kind: x[1].replace('DHCP', '').toLowerCase(), iface: x[2], value: x[3], mac: x[4], name: (x[5] || '').trim() };
    }
    return null;
  }

  // The empty state should tell the truth: log-queries off is one cause,
  // but "nothing points at dnsmasq" is the other common one.
  function updateActivityEmpty() {
    const empty = $('#activity-empty');
    if (!empty || !S.conf) return;
    if (activityCount > 0) { empty.remove(); return; }
    if (confScalar('log-queries') === null) {
      empty.innerHTML = `<h3>Query logging is off</h3>
        <p>Enable <b>log-queries</b> in <a href="/settings">Settings → Logging</a> to see the live DNS stream.</p>`;
    } else {
      empty.innerHTML = `<h3>Query logging is on — no DNS traffic yet</h3>
        <p>dnsmasq isn't receiving queries. Point clients (or this machine's resolver) at it,
        or try the <b>query tester</b> above — its lookups appear here instantly.</p>`;
    }
  }

  const activityIcons = { query: '?', forwarded: '→', reply: '✓', cached: '⚡', config: '⚙', hosts: '≡', 'dhcp-lease': '⌂' };
  let activityCount = 0;
  function appendActivity(ev, kind) {
    const feed = $('#activity-feed'); if (!feed) return;
    const empty = $('#activity-empty'); if (empty) empty.remove();
    const row = document.createElement('div');
    row.className = 'act-row ' + (kind === 'dhcp' ? 'act-dhcp' : 'act-' + ev.kind);
    if (kind === 'dhcp') {
      row.innerHTML = `<span class="act-ts">${esc(ev.ts)}</span>
        <span class="act-kind">DHCP ${esc(ev.kind.toUpperCase())}</span>
        <span class="act-name">${esc(ev.value || '')} ${ev.name ? '· <b>' + esc(ev.name) + '</b>' : ''}</span>
        <span class="act-val dim">${esc(ev.mac || '')}</span>`;
    } else {
      row.innerHTML = `<span class="act-ts">${esc(ev.ts)}</span>
        <span class="act-kind">${activityIcons[ev.kind] || '·'} ${esc(ev.kind)}${ev.rtype ? '[' + esc(ev.rtype) + ']' : ''}</span>
        <span class="act-name">${esc(ev.name || '')}</span>
        <span class="act-val dim">${esc(ev.value || ev.client || '')}</span>`;
    }
    feed.prepend(row);
    activityCount++;
    while (feed.children.length > 60) feed.lastElementChild.remove();
    const hint = $('#activity-hint');
    if (hint) hint.textContent = `${activityCount} events this session`;
  }

  function initLookup() {
    const form = $('#lookup-form'); if (!form) return;
    form.addEventListener('submit', async e => {
      e.preventDefault();
      const name = $('#lookup-name').value.trim();
      const type = $('#lookup-type').value;
      const out = $('#lookup-result');
      if (!name) return;
      out.innerHTML = '<div class="lookup-empty"><span class="spinner"></span></div>';
      try {
        const d = await api('GET', `/api/lookup?name=${encodeURIComponent(name)}&type=${type}`);
        if (d.ok) {
          out.innerHTML = `<div class="lookup-meta"><span class="badge badge-green">OK</span>
              <span class="dim">${d.ms} ms</span></div>` +
            (d.answers || []).map(a => `<div class="lookup-answer mono-sm">${esc(a)}</div>`).join('');
          if (!d.answers || !d.answers.length) out.innerHTML += '<div class="lookup-empty">no answers</div>';
        } else {
          out.innerHTML = `<div class="lookup-meta"><span class="badge badge-red">FAIL</span>
            <span class="dim">${d.ms} ms</span></div><div class="lookup-answer err">${esc(d.error)}</div>`;
        }
      } catch (err) { out.innerHTML = `<div class="lookup-answer err">${esc(err.message)}</div>`; }
    });
  }

  /* ── DHCP page: live lease table with keyed diff ───────────────────── */
  function renderLeaseTable() {
    const box = $('#lease-table'); if (!box) return;
    const filter = ($('#lease-filter')?.value || '').toLowerCase();
    const count = $('#lease-count');
    let leases = leaseRows([...S.leases].sort((a, b) => a.ip_address.localeCompare(b.ip_address, undefined, { numeric: true })));
    if (filter) leases = leases.filter(l =>
      (l.ip_address + ' ' + l.mac_address + ' ' + (l.hostname || '')).toLowerCase().includes(filter));
    if (count) count.textContent = `${leases.length} of ${S.leases.length}`;

    if (!S.leases.length) {
      box.innerHTML = `<div class="empty"><h3>No active leases</h3>
        <p>Configure a range under the Ranges tab; leases appear here in realtime.</p></div>`;
      return;
    }
    let table = $('table', box), tbody;
    if (!table) {
      box.innerHTML = `<div class="tbl-wrap"><table>
        <thead><tr><th>IP address</th><th>Hostname</th><th>MAC</th><th>Client ID</th><th>Expires</th><th>Remaining</th><th></th></tr></thead>
        <tbody></tbody></table></div>`;
      table = $('table', box);
    }
    tbody = $('tbody', table);
    const seen = new Set();
    const existing = new Map($$('tr', tbody).map(tr => [tr.dataset.key, tr]));
    let prev = null;
    for (const l of leases) {
      const key = l.mac_address + '|' + l.ip_address;
      seen.add(key);
      const expiryTxt = l.expiry_unix === 0 ? '∞' : new Date(l.expiry_unix * 1000).toLocaleString(undefined, { month: 'short', day: '2-digit', hour: '2-digit', minute: '2-digit' });
      const html = `
        <td class="mono-sm"><strong>${esc(l.ip_address)}</strong>${l.ipv6 ? ' <span class="badge badge-cyan">v6</span>' : ''}</td>
        <td>${l.hostname ? esc(l.hostname) : '<span class="dim">—</span>'}</td>
        <td class="mono-sm dim">${esc(l.mac_address)}</td>
        <td class="mono-sm dim">${esc(l.client_id || '—')}</td>
        <td class="dim">${expiryTxt}</td>
        <td><span class="badge ${l.remainTxt === 'expired' ? 'badge-red' : 'badge-gray'}">${l.remainTxt}</span></td>
        <td class="td-actions"><button class="btn btn-ghost btn-sm" data-reserve title="Create a static reservation">Reserve</button></td>`;
      let tr = existing.get(key);
      if (!tr) {
        tr = document.createElement('tr');
        tr.dataset.key = key;
        tr.classList.add('row-new');
        setTimeout(() => tr.classList.remove('row-new'), 1600);
      }
      if (tr.innerHTML !== html) tr.innerHTML = html;
      $('[data-reserve]', tr).onclick = () => openEntryModal('dhcp-host', null, {
        mac: l.mac_address, ip: l.ip_address, hostname: l.hostname || '',
      });
      // keep order
      if (prev) { if (prev.nextElementSibling !== tr) prev.after(tr); }
      else if (tbody.firstElementChild !== tr) tbody.prepend(tr);
      prev = tr;
    }
    existing.forEach((tr, key) => { if (!seen.has(key)) tr.remove(); });
  }

  /* ── encrypted upstream (DNS page) ─────────────────────────────────── */
  async function renderEncDNS() {
    const box = $('#encdns');
    if (!box) return;
    let d;
    try { d = await api('GET', '/api/encdns'); }
    catch (e) { box.innerHTML = ''; return; }
    const st = d.status, providers = d.providers;
    const encrypted = st.dnsmasq_encrypted && st.active && st.answering;

    const chip = (ok, okTxt, badTxt, warn) =>
      `<span class="badge ${ok ? 'badge-green' : warn ? 'badge-yellow' : 'badge-red'}">${ok ? okTxt : badTxt}</span>`;

    box.innerHTML = `
      <div class="card encdns-card ${encrypted ? 'enc-on' : ''}">
        <div class="card-header">
          <div class="card-title">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75">
              <rect x="3" y="11" width="18" height="11" rx="2" />
              <path d="M7 11V7a5 5 0 0 1 10 0v4" />
            </svg>
            Encrypted upstream
            ${encrypted
        ? `<span class="badge badge-green">● ${esc(st.protocol || 'DoH')} active</span>`
        : '<span class="badge badge-gray">plaintext</span>'}
          </div>
          <span class="card-hint">dnsmasq → dnscrypt-proxy (127.0.0.1#5053) → HTTPS</span>
        </div>
        <div class="card-body">
          <p class="enc-desc">Routes internet DNS through a local DoH forwarder so your ISP can't read or
            tamper with queries. Local records, <code>.lan</code> forwarding and caching stay in dnsmasq.</p>
          <div class="enc-row">
            <div class="enc-status">
              <div class="enc-stat"><span class="enc-k">forwarder</span>
                ${!st.installed ? chip(false, '', 'not installed')
        : chip(st.active, 'running', 'stopped', true)}</div>
              <div class="enc-stat"><span class="enc-k">resolving</span>
                ${st.active ? chip(st.answering, 'yes', 'no') : '<span class="badge badge-gray">—</span>'}</div>
              <div class="enc-stat"><span class="enc-k">dnsmasq</span>
                ${chip(st.dnsmasq_encrypted, 'encrypted route', 'plain upstreams', true)}</div>
            </div>
            <div class="enc-controls">
              <select class="form-select" id="enc-provider" ${st.dnsmasq_encrypted ? '' : ''}>
                ${Object.entries(providers).map(([id, label]) =>
          `<option value="${id}" ${id === (st.provider || 'cloudflare') ? 'selected' : ''}>${esc(label)}</option>`).join('')}
              </select>
              <button class="btn ${st.dnsmasq_encrypted ? 'btn-danger' : 'btn-primary'}" id="enc-toggle"
                ${!st.installed ? 'disabled title="install dnscrypt-proxy first"' : ''}>
                ${st.dnsmasq_encrypted ? 'Disable' : 'Enable encryption'}
              </button>
            </div>
          </div>
          ${st.detail ? `<div class="form-hint">${esc(st.detail)} — <code>sudo dnf install dnscrypt-proxy</code></div>` : ''}
        </div>
      </div>`;

    $('#enc-toggle', box).addEventListener('click', b => guarded(b, async () => {
      const enable = !st.dnsmasq_encrypted;
      const provider = $('#enc-provider', box).value;
      b.innerHTML = '<span class="spinner"></span> ' + (enable ? 'Enabling…' : 'Disabling…');
      const d2 = await api('PUT', '/api/encdns', { enabled: enable, provider });
      toast(d2.message, 'success', 5000);
      await loadConf();
      renderAllSections();
      renderEncDNS();
    }));
    // switching provider while encrypted re-applies immediately
    $('#enc-provider', box).addEventListener('change', async e => {
      if (!st.dnsmasq_encrypted) return;
      try {
        const d2 = await api('PUT', '/api/encdns', { enabled: true, provider: e.target.value });
        toast(d2.message, 'success');
        renderEncDNS();
      } catch (err) { toast(err.message, 'error', 6000); renderEncDNS(); }
    });
  }

  /* ── resolver health / browser-bypass check (DNS page) ─────────────── */
  async function renderResolverCheck() {
    const box = $('#resolver-check');
    if (!box) return;
    let d;
    try { d = await api('GET', '/api/resolver-check'); }
    catch { box.innerHTML = ''; return; }

    const nsList = (d.nameservers || []).map(ip =>
      `<code class="mr4">${esc(ip)}</code>`).join('') || '<span class="dim">none</span>';
    const riskHtml = (d.doh_risks || []).length
      ? `<div class="rc-warn">⚠ resolv.conf lists ${d.doh_risks.map(r =>
        `<code>${esc(r.ip)}</code> (${esc(r.provider)})`).join(', ')} —
        browsers that recognise these can silently switch to their own DoH and bypass dnsmasq.
        Keep the browser test below green, or set the browser's Secure DNS to <b>Off</b>.</div>`
      : '';

    box.innerHTML = `
      <div class="card">
        <div class="card-header">
          <div class="card-title">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75">
              <path d="M22 11.08V12a10 10 0 1 1-5.93-9.14" />
              <polyline points="22 4 12 14.01 9 11.01" />
            </svg>
            Resolver health
            ${d.dnsmasq_first
        ? '<span class="badge badge-green">system → dnsmasq</span>'
        : '<span class="badge badge-red">system NOT using dnsmasq</span>'}
          </div>
          <span class="card-hint">nameservers: ${nsList}</span>
        </div>
        <div class="card-body">
          ${riskHtml}
          <div class="enc-row">
            <p class="enc-desc" style="margin:0">
              <b>Is my browser using this resolver?</b> Browsers can bypass dnsmasq with built-in
              DoH ("Secure DNS"). This test makes your browser resolve a random marker name and
              checks whether the query reached dnsmasq.
              ${!d.log_queries ? '<br><span class="err-text">Requires log-queries (Settings → Logging).</span>' : ''}
            </p>
            <div class="enc-controls">
              <button class="btn btn-primary" id="rc-test" ${!d.log_queries ? 'disabled' : ''}>Test this browser</button>
            </div>
          </div>
          <div id="rc-result"></div>
        </div>
      </div>`;

    const btn = $('#rc-test', box);
    if (btn) btn.addEventListener('click', async () => {
      const out = $('#rc-result');
      btn.disabled = true;
      out.innerHTML = '<div class="rc-running"><span class="spinner"></span> resolving marker through this browser…</div>';

      const rand = () => Math.random().toString(36).slice(2, 12);
      const marker = 'browser-check-' + rand() + '.test';
      const control = 'control-check-' + rand() + '.test';

      // Preconditions: without a running dnsmasq + query logging the test
      // cannot distinguish anything — report inconclusive up front.
      const inconclusive = why => {
        out.innerHTML = `<div class="rc-verdict warn">? Inconclusive — ${why}</div>`;
      };
      if (S.status && !S.status.running) { inconclusive('dnsmasq is not running.'); btn.disabled = false; return; }
      if (!d.log_queries) { inconclusive('log-queries is off (Settings → Logging).'); btn.disabled = false; return; }

      try {
        // Make THIS browser resolve the marker. Match the page's scheme so
        // mixed-content blocking can never kill the lookup before DNS fires,
        // and use both fetch and an image beacon for belt-and-braces.
        const url = location.protocol + '//' + marker + '/';
        const ctl = new AbortController();
        setTimeout(() => ctl.abort(), 2500);
        const img = new Image();
        img.src = url + 'px.gif';
        await fetch(url, { mode: 'no-cors', signal: ctl.signal }).catch(() => { });
        img.src = '';

        // Poll the journal (browser → resolver → dnsmasq → journald has real
        // latency). The first poll also fires the server-side control query.
        let v = { marker_seen: false, control_seen: false };
        for (let i = 0; i < 5; i++) {
          const q = `name=${encodeURIComponent(marker)}&control=${encodeURIComponent(control)}` + (i === 0 ? '&fire=1' : '');
          v = await api('GET', '/api/resolver-check/verify?' + q);
          if (v.marker_seen) break;
          await new Promise(r => setTimeout(r, 900));
        }

        if (v.marker_seen) {
          out.innerHTML = `<div class="rc-verdict ok">✓ Your browser's DNS goes through dnsmasq — the marker
            query <code>${esc(marker)}</code> arrived. The encrypted chain covers this browser.</div>`;
        } else if (v.control_seen) {
          // journald pipeline demonstrably works, the browser's query never came
          out.innerHTML = `<div class="rc-verdict bad">✕ The control query reached dnsmasq but this browser's
            marker never did — the browser resolves with its own Secure DNS (DoH) and <b>bypasses your chain</b>.<br>
            Fix: Chrome → <code>chrome://settings/security</code> → “Use secure DNS” → <b>Off</b> ·
            Firefox → Settings → Privacy → “Enable DNS over HTTPS” → <b>Off</b> — dnsmasq already
            encrypts upstream, so nothing is lost.</div>`;
        } else {
          inconclusive(`neither the browser marker nor the server-side control query surfaced in the
            journal within 5 s — the logging pipeline looks stalled (journald backlog or the browser
            never issued the lookup). Try again; a real DoH bypass would show the control but not the marker.`);
        }
      } catch (e) {
        inconclusive('test error: ' + esc(e.message));
      } finally { btn.disabled = false; }
    });
  }

  /* ── network page ──────────────────────────────────────────────────── */
  async function renderNetworkExtras() {
    const box = $('#iface-table'); if (!box) return;
    try {
      const ifaces = await api('GET', '/api/interfaces');
      const listening = new Set(confEntries('interface').map(l => l.value));
      box.innerHTML = `<div class="tbl-wrap"><table>
        <thead><tr><th>Interface</th><th>State</th><th>Addresses</th><th class="th-actions"></th></tr></thead>
        <tbody>${ifaces.map(i => `<tr>
          <td><strong>${esc(i.name)}</strong></td>
          <td>${i.up ? '<span class="badge badge-green">up</span>' : '<span class="badge badge-gray">down</span>'}</td>
          <td class="mono-sm dim">${i.addrs.map(esc).join('&ensp;') || '—'}</td>
          <td class="td-actions">${listening.has(i.name)
          ? '<span class="badge badge-cyan">listening</span>'
          : `<button class="btn btn-ghost btn-sm" data-listen="${esc(i.name)}">+ listen</button>`}</td>
        </tr>`).join('')}</tbody></table></div>`;
      $$('[data-listen]', box).forEach(b => b.addEventListener('click', () => guarded(b, async () => {
        S.conf = await api('POST', '/api/conf/lines', { key: 'interface', value: b.dataset.listen });
        toast(`interface=${b.dataset.listen} added`, 'success');
        rerender();
      })));
    } catch (e) {
      box.innerHTML = `<div class="empty small"><h3>Unavailable</h3><p>${esc(e.message)}</p></div>`;
    }
  }

  /* ── settings page ─────────────────────────────────────────────────── */
  function renderBootToggle() {
    const t = $('#boot-toggle'); if (!t || !S.status) return;
    t.checked = !!S.status.enabled;
    const badge = $('#boot-badge');
    if (badge) badge.innerHTML = S.status.enabled
      ? '<span class="badge badge-green">enabled</span>' : '<span class="badge badge-gray">disabled</span>';
  }
  function initSettings() {
    const t = $('#boot-toggle'); if (!t) return;
    t.addEventListener('change', async () => {
      t.disabled = true;
      try {
        const d = await api('POST', '/api/service/' + (t.checked ? 'enable' : 'disable'));
        toast(d.message, 'success');
      } catch (e) { t.checked = !t.checked; toast(e.message, 'error', 6000); }
      finally { t.disabled = false; }
    });
  }

  /* ── config page ───────────────────────────────────────────────────── */
  const cfgPage = { rev: null, dirty: false, loadedContent: '' };

  async function renderConfigPage() {
    const ta = $('#raw-editor'); if (!ta) return;
    if (cfgPage.dirty) return; // don't clobber unsaved edits on SSE refresh
    const d = await api('GET', '/api/conf/raw');
    cfgPage.rev = d.rev; cfgPage.loadedContent = d.content;
    ta.value = d.content;
    updateEditorMeta();
    renderLineExplorer();
  }

  function updateEditorMeta() {
    const ta = $('#raw-editor'); if (!ta) return;
    const lines = ta.value.split('\n').length;
    $('#editor-meta').textContent = `${lines} lines · rev ${cfgPage.rev || '—'}${cfgPage.dirty ? ' · unsaved changes' : ''}`;
    $('#btn-save-raw').disabled = !cfgPage.dirty;
  }

  function initConfigPage() {
    const ta = $('#raw-editor'); if (!ta) return;
    ta.addEventListener('input', () => {
      cfgPage.dirty = ta.value !== cfgPage.loadedContent;
      $('#validate-result').textContent = '';
      updateEditorMeta();
    });
    ta.addEventListener('keydown', e => {
      if (e.key === 'Tab') {
        e.preventDefault();
        ta.setRangeText('    ', ta.selectionStart, ta.selectionEnd, 'end');
        ta.dispatchEvent(new Event('input'));
      }
      if ((e.ctrlKey || e.metaKey) && e.key === 's') { e.preventDefault(); $('#btn-save-raw').click(); }
    });
    $('#btn-validate').addEventListener('click', b => guarded(b, async () => {
      const d = await api('POST', '/api/conf/validate', { content: ta.value });
      const out = $('#validate-result');
      if (d.valid) { out.innerHTML = '<span class="ok-text">✓ syntax OK</span>'; }
      else { out.innerHTML = `<span class="err-text">✕ ${esc(d.error)}</span>`; }
    }));
    $('#btn-save-raw').addEventListener('click', b => guarded(b, async () => {
      const d = await api('PUT', '/api/conf/raw', { content: ta.value, rev: cfgPage.rev });
      cfgPage.rev = d.rev; cfgPage.dirty = false; cfgPage.loadedContent = ta.value;
      updateEditorMeta();
      toast('Configuration saved', 'success');
      await loadConf(); renderLineExplorer(); renderBadges();
    }));
    $('#line-filter').addEventListener('input', debounce(renderLineExplorer, 150));
    $('#hide-comments').addEventListener('change', renderLineExplorer);
    $('#btn-add-directive').addEventListener('click', openAddDirectiveModal);
  }

  function openAddDirectiveModal() {
    const form = document.createElement('form');
    form.className = 'entry-form';
    const keys = S.schema.directives.map(d => d.key).sort();
    form.innerHTML = `
      <div class="form-group">
        <label class="form-label">Directive</label>
        <input class="form-input" name="key" list="dir-keys" placeholder="e.g. server" autocomplete="off" spellcheck="false">
        <datalist id="dir-keys">${keys.map(k => `<option value="${k}">`).join('')}</datalist>
        <div class="form-hint" id="add-dir-help"></div>
      </div>
      <div class="form-group">
        <label class="form-label">Value <span class="dim">(leave empty for a flag)</span></label>
        <input class="form-input" name="value" autocomplete="off" spellcheck="false">
      </div>`;
    const keyInput = form.elements.key;
    keyInput.addEventListener('input', () => {
      const d = S.dir[keyInput.value.trim()];
      $('#add-dir-help').textContent = d ? d.help : '';
      form.elements.value.placeholder = d ? (d.placeholder || d.syntax || '') : '';
    });
    const save = b => guarded(b, async () => {
      const key = keyInput.value.trim();
      const value = form.elements.value.value.trim();
      if (!key) throw new Error('directive name is required');
      S.conf = await api('POST', '/api/conf/lines', { key, value, flag: value === '' });
      modal.close(); toast(`${key} added`, 'success');
      cfgPage.dirty = false; await renderConfigPage(); renderBadges();
    });
    form.addEventListener('submit', e => { e.preventDefault(); save(null); });
    modal.open({
      title: 'Add directive', body: form,
      footer: [btn('Cancel', 'btn-ghost', modal.close), btn('Add', 'btn-primary', save)],
    });
  }

  function renderLineExplorer() {
    const box = $('#line-explorer'); if (!box || !S.conf) return;
    const filter = ($('#line-filter')?.value || '').toLowerCase();
    const hideComments = $('#hide-comments')?.checked;
    let lines = S.conf.lines;
    if (hideComments) lines = lines.filter(l => l.key !== '');
    if (filter) lines = lines.filter(l => l.raw.toLowerCase().includes(filter));
    if (!lines.length) {
      box.innerHTML = '<div class="empty small"><h3>No matching lines</h3><p>Adjust the filter above.</p></div>';
      return;
    }
    box.innerHTML = `<div class="line-list">${lines.map(l => {
      const d = S.dir[l.key];
      const known = !!d;
      return `<div class="line-row ${l.key === '' ? 'line-comment' : known ? '' : 'line-unknown'}" data-idx="${l.idx}">
        <span class="line-no">${l.idx + 1}</span>
        <span class="line-raw mono-sm">${esc(l.raw) || '&nbsp;'}</span>
        ${known ? `<span class="line-label" title="${esc(d.help)}">${esc(d.label)}</span>` : (l.key ? '<span class="line-label unknown">unmanaged</span>' : '')}
        <span class="line-actions">
          ${l.key !== '' ? '' : ''}<button class="btn btn-ghost btn-sm" data-edit-line>Edit</button>
          <button class="btn btn-ghost btn-sm danger" data-del-line>✕</button>
        </span>
      </div>`;
    }).join('')}</div>`;
    $$('.line-row', box).forEach(row => {
      const idx = Number(row.dataset.idx);
      const line = S.conf.lines[idx];
      $('[data-edit-line]', row).addEventListener('click', () => {
        if (line.key && S.dir[line.key] && S.dir[line.key].kind === 'multi') { openEntryModal(line.key, line); return; }
        openRawEditModal(line);
      });
      $('[data-del-line]', row).addEventListener('click', async () => {
        const ok = await confirmDialog('Delete line', `Remove line ${idx + 1}?\n${line.raw || '(blank line)'}`, 'Delete');
        if (!ok) return;
        try {
          S.conf = await api('DELETE', `/api/conf/lines/${idx}?expect_raw=${encodeURIComponent(line.raw)}`);
          toast('Line removed', 'success');
          cfgPage.dirty = false; renderConfigPage(); renderBadges();
        } catch (e) { toast(e.message, 'error', 6000); }
      });
    });
  }

  function openRawEditModal(line) {
    const form = document.createElement('form');
    form.className = 'entry-form';
    form.innerHTML = `<div class="form-group">
      <label class="form-label">Line ${line.idx + 1}</label>
      <input class="form-input mono" name="raw" value="${esc(line.raw)}" autocomplete="off" spellcheck="false">
    </div>`;
    const save = b => guarded(b, async () => {
      S.conf = await api('PUT', `/api/conf/lines/${line.idx}`, { raw: form.elements.raw.value, expect_raw: line.raw });
      modal.close(); toast('Line updated', 'success');
      cfgPage.dirty = false; renderConfigPage(); renderBadges(); renderAllSections();
    });
    form.addEventListener('submit', e => { e.preventDefault(); save(null); });
    modal.open({
      title: 'Edit line', body: form, wide: true,
      footer: [btn('Cancel', 'btn-ghost', modal.close), btn('Save', 'btn-primary', save)],
    });
  }

  /* ── logs page ─────────────────────────────────────────────────────── */
  const LOG_CAP = 2000;
  function classifyLog(raw) {
    const l = raw.toLowerCase();
    if (l.includes('error') || l.includes('failed') || l.includes('refused')) return 'err';
    if (l.includes('warn')) return 'warn';
    if (l.includes('started') || l.includes('read /etc') || l.includes('using nameserver') || l.includes('compile time')) return 'success';
    if (l.includes('query') || l.includes('forwarded') || l.includes('reply') || l.includes('cached')) return 'query';
    if (l.includes('dhcp')) return 'dhcp';
    return '';
  }
  function fmtLogLine(raw) {
    const m = raw.match(/^(\S+)\s+(\S+)\s+(\S+?):\s*(.*)$/);
    const cls = classifyLog(raw);
    if (m) {
      let ts = m[1];
      const tm = ts.match(/T(\d{2}:\d{2}:\d{2})/);
      if (tm) ts = tm[1];
      return `<div class="log-line ${cls}"><span class="log-ts">${esc(ts)}</span><span class="log-proc">${esc(m[3])}</span><span class="log-msg">${esc(m[4])}</span></div>`;
    }
    return `<div class="log-line ${cls}"><span class="log-msg">${esc(raw)}</span></div>`;
  }

  function nearBottom(el) { return el.scrollHeight - el.scrollTop - el.clientHeight < 60; }

  function appendLogLine(raw) {
    const box = $('#log-box'); if (!box) return;
    const stick = nearBottom(box);
    const filter = ($('#log-filter')?.value || '').toLowerCase();
    box.insertAdjacentHTML('beforeend', fmtLogLine(raw));
    const el = box.lastElementChild;
    if (filter && !raw.toLowerCase().includes(filter)) el.style.display = 'none';
    while (box.children.length > LOG_CAP) box.firstElementChild.remove();
    if (stick) box.scrollTop = box.scrollHeight;
    const c = $('#log-count'); if (c) c.textContent = box.children.length + ' lines';
  }

  let queryCount = 0;
  function appendQueryRow(ev) {
    const box = $('#query-box'); if (!box) return;
    const stick = nearBottom(box);
    const filter = ($('#query-filter')?.value || '').toLowerCase();
    const div = document.createElement('div');
    div.className = 'log-line qrow q-' + ev.kind;
    div.innerHTML = `<span class="log-ts">${esc(ev.ts)}</span>
      <span class="q-kind">${esc(ev.kind)}${ev.rtype ? '<b>[' + esc(ev.rtype) + ']</b>' : ''}</span>
      <span class="q-name">${esc(ev.name || '')}</span>
      <span class="q-arrow">${ev.kind === 'query' ? '⇐' : '⇒'}</span>
      <span class="q-val">${esc(ev.value || ev.client || '')}</span>`;
    if (filter && !div.textContent.toLowerCase().includes(filter)) div.style.display = 'none';
    box.appendChild(div);
    while (box.children.length > LOG_CAP) box.firstElementChild.remove();
    if (stick) box.scrollTop = box.scrollHeight;
    queryCount++;
    const c = $('#query-count'); if (c) c.textContent = queryCount + ' events';
  }

  function renderQueryNote() {
    const note = $('#query-note'); if (!note || !S.conf) return;
    note.hidden = confScalar('log-queries') !== null;
  }

  async function initLogsPage() {
    if (S.page !== 'logs') return;
    const box = $('#log-box'); if (!box) return;
    const load = async () => {
      const n = $('#log-lines').value;
      const d = await api('GET', '/api/service/logs?lines=' + n);
      const logs = d.logs || [];
      box.innerHTML = logs.map(fmtLogLine).join('');
      box.scrollTop = box.scrollHeight;
      $('#log-count').textContent = logs.length + ' lines';
      applyLogFilter();
      // Backfill the query stream from journal history so the DNS action is
      // visible immediately; live SSE events keep appending after this.
      const qbox = $('#query-box');
      qbox.innerHTML = '';
      queryCount = 0;
      logs.forEach(raw => {
        const ev = parseJournal(raw);
        if (ev && ev.stream === 'query') appendQueryRow(ev);
      });
      qbox.scrollTop = qbox.scrollHeight;
    };
    const applyLogFilter = () => {
      const q = ($('#log-filter').value || '').toLowerCase();
      $$('#log-box .log-line').forEach(el => {
        el.style.display = !q || el.textContent.toLowerCase().includes(q) ? '' : 'none';
      });
    };
    $('#log-lines').addEventListener('change', load);
    $('#log-filter').addEventListener('input', debounce(applyLogFilter, 120));
    $('#query-filter').addEventListener('input', debounce(() => {
      const q = ($('#query-filter').value || '').toLowerCase();
      $$('#query-box .log-line').forEach(el => {
        el.style.display = !q || el.textContent.toLowerCase().includes(q) ? '' : 'none';
      });
    }, 120));
    $('#btn-pause').addEventListener('click', () => {
      S.paused = !S.paused;
      $('#pause-label').textContent = S.paused ? 'Resume' : 'Pause';
      $('#btn-pause').classList.toggle('btn-primary', S.paused);
      $$('.live-inline').forEach(el => el.classList.toggle('paused', S.paused));
    });
    $('#btn-clear').addEventListener('click', () => {
      $('#log-box').innerHTML = ''; $('#query-box').innerHTML = '';
      queryCount = 0; $('#query-count').textContent = '0 events'; $('#log-count').textContent = '0 lines';
    });
    renderQueryNote();
    await load();
  }

  /* ── backups page ──────────────────────────────────────────────────── */
  async function renderBackups() {
    const box = $('#backup-list'); if (!box) return;
    const backups = await api('GET', '/api/backups') || [];
    $('#backup-count').textContent = backups.length + (backups.length === 1 ? ' backup' : ' backups');
    if (!backups.length) {
      box.innerHTML = `<div class="empty"><h3>No backups yet</h3>
        <p>A snapshot is taken automatically before every configuration write.</p></div>`;
      return;
    }
    box.innerHTML = backups.map(b => `
      <div class="backup-row" data-name="${esc(b.filename)}">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" width="17" height="17" class="dim">
          <path d="M13 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9z" /><polyline points="13 2 13 9 20 9" />
        </svg>
        <div class="backup-name mono-sm">${esc(b.filename)}</div>
        <div class="backup-meta">${(b.size / 1024).toFixed(1)} KB</div>
        <div class="backup-meta">${new Date(b.mod_time).toLocaleString(undefined, { month: 'short', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit' })}</div>
        <div class="backup-actions">
          <button class="btn btn-ghost btn-sm" data-view>View</button>
          <button class="btn btn-success btn-sm" data-restore>Restore</button>
          <button class="btn btn-ghost btn-sm danger" data-del>✕</button>
        </div>
      </div>`).join('');
    $$('.backup-row', box).forEach(row => {
      const name = row.dataset.name;
      $('[data-view]', row).addEventListener('click', () => viewBackup(name));
      $('[data-restore]', row).addEventListener('click', async b => {
        const ok = await confirmDialog('Restore backup',
          `Replace the current configuration with "${name}"? The current config is backed up first. You'll still need to restart dnsmasq to apply.`, 'Restore', true);
        if (!ok) return;
        await guarded(b, async () => {
          await api('POST', '/api/backups/restore', { filename: name });
          toast('Backup restored — restart dnsmasq to apply', 'success', 5000);
          await loadConf(); renderBackups(); renderBadges();
        });
      });
      $('[data-del]', row).addEventListener('click', async () => {
        const ok = await confirmDialog('Delete backup', `Delete "${name}"? This cannot be undone.`, 'Delete');
        if (!ok) return;
        try {
          await api('DELETE', '/api/backups/' + encodeURIComponent(name));
          toast('Backup deleted', 'success'); renderBackups();
        } catch (e) { toast(e.message, 'error', 6000); }
      });
    });
  }

  async function viewBackup(name) {
    try {
      const d = await api('GET', '/api/backups/' + encodeURIComponent(name));
      const identical = d.content === d.current;
      const body = document.createElement('div');
      body.innerHTML = `
        <div class="backup-diff-meta">${identical
          ? '<span class="badge badge-green">identical to current config</span>'
          : '<span class="badge badge-yellow">differs from current config</span>'}</div>
        <pre class="backup-view mono-sm">${esc(d.content)}</pre>`;
      modal.open({
        title: name, body, wide: true,
        footer: [btn('Close', 'btn-ghost', modal.close)],
      });
    } catch (e) { toast(e.message, 'error'); }
  }

  function initBackups() {
    const b = $('#btn-create-backup'); if (!b) return;
    b.addEventListener('click', () => guarded(b, async () => {
      await api('POST', '/api/backups');
      toast('Snapshot created', 'success'); renderBackups();
    }));
  }

  /* ── MCP page ──────────────────────────────────────────────────────── */
  function mcpAgo(iso) {
    if (!iso) return 'never';
    const d = (Date.now() - new Date(iso).getTime()) / 1000;
    if (d < 5) return 'just now';
    if (d < 60) return Math.floor(d) + 's ago';
    if (d < 3600) return Math.floor(d / 60) + 'm ago';
    if (d < 86400) return Math.floor(d / 3600) + 'h ago';
    return Math.floor(d / 86400) + 'd ago';
  }

  async function renderMCP(data) {
    const st = data || await api('GET', '/api/mcp/status').catch(() => null);
    if (!st) return;
    S.mcp = st;

    // Honest status model: the integration is "Ready" whenever this console is
    // up — it IS the API the MCP calls. "Active" means an agent called within
    // the recent window. A stdio MCP is rarely mid-call, so we never show a
    // scary "offline"; green Ready is the resting state.
    const active = !!st.connected;
    const beacon = $('#mcp-beacon');
    if (beacon) beacon.className = 'mcp-beacon ' + (active ? 'active' : 'idle');
    const set = (id, v) => { const el = $('#' + id); if (el) el.textContent = v; };
    set('mcp-state', active ? 'Active' : 'Ready');
    set('mcp-substate',
      active ? 'An agent is using it right now'
        : st.seen_ever ? `Idle — last active ${mcpAgo(st.last_seen)}`
          : 'Waiting for an agent to connect');
    set('mcp-client', st.client || 'dnsmasq-web-mcp');
    set('mcp-total', st.total_calls ?? 0);
    set('mcp-blocked', st.blocked_calls ?? 0);
    set('mcp-lastseen', st.seen_ever ? mcpAgo(st.last_seen) : 'never');

    // write kill-switch
    const tog = $('#mcp-writes-toggle');
    if (tog && document.activeElement !== tog) tog.checked = !!st.writes_allowed;
    set('mcp-writes-text', st.writes_allowed ? 'Writable' : 'Read-only');
    const badge = $('#mcp-writes-badge');
    if (badge) badge.innerHTML = st.writes_allowed
      ? '<span class="badge badge-green">writable</span>'
      : '<span class="badge badge-yellow">read-only</span>';
    const note = $('#mcp-writes-note');
    if (note) note.textContent = st.writes_allowed
      ? 'The agent can read and change dnsmasq. Every write is still validated with dnsmasq --test and auto-snapshotted first.'
      : 'Read-only — the agent can inspect everything, but any write is rejected until you re-enable it here.';

    // recent calls
    const box = $('#mcp-recent'), cnt = $('#mcp-recent-count');
    if (box) {
      const rec = st.recent || [];
      box.innerHTML = rec.length
        ? '<div class="mcp-calls">' + rec.map(c => {
          const m = (c.method || '').toLowerCase();
          const t = new Date(c.at).toLocaleTimeString('en-GB');
          return `<div class="mcp-call${c.blocked ? ' blocked' : ''}">`
            + `<span class="mcp-method ${esc(m)}">${esc(c.method)}</span>`
            + `<span class="mcp-path">${esc(c.path)}${c.blocked ? ' <span class="badge badge-red">blocked</span>' : ''}</span>`
            + `<span class="mcp-call-time">${t}</span></div>`;
        }).join('') + '</div>'
        : '<div class="mcp-empty">No MCP calls yet — connect Claude Code and run a dnsmasq tool.</div>';
      if (cnt) cnt.textContent = rec.length ? `${rec.length} recent` : '';
    }
  }

  // Dashboard MCP tile — always green/Ready when the console is up.
  function renderMcpStat() {
    const el = $('#stat-mcp'); if (!el) return;
    const st = S.mcp;
    if (!st) { statSet('stat-mcp', 'Ready', 'integration live', 'green'); return; }
    const val = st.connected ? 'Active' : 'Ready';
    const trend = `${st.total_calls || 0} call${st.total_calls === 1 ? '' : 's'} · ${st.writes_allowed ? 'writable' : 'read-only'}`;
    statSet('stat-mcp', val, trend, st.writes_allowed ? 'green' : 'orange');
  }

  function initMCP() {
    const tog = $('#mcp-writes-toggle');
    if (!tog) return;
    tog.addEventListener('change', async () => {
      const allowed = tog.checked;
      try {
        const d = await api('PUT', '/api/mcp/writes', { allowed });
        toast(d.message, allowed ? 'success' : 'info');
      } catch (e) {
        toast('Failed: ' + e.message, 'error');
        tog.checked = !allowed; // revert on failure
      }
      renderMCP();
    });
  }

  /* ── chrome init ───────────────────────────────────────────────────── */
  function initChrome() {
    // active nav
    const active = { index: 'index', dns: 'dns', dhcp: 'dhcp', tftp: 'tftp', network: 'network', settings: 'settings', config: 'config', logs: 'logs', backups: 'backups', mcp: 'mcp' }[S.page];
    const nav = $(`[data-nav="${active}"]`);
    if (nav) nav.classList.add('active');
    // clock
    const tick = () => { const el = $('#clock'); if (el) el.textContent = new Date().toLocaleTimeString('en-GB'); };
    tick(); setInterval(tick, 1000);
    // mobile sidebar
    const sb = $('#sidebar');
    $('#mob-btn').addEventListener('click', () => sb.classList.toggle('open'));
    $('#sb-scrim').addEventListener('click', () => sb.classList.remove('open'));
    // apply bar restart + dismiss
    $('[data-action="svc-restart-apply"]').addEventListener('click', b => guarded(b, async () => {
      const d = await api('POST', '/api/service/restart');
      toast(d.message, 'success');
    }));
    $('[data-action="apply-dismiss"]').addEventListener('click', () => {
      sessionStorage.setItem('applyDismissed', '1');
      $('#apply-bar').hidden = true;
    });
    // auto-apply preference
    const autoCb = $('#auto-apply-cb');
    autoCb.checked = autoApplyOn();
    autoCb.addEventListener('change', () => {
      localStorage.setItem('autoApply', autoCb.checked ? '1' : '0');
      if (autoCb.checked && S.status && S.status.stale_config) scheduleAutoApply();
      else cancelAutoApply();
      toast('Auto-apply ' + (autoCb.checked ? 'on — changes restart dnsmasq automatically' : 'off'), 'info', 2600);
    });
    // a tab waking from background refetches status so it can never show a stale banner
    document.addEventListener('visibilitychange', async () => {
      if (document.hidden) return;
      try { S.status = await api('GET', '/api/service/status'); renderStatus(); } catch { }
    });
  }

  /* ── boot ──────────────────────────────────────────────────────────── */
  async function boot() {
    initChrome();
    initTabs();
    try {
      await loadSchema();
      await Promise.all([loadConf(), loadLeases()]);
      S.status = await api('GET', '/api/service/status');
    } catch (e) {
      toast('Failed to load: ' + e.message, 'error', 8000);
    }
    renderStatus();
    renderBadges();
    renderAllSections();

    switch (S.page) {
      case 'dns': renderEncDNS(); renderResolverCheck(); break;
      case 'index':
        renderDashboard(); initLookup();
        api('GET', '/api/mcp/status').then(d => { S.mcp = d; renderMcpStat(); }).catch(() => { });
        // backfill the live activity feed with recent journal history (deep
        // enough that restart boilerplate doesn't crowd out real traffic)
        api('GET', '/api/service/logs?lines=600').then(d => {
          (d.logs || []).forEach(raw => {
            const ev = parseJournal(raw);
            if (ev) appendActivity(ev, ev.stream === 'dhcp' ? 'dhcp' : 'dns');
          });
          updateActivityEmpty();
        }).catch(() => { });
        break;
      case 'dhcp':
        renderLeaseTable();
        $('#lease-filter')?.addEventListener('input', debounce(renderLeaseTable, 120));
        break;
      case 'network': renderNetworkExtras(); break;
      case 'settings': initSettings(); renderBootToggle(); break;
      case 'config': initConfigPage(); await renderConfigPage(); break;
      case 'logs': await initLogsPage(); break;
      case 'backups': initBackups(); await renderBackups(); break;
      case 'mcp': initMCP(); await renderMCP(); break;
    }
    connectSSE();
  }

  boot();
})();
