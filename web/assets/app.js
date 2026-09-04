/* clash-sub — shared UI behaviour (theme, toast, copy, dialogs).
   No framework; this is meant to be served from Go's embed.FS as-is. */
(function () {
  'use strict';

  /* ---- theme ------------------------------------------------------------ */

  var KEY = 'clashsub.theme';

  function apply(t) {
    document.documentElement.setAttribute('data-theme', t);
    document.querySelectorAll('[data-theme-toggle]').forEach(function (b) {
      b.setAttribute('aria-label', t === 'dark' ? '切换到浅色外观' : '切换到深色外观');
      b.querySelector('[data-icon-light]').hidden = t === 'dark';
      b.querySelector('[data-icon-dark]').hidden = t !== 'dark';
    });
  }

  var stored = null;
  try { stored = localStorage.getItem(KEY); } catch (e) {}
  apply(stored || (matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'));

  document.addEventListener('click', function (e) {
    var t = e.target.closest('[data-theme-toggle]');
    if (!t) return;
    var next = document.documentElement.getAttribute('data-theme') === 'dark' ? 'light' : 'dark';
    apply(next);
    try { localStorage.setItem(KEY, next); } catch (err) {}
  });

  /* ---- api -------------------------------------------------------------- */

  function onLogin() { return /login\.html$/.test(location.pathname); }

  /* Resolves with the parsed body, rejects with an Error carrying .status and
     the server's own message. A 401 anywhere but the login page bounces to it. */
  window.api = function (method, path, body) {
    var opt = { method: method, credentials: 'same-origin', headers: {} };
    if (body !== undefined) {
      opt.headers['Content-Type'] = 'application/json';
      opt.body = JSON.stringify(body);
    }
    return fetch(path, opt).then(function (r) {
      if (r.status === 401 && !onLogin()) {
        location.replace('login.html?next=' + encodeURIComponent(location.pathname + location.search));
        return new Promise(function () {});           // never settles; page is leaving
      }
      if (r.status === 204) return null;
      var ct = r.headers.get('content-type') || '';
      return (ct.indexOf('application/json') >= 0 ? r.json() : r.text()).then(function (data) {
        if (r.ok) return data;
        var e = new Error((data && data.error) || '请求失败（' + r.status + '）');
        e.status = r.status;
        throw e;
      });
    });
  };

  /* ---- toast ------------------------------------------------------------ */

  var toastEl, toastTimer;

  window.toast = function (msg, kind) {
    if (!toastEl) {
      toastEl = document.createElement('div');
      toastEl.className = 'toast';
      toastEl.setAttribute('role', 'status');
      document.body.appendChild(toastEl);
    }
    toastEl.innerHTML = '';
    var d = document.createElement('span');
    d.className = 'dot ' + (kind === 'error' ? '' : 'dot--live');
    if (kind === 'error') d.style.background = '#f08167';
    var s = document.createElement('span');
    s.textContent = msg;
    toastEl.appendChild(d);
    toastEl.appendChild(s);
    toastEl.setAttribute('data-show', '1');
    clearTimeout(toastTimer);
    toastTimer = setTimeout(function () { toastEl.removeAttribute('data-show'); }, 2400);
  };

  /* ---- copy ------------------------------------------------------------- */

  function copy(text) {
    if (navigator.clipboard && window.isSecureContext) return navigator.clipboard.writeText(text);
    return new Promise(function (res, rej) {
      var ta = document.createElement('textarea');
      ta.value = text;
      ta.style.cssText = 'position:fixed;opacity:0';
      document.body.appendChild(ta);
      ta.select();
      try { document.execCommand('copy'); res(); } catch (e) { rej(e); }
      ta.remove();
    });
  }
  window.copyText = copy;

  document.addEventListener('click', function (e) {
    var b = e.target.closest('[data-copy]');
    if (!b) return;
    copy(b.getAttribute('data-copy'))
      .then(function () { window.toast('订阅地址已复制'); })
      .catch(function () { window.toast('复制失败，请手动选择地址', 'error'); });
  });

  /* ---- dialogs ---------------------------------------------------------- */

  var lastFocus = null;

  window.openDialog = function (id) {
    var el = document.getElementById(id);
    if (!el) return;
    lastFocus = document.activeElement;
    el.hidden = false;
    var f = el.querySelector('[data-autofocus]') || el.querySelector('button, [href], input');
    if (f) f.focus();
  };

  window.closeDialog = function (el) {
    el = typeof el === 'string' ? document.getElementById(el) : el;
    if (!el) return;
    el.hidden = true;
    if (lastFocus) lastFocus.focus();
  };

  document.addEventListener('click', function (e) {
    var open = e.target.closest('[data-open]');
    if (open) { window.openDialog(open.getAttribute('data-open')); return; }

    var close = e.target.closest('[data-close]');
    if (close) { window.closeDialog(close.closest('.scrim')); return; }

    if (e.target.classList && e.target.classList.contains('scrim')) window.closeDialog(e.target);
  });

  document.addEventListener('keydown', function (e) {
    if (e.key !== 'Escape') return;
    var open = document.querySelector('.scrim:not([hidden])');
    if (open) window.closeDialog(open);
  });
})();
