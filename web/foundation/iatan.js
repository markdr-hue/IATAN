/*
 * IATAN Foundation Runtime
 * Built-in JS runtime + SPA router for IATAN-generated sites.
 * This file is embedded in the Go binary and auto-injected on every page.
 */

(function() {
  'use strict';

  // ---------------------------------------------------------------------------
  // App — global runtime API
  // ---------------------------------------------------------------------------
  var app = {
    _state: {},
    _listeners: {},

    // State management
    set: function(key, val) {
      this._state[key] = val;
      var fns = this._listeners[key];
      if (fns) for (var i = 0; i < fns.length; i++) fns[i](val);
    },
    get: function(key) { return this._state[key]; },
    on: function(key, fn) {
      if (!this._listeners[key]) this._listeners[key] = [];
      this._listeners[key].push(fn);
    },
    off: function(key, fn) {
      var fns = this._listeners[key];
      if (fns) this._listeners[key] = fns.filter(function(f) { return f !== fn; });
    },

    // API helper — auto-includes auth header, returns parsed JSON
    fetch: function(path, opts) {
      opts = opts || {};
      var headers = {};
      var src = opts.headers || {};
      for (var k in src) headers[k] = src[k];
      if (!headers['Content-Type'] && opts.body) headers['Content-Type'] = 'application/json';
      var token = localStorage.getItem('auth_token');
      if (token && !headers['Authorization']) headers['Authorization'] = 'Bearer ' + token;
      var fetchOpts = { method: opts.method || 'GET', headers: headers };
      if (opts.body) fetchOpts.body = typeof opts.body === 'string' ? opts.body : JSON.stringify(opts.body);
      return fetch(path, fetchOpts).then(function(res) {
        if (res.status === 401) {
          app.set('auth:user', null);
          localStorage.removeItem('auth_token');
        }
        var ct = res.headers.get('content-type') || '';
        if (ct.indexOf('application/json') === -1) {
          return { error: 'Server error (' + res.status + ')' };
        }
        return res.json();
      });
    },

    // Auth helpers
    auth: {
      login: function(endpoint, credentials) {
        return app.fetch(endpoint + '/login', { method: 'POST', body: credentials }).then(function(data) {
          if (data.token) {
            localStorage.setItem('auth_token', data.token);
            app.set('auth:user', data);
          }
          return data;
        });
      },
      register: function(endpoint, userData) {
        return app.fetch(endpoint + '/register', { method: 'POST', body: userData }).then(function(data) {
          if (data.token) {
            localStorage.setItem('auth_token', data.token);
            app.set('auth:user', data);
          }
          return data;
        });
      },
      logout: function() {
        localStorage.removeItem('auth_token');
        app.set('auth:user', null);
      },
      isLoggedIn: function() { return !!localStorage.getItem('auth_token'); },
      getToken: function() { return localStorage.getItem('auth_token'); },
      getRole: function() {
        var t = localStorage.getItem('auth_token');
        if (!t) return null;
        try { return JSON.parse(atob(t.split('.')[1])).role; } catch(e) { return null; }
      },
      getUser: function(endpoint) {
        return app.fetch(endpoint + '/me');
      },
      onAuthChange: function(fn) { app.on('auth:user', fn); }
    },

    // Navigation — delegates to SPA router or full page load
    navigate: function(path) {
      if (window.__iatan_router) window.__iatan_router.go(path);
      else window.location.href = path;
    }
  };

  window.App = app;

  // ---------------------------------------------------------------------------
  // OAuth token capture — grab #token= from URL fragment on any page load.
  // Fragments are never sent to the server, preventing token leakage in
  // server logs, Referer headers, and browser history.
  // ---------------------------------------------------------------------------
  var hashParams = new URLSearchParams(window.location.hash.substring(1));
  var oauthToken = hashParams.get('token');
  // Backward compat: also check query param for older redirects.
  if (!oauthToken) {
    var searchParams = new URLSearchParams(window.location.search);
    oauthToken = searchParams.get('token');
  }
  if (oauthToken) {
    localStorage.setItem('auth_token', oauthToken);
    window.history.replaceState({}, '', window.location.pathname);
    app.set('auth:user', { token: oauthToken });
  }

  // ---------------------------------------------------------------------------
  // SPA Router — only activates when window.__IATAN_SPA = true
  // ---------------------------------------------------------------------------
  if (!window.__IATAN_SPA) return;

  var router = {
    loading: false,
    _loadId: 0,

    init: function() {
      var self = this;

      // Intercept link clicks
      document.addEventListener('click', function(e) {
        var link = e.target.closest('a[href]');
        if (!link) return;

        var href = link.getAttribute('href');
        // Skip: external, anchors, new-tab, non-http, javascript:
        if (!href || href.charAt(0) === '#' || href.indexOf('://') !== -1 ||
            href.indexOf('javascript:') === 0 || href.indexOf('mailto:') === 0 ||
            link.target === '_blank' || e.ctrlKey || e.metaKey || e.shiftKey) return;

        // Skip API and file links
        if (href.indexOf('/api/') === 0 || href.indexOf('/files/') === 0 ||
            href.indexOf('/assets/') === 0) return;

        e.preventDefault();
        self.go(href);
      });

      // Handle browser back/forward
      window.addEventListener('popstate', function() {
        self.load(window.location.pathname, false);
      });
    },

    go: function(path) {
      if (this.loading || path === window.location.pathname) return;
      this.load(path, true);
    },

    load: function(path, pushState) {
      var self = this;
      this.loading = true;
      var thisLoad = ++this._loadId;
      var main = document.querySelector('main');
      if (main) main.classList.add('page-loading');

      app.set('page:loading', true);

      fetch('/api/page?path=' + encodeURIComponent(path))
        .then(function(res) {
          if (res.status === 404) {
            return fetch('/api/page?path=/404').then(function(r) { return r.json(); });
          }
          return res.json();
        })
        .then(function(data) {
          // Discard stale response if a newer navigation started.
          if (thisLoad !== self._loadId) return;

          if (!data || data.error) {
            if (main) main.innerHTML = '<div class="alert alert-error" style="margin:2rem auto;max-width:600px;">Page not found.</div>';
            return;
          }

          // Set route params before page scripts run (e.g. {id: "4"} for /thread/:id)
          window.__routeParams = data.params || {};

          // Clean up previous page assets
          self.removePageAssets();

          // Swap content
          if (main) main.innerHTML = data.content || '';

          // Update title
          document.title = data.title ? data.title + ' | ' + (document.title.split(' | ').pop()) : document.title;

          // Load page-scoped CSS (wait for all sheets before running scripts)
          var cssReady = Promise.resolve();
          if (data.page_css) {
            var cssPromises = data.page_css.map(function(url) { return self.loadCSS(url); });
            cssReady = Promise.all(cssPromises);
          }

          // Load page-scoped JS after CSS is ready
          cssReady.then(function() {
            if (data.page_js) {
              var chain = Promise.resolve();
              data.page_js.forEach(function(url) {
                chain = chain.then(function() { return self.loadJS(url); });
              });
              chain.then(function() { self.evalInlineScripts(main); });
            } else {
              self.evalInlineScripts(main);
            }
          });

          // Push history
          if (pushState) {
            window.history.pushState(null, '', path);
          }

          // Update active nav link
          self.updateNav(path);

          // Notify listeners
          app.set('page', path);
          app.set('page:loading', false);
        })
        .catch(function() {
          if (main) main.innerHTML = '<div class="alert alert-error" style="margin:2rem auto;max-width:600px;">Connection lost. Please try again.</div>';
          app.set('page:loading', false);
        })
        .finally(function() {
          self.loading = false;
          if (main) main.classList.remove('page-loading');
        });
    },

    removePageAssets: function() {
      var els = document.querySelectorAll('[data-page-asset]');
      for (var i = 0; i < els.length; i++) els[i].remove();
    },

    loadCSS: function(url) {
      return new Promise(function(resolve) {
        var link = document.createElement('link');
        link.rel = 'stylesheet';
        link.href = url;
        link.setAttribute('data-page-asset', '');
        link.onload = resolve;
        link.onerror = resolve;
        document.head.appendChild(link);
      });
    },

    loadJS: function(url) {
      return new Promise(function(resolve) {
        var script = document.createElement('script');
        script.src = url;
        script.setAttribute('data-page-asset', '');
        script.onload = resolve;
        script.onerror = resolve;
        document.body.appendChild(script);
      });
    },

    evalInlineScripts: function(container) {
      if (!container) return;
      var scripts = container.querySelectorAll('script:not([src])');
      for (var i = 0; i < scripts.length; i++) {
        var oldScript = scripts[i];
        var newScript = document.createElement('script');
        newScript.textContent = oldScript.textContent;
        // Copy attributes
        for (var j = 0; j < oldScript.attributes.length; j++) {
          newScript.setAttribute(oldScript.attributes[j].name, oldScript.attributes[j].value);
        }
        oldScript.parentNode.replaceChild(newScript, oldScript);
      }
    },

    updateNav: function(path) {
      var links = document.querySelectorAll('nav a[href]');
      for (var i = 0; i < links.length; i++) {
        var href = links[i].getAttribute('href');
        if (href === path || (path !== '/' && href !== '/' && path.indexOf(href) === 0)) {
          links[i].classList.add('active');
        } else {
          links[i].classList.remove('active');
        }
      }
    }
  };

  window.__iatan_router = router;
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', function() { router.init(); });
  } else {
    router.init();
  }

  // Set initial active nav
  router.updateNav(window.location.pathname);
})();
