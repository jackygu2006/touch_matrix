// i18n Engine
// ============================================================
(function() {
  'use strict';

  var STORAGE_KEY = 'nftouch_lang';
  var SUPPORTED = ['zh', 'en'];

  // Detect language
  var lang = localStorage.getItem(STORAGE_KEY);
  if (!lang) {
    lang = navigator.language && navigator.language.startsWith('zh') ? 'zh' : 'en';
  }
  if (SUPPORTED.indexOf(lang) === -1) lang = 'en';

  // URL query param override
  var qs = new URLSearchParams(location.search);
  if (SUPPORTED.indexOf(qs.get('lang')) !== -1) {
    lang = qs.get('lang');
    localStorage.setItem(STORAGE_KEY, lang);
  }

  var localeData = {};

  function loadLocale(l, callback) {
    var xhr = new XMLHttpRequest();
    xhr.open('GET', 'locales/' + l + '.json', true);
    xhr.onload = function() {
      if (xhr.status === 200) {
        try { localeData = JSON.parse(xhr.responseText); } catch(e) { localeData = {}; }
      }
      // Set html lang attribute
      document.documentElement.lang = l;
      if (callback) callback();
    };
    xhr.onerror = function() {
      document.documentElement.lang = 'en';
      if (callback) callback();
    };
    xhr.send();
  }

  // Global __() function
  window.__ = function(key) {
    var val = localeData[key];
    if (val === undefined || val === null) return key;
    // Support simple {0} {1} positional args
    var args = Array.prototype.slice.call(arguments, 1);
    if (args.length > 0) {
      return val.replace(/\{(\d+)\}/g, function(m, idx) {
        return args[parseInt(idx)] !== undefined ? args[parseInt(idx)] : m;
      });
    }
    return val;
  };

  // Apply translations to data-i18n elements (called after DOM ready)
  window.__applyI18n = function() {
    document.querySelectorAll('[data-i18n]').forEach(function(el) {
      var key = el.getAttribute('data-i18n');
      var text = __(key);
      if (text !== key) {
        if (el.tagName === 'INPUT' || el.tagName === 'TEXTAREA') {
          el.placeholder = text;
        } else {
          el.textContent = text;
        }
      }
    });
    document.querySelectorAll('[data-i18n-placeholder]').forEach(function(el) {
      var key = el.getAttribute('data-i18n-placeholder');
      var text = __(key);
      if (text !== key) el.placeholder = text;
    });
    document.querySelectorAll('[data-i18n-title]').forEach(function(el) {
      var key = el.getAttribute('data-i18n-title');
      var text = __(key);
      if (text !== key) el.title = text;
    });
  };

  // Language switcher
  window.__setLang = function(l) {
    if (SUPPORTED.indexOf(l) === -1) return;
    localStorage.setItem(STORAGE_KEY, l);
    lang = l;
    loadLocale(l, function() {
      window.__applyI18n();
      // Re-render dynamic UI
      if (typeof renderDeviceList === 'function') renderDeviceList();
      if (typeof updateDeviceStatus === 'function') updateDeviceStatus();
      if (typeof renderUserInfo === 'function') renderUserInfo();
      if (typeof buildGrid === 'function') buildGrid();
    });
  };

  // Load the locale and auto-apply when ready
  loadLocale(lang, function() {
    if (document.readyState === 'loading') {
      document.addEventListener('DOMContentLoaded', window.__applyI18n);
    } else {
      window.__applyI18n();
    }
  });
})();
