window.userTimezone = 'UTC';
(function() {
  function initTimezone() {
    window.userTimezone = (document.body && document.body.dataset.timezone) || 'UTC';
  }
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', initTimezone);
  } else {
    initTimezone();
  }
})();
window._i18nMessages = {};

// Load authoritative English dictionary for the English locale fallback.
// The inline seed has been removed; en.json is now the single source of truth.
(function() {
  var xhr = new XMLHttpRequest();
  xhr.open('GET', '/i18n/en.json', false); // sync load
  try {
    xhr.send();
    if (xhr.status === 200) {
      var extra = JSON.parse(xhr.responseText) || {};
      for (var k in extra) {
        if (extra.hasOwnProperty(k)) {
          window._i18nMessages[k] = extra[k];
        }
      }
    }
  } catch(e) { /* ignore - fallback to inline seed */ }
})();

// Load Chinese dictionary (English keys -> Chinese values).
(function() {
  window._i18nMessagesZh = {};
  var xhr = new XMLHttpRequest();
  xhr.open('GET', '/i18n/zh.json', false); // sync load
  try {
    xhr.send();
    if (xhr.status === 200) {
      window._i18nMessagesZh = JSON.parse(xhr.responseText) || {};
    }
  } catch(e) { /* ignore - Chinese translation is optional */ }
})();

(function loadProperNouns() {
  window._properNouns = {};
  var xhr = new XMLHttpRequest();
  xhr.open('GET', '/static/js/proper-nouns.json', false);
  try {
    xhr.send();
    if (xhr.status === 200) {
      var data = JSON.parse(xhr.responseText);
      var nouns = data && data.nouns ? data.nouns : {};
      for (var term in nouns) {
        if (nouns.hasOwnProperty(term)) {
          window._properNouns[term] = true;
        }
      }
    }
  } catch(e) { /* ignore - proper noun guard is optional */ }
})();

function _t(text) {
    if (window._properNouns && window._properNouns[text]) return text;
    var lang = document.documentElement.lang || 'zh';
    if (lang === 'en') {
      var msg = window._i18nMessages[text];
      if (msg === undefined) console.warn('[i18n] missing en:', text);
      return msg !== undefined ? msg : text;
    }
    var zh = window._i18nMessagesZh[text];
    if (zh === undefined) console.warn('[i18n] missing zh:', text);
    return zh !== undefined ? zh : text;
}

    // Theme sync to server (only called on user action, not page load)
    function syncTheme(theme) {
      const csrf = document.querySelector('meta[name="csrf-token"]')?.content || '';
      fetch('/theme', {
        method: 'POST',
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        body: 'theme=' + encodeURIComponent(theme) + '&csrf_token=' + encodeURIComponent(csrf)
      }).catch(() => {});
    }

    // Placeholder for executeGlobalConfirm — overridden by footer script
    function executeGlobalConfirm() {}

    // Apply theme: converts 'system' to actual theme based on OS preference
    function applyTheme(theme) {
      var actual = theme;
      if (theme === 'system') {
        actual = window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
      }
      document.documentElement.setAttribute('data-theme', actual);
      document.documentElement.setAttribute('data-theme-preference', theme);
      localStorage.setItem('theme', theme);
    }

    // Listen for OS theme changes when in system mode
    window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', function(e) {
      var pref = localStorage.getItem('theme') || 'system';
      if (pref === 'system') {
        applyTheme('system');
      }
    });

    // Toast notification system
    function showToast(type, message) {
      const container = document.getElementById('toast-container');
      if (!container) return;
      const el = document.createElement('div');
      el.className = 'toast toast-' + type;
      el.textContent = _t(message);
      container.appendChild(el);
      requestAnimationFrame(() => el.classList.add('toast-show'));
      setTimeout(() => {
        el.classList.add('toast-exit');
        setTimeout(() => el.remove(), 300);
      }, 4000);
    }

    document.addEventListener('DOMContentLoaded', function() {
      const toastData = document.getElementById('toast-data');
      if (toastData && toastData.dataset.toastType) {
        showToast(toastData.dataset.toastType, toastData.dataset.toastMessage);
        // Clean up query params from URL so refresh doesn't re-show toast
        if (window.history.replaceState) {
          const url = new URL(window.location);
          url.searchParams.delete('toast');
          url.searchParams.delete('message');
          window.history.replaceState({}, '', url);
        }
      }
    });

    // Listen for HTMX-triggered toast events (message is URL-encoded for header safety)
    document.addEventListener('showToast', function(evt) {
      const detail = evt.detail || {};
      showToast(detail.type, decodeURIComponent(detail.message));
    });

    // Scroll position preservation
    (function() {
      var scrollKey = 'scrollPos_' + window.location.pathname;
      window.addEventListener('beforeunload', function() {
        sessionStorage.setItem(scrollKey, window.scrollY);
      });
      window.addEventListener('load', function() {
        var savedPos = sessionStorage.getItem(scrollKey);
        if (savedPos !== null) {
          setTimeout(function() {
            window.scrollTo(0, parseInt(savedPos, 10));
          }, 0);
        }
      });
    })();

    // Sidebar scroll position preservation - prevent auto-scroll to top
    // Must run after DOM is ready and use a more aggressive approach
    (function() {
      var sidebarScrollKey = 'sidebarScrollPos';
      var restoreAttempts = 0;
      var maxRestoreAttempts = 10;

      function restoreSidebarScroll() {
        var sidebar = document.getElementById('sidebar');
        if (!sidebar) {
          // Sidebar not found yet, try again
          if (restoreAttempts < maxRestoreAttempts) {
            restoreAttempts++;
            setTimeout(restoreSidebarScroll, 50);
          }
          return;
        }

        var savedSidebarPos = sessionStorage.getItem(sidebarScrollKey);
        if (savedSidebarPos !== null) {
          sidebar.scrollTop = parseInt(savedSidebarPos, 10);
          // Also set via scrollTo for reliability
          sidebar.scrollTo(0, parseInt(savedSidebarPos, 10));
        }

        // Save sidebar scroll position on scroll (debounced)
        var scrollTimeout;
        sidebar.addEventListener('scroll', function() {
          clearTimeout(scrollTimeout);
          scrollTimeout = setTimeout(function() {
            sessionStorage.setItem(sidebarScrollKey, sidebar.scrollTop);
          }, 100);
        });
      }

      // Run immediately if DOM ready, otherwise wait
      if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', restoreSidebarScroll);
      } else {
        restoreSidebarScroll();
      }

      // Also save on page unload
      window.addEventListener('beforeunload', function() {
        var sidebar = document.getElementById('sidebar');
        if (sidebar) {
          sessionStorage.setItem(sidebarScrollKey, sidebar.scrollTop);
        }
      });

      // Save on click events (for navigation links)
      document.addEventListener('click', function(e) {
        var sidebar = document.getElementById('sidebar');
        if (sidebar) {
          sessionStorage.setItem(sidebarScrollKey, sidebar.scrollTop);
        }
      }, true); // Use capture phase to save before navigation
    })();

  var _pendingConfirmCallback = null;

  function closeGlobalModal(id) {
    document.getElementById(id).style.display = 'none';
  }

  function showMessage(title, text, callback) {
    document.getElementById('global-message-title').textContent = _t(title || 'Notice');
    document.getElementById('global-message-text').textContent = _t(text || '');
    document.getElementById('global-message-modal').style.display = 'flex';
    if (callback) {
      document.getElementById('global-message-modal').querySelector('.modal-close').onclick = function() {
        closeGlobalModal('global-message-modal');
        callback();
      };
      document.getElementById('global-message-modal').querySelector('.btn-primary').onclick = function() {
        closeGlobalModal('global-message-modal');
        callback();
      };
    }
  }

  function showConfirm(title, text, callback) {
    document.getElementById('global-confirm-title').textContent = _t(title || 'Confirm');
    document.getElementById('global-confirm-text').textContent = _t(text || '');
    _pendingConfirmCallback = callback;
    document.getElementById('global-confirm-modal').style.display = 'flex';
  }

  // Right panel functions
  function openRightPanel(title, html) {
    var panelTitle = document.getElementById('panel-title');
    var panelBody = document.getElementById('panel-body');
    if (panelTitle) panelTitle.textContent = title;
    if (panelBody) panelBody.innerHTML = html;
    var layout = document.querySelector('.app-layout');
    if (layout) layout.classList.add('has-right-panel');
  }
  function closeRightPanel() {
    var layout = document.querySelector('.app-layout');
    if (layout) layout.classList.remove('has-right-panel');
  }

  function executeGlobalConfirm() {
    closeGlobalModal('global-confirm-modal');
    if (_pendingConfirmCallback) {
      _pendingConfirmCallback();
      _pendingConfirmCallback = null;
    }
  }

  // ── Announcement system (banner / modal / wizard) ──
  var _currentWizardAnn = null;

  function showAnnouncementPopup(ann) {
    document.getElementById('ann-popup-title').textContent = ann.title || _t('Announcement');
    document.getElementById('ann-popup-content').innerHTML = (ann.content || '').replace(/\n/g, '<br>');
    document.getElementById('announcement-popup-modal').style.display = 'flex';
  }

  function closeAnnouncementPopup() {
    document.getElementById('announcement-popup-modal').style.display = 'none';
  }

  // ── Banner rendering ──
  function renderBanners(banners) {
    var area = document.getElementById('promo-banner-area');
    if (!area) return;
    area.innerHTML = '';
    banners.forEach(function(ann) {
      // Darker backgrounds so white text meets WCAG AA contrast (≥4.5:1)
      var colorMap = {primary:'#1d4ed8', success:'#047857', warning:'#b45309', danger:'#b91c1c'};
      var bgColor = colorMap[ann.banner_color] || colorMap.primary;
      var div = document.createElement('div');
      div.setAttribute('data-announcement-id', ann.id);
      div.style.cssText = 'display:flex;align-items:center;justify-content:space-between;padding:10px 24px;font-size:14px;font-weight:500;background:' + bgColor + ';color:#fff;gap:12px;animation:promoBannerSlideDown 0.3s ease;';
      div.className = 'promo-banner promo-banner--' + (ann.banner_color || 'primary');
      var html = '<div style="display:flex;align-items:center;gap:8px;flex:1;min-width:0">';
      if (ann.icon_emoji) html += '<span style="font-size:18px;flex-shrink:0">' + ann.icon_emoji + '</span>';
      html += '<span style="flex:1;min-width:0">' + escapeHtml(ann.title);
      if (ann.content) html += ' — ' + escapeHtml(ann.content);
      html += '</span></div>';
      html += '<div style="display:flex;align-items:center;gap:8px;flex-shrink:0">';
      if (ann.cta_text && ann.cta_link) {
        html += '<a href="' + escapeHtml(ann.cta_link) + '" style="background:rgba(255,255,255,0.2);color:inherit;padding:4px 14px;border-radius:4px;text-decoration:none;font-weight:600;font-size:13px;white-space:nowrap">' + escapeHtml(ann.cta_text) + '</a>';
      }
      html += '<button style="background:none;border:none;color:inherit;opacity:0.7;font-size:20px;cursor:pointer;line-height:1;padding:0 4px" onclick="dismissBanner(\'' + ann.id + '\')">&times;</button>';
      html += '</div>';
      div.innerHTML = html;
      area.appendChild(div);
    });
    // Adjust spacer height so fixed banner doesn't overlap content
    var spacer = document.getElementById('promo-banner-spacer');
    if (spacer && banners.length > 0) {
      spacer.style.display = '';
      spacer.style.height = area.offsetHeight + 'px';
    }
  }

  function escapeHtml(str) {
    if (!str) return '';
    var d = document.createElement('div');
    d.textContent = str;
    return d.innerHTML;
  }

  function dismissBanner(announcementId) {
    var banner = document.querySelector('[data-announcement-id="' + announcementId + '"]');
    if (banner) {
      banner.style.transition = 'transform 0.3s, opacity 0.3s';
      banner.style.transform = 'translateY(-100%)';
      banner.style.opacity = '0';
      setTimeout(function() {
        banner.remove();
        // Update spacer height or hide if no banners left
        var area = document.getElementById('promo-banner-area');
        var spacer = document.getElementById('promo-banner-spacer');
        if (spacer) {
          if (area && area.children.length > 0) {
            spacer.style.height = area.offsetHeight + 'px';
          } else {
            spacer.style.display = 'none';
          }
        }
      }, 300);
    }
    // Mark as read on server
    fetch('/api/announcements/' + announcementId + '/read', {
      method: 'POST', credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' }
    }).catch(function() {});
    // Session-level dismiss
    var dismissed = JSON.parse(sessionStorage.getItem('dismissed_banners') || '[]');
    if (dismissed.indexOf(announcementId) === -1) dismissed.push(announcementId);
    sessionStorage.setItem('dismissed_banners', JSON.stringify(dismissed));
  }

  function isBannerDismissed(ann) {
    if (ann.dismiss_mode === 'always') return false;
    var dismissed = JSON.parse(sessionStorage.getItem('dismissed_banners') || '[]');
    return dismissed.indexOf(ann.id) !== -1;
  }

  // ── Onboarding wizard ──
  function showOnboardingWizard(ann) {
    _currentWizardAnn = ann;
    document.getElementById('wizard-title').textContent = ann.title || '';
    // Render content: try JSON (two-column plans) or plain text
    var body = document.getElementById('wizard-body');
    try {
      var plans = JSON.parse(ann.content);
      if (Array.isArray(plans) && plans.length >= 1) {
        var html = '<div class="onboarding-wizard__plans">';
        plans.forEach(function(p, i) {
          var popular = p.popular ? ' onboarding-wizard__plan--popular' : '';
          html += '<div class="onboarding-wizard__plan' + popular + '">';
          html += '<div class="onboarding-wizard__plan-title">' + escapeHtml(p.title) + '</div>';
          if (p.features && p.features.length) {
            html += '<ul class="onboarding-wizard__plan-features">';
            p.features.forEach(function(f) { html += '<li>' + escapeHtml(f) + '</li>'; });
            html += '</ul>';
          }
          if (p.cta_text && p.cta_link) {
            var btnClass = i === 0 ? 'onboarding-wizard__plan-btn onboarding-wizard__plan-btn--primary' : 'onboarding-wizard__plan-btn onboarding-wizard__plan-btn--secondary';
            html += '<a href="' + escapeHtml(p.cta_link) + '" class="' + btnClass + '">' + escapeHtml(p.cta_text) + '</a>';
          }
          html += '</div>';
        });
        html += '</div>';
        body.innerHTML = html;
      } else {
        throw new Error('not plans array');
      }
    } catch(e) {
      // Fallback: plain text content
      body.innerHTML = '<div style="line-height:1.7">' + (ann.content || '').replace(/\n/g, '<br>') + '</div>';
    }
    document.getElementById('wizard-dont-remind').checked = false;
    document.getElementById('onboarding-wizard-modal').style.display = 'flex';
  }

  function closeOnboardingWizard() {
    document.getElementById('onboarding-wizard-modal').style.display = 'none';
    if (_currentWizardAnn && document.getElementById('wizard-dont-remind').checked) {
      // Permanent dismiss: mark as read on server
      fetch('/api/announcements/' + _currentWizardAnn.id + '/read', {
        method: 'POST', credentials: 'same-origin',
        headers: { 'Content-Type': 'application/json' }
      }).catch(function() {});
    }
    _currentWizardAnn = null;
  }

  // ── Unified announcement loader ──
  function loadAndRenderAnnouncements() {
    var seen = JSON.parse(localStorage.getItem('seen_announcements') || '[]');
    fetch('/api/announcements', { credentials: 'same-origin' }).then(function(r) { return r.json(); }).then(function(data) {
      var announcements = data.announcements || [];

      // 1. Render Banners (display_mode=banner, not dismissed, not promo on admin pages)
      var isAdminPage = window.location.pathname.startsWith('/admin');
      var banners = announcements.filter(function(a) {
        if (a.display_mode !== 'banner' || isBannerDismissed(a)) return false;
        if (a.type === 'promo' && isAdminPage) return false;
        return true;
      });
      renderBanners(banners);

      // 2. Render Modal popups (display_mode=modal or show_popup, not wizard/banner)
      var popups = announcements.filter(function(a) {
        return a.show_popup && a.display_mode !== 'banner' && a.display_mode !== 'wizard'
          && seen.indexOf(a.id) === -1;
      });
      if (popups.length > 0) {
        showAnnouncementPopup(popups[0]);
        seen.push(popups[0].id);
        localStorage.setItem('seen_announcements', JSON.stringify(seen));
      }

      // 3. Render Wizard (display_mode=wizard, only once per session)
      var wizardShown = sessionStorage.getItem('wizard_shown');
      var wizards = announcements.filter(function(a) {
        return a.display_mode === 'wizard' && !wizardShown;
      });
      if (wizards.length > 0) {
        showOnboardingWizard(wizards[0]);
        sessionStorage.setItem('wizard_shown', '1');
      }
    }).catch(function() {});
  }

  // Load announcements on page load for authenticated users
  function initAnnouncements() {
    if (document.body && document.body.dataset.isAuth === 'true') {
      setTimeout(loadAndRenderAnnouncements, 500);
    }
  }
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', initAnnouncements);
  } else {
    initAnnouncements();
  }

	// Global handler: catch unhandled fetch() rejections across all pages
	window.addEventListener('unhandledrejection', function(event) {
		console.error('Unhandled fetch error:', event.reason);
		showToast('error', _t('Operation failed: ') + (event.reason?.message || _t('Network error, please retry')));
		event.preventDefault();
	});
    // Sidebar collapse state (localStorage)
  function initSidebarCollapse() {
    var sidebar = document.getElementById('sidebar');
    var appLayout = document.querySelector('.app-layout');
    var collapseBtn = document.getElementById('sidebar-collapse-btn-footer');
    if (!sidebar) return;

    function setCollapsed(collapsed) {
      if (collapsed) {
        sidebar.classList.add('collapsed');
        if (appLayout) appLayout.classList.add('sidebar-collapsed');
        if (collapseBtn) collapseBtn.setAttribute('aria-expanded', 'false');
        sidebar.scrollTop = 0;
      } else {
        sidebar.classList.remove('collapsed');
        if (appLayout) appLayout.classList.remove('sidebar-collapsed');
        if (collapseBtn) collapseBtn.setAttribute('aria-expanded', 'true');
      }
      localStorage.setItem('sidebarCollapsed', collapsed ? 'true' : 'false');
    }

    var saved = localStorage.getItem('sidebarCollapsed') === 'true';
    if (saved) {
      setCollapsed(true);
      sidebar.scrollTop = 0;
    }

    // Fixed tooltip for collapsed sidebar (bypasses overflow clipping)
    var fixedTooltip = document.getElementById('sidebar-fixed-tooltip');
    if (fixedTooltip) {
      function showFixedTooltip(link, title) {
        var rect = link.getBoundingClientRect();
        fixedTooltip.textContent = title;
        fixedTooltip.classList.add('visible');
        var top = rect.top + rect.height / 2 - fixedTooltip.offsetHeight / 2;
        var left = rect.right + 8;
        fixedTooltip.style.top = Math.max(8, top) + 'px';
        fixedTooltip.style.left = left + 'px';
      }
      function hideFixedTooltip() {
        fixedTooltip.classList.remove('visible');
      }
      sidebar.addEventListener('mouseover', function(e) {
        if (!sidebar.classList.contains('collapsed')) return;
        var link = e.target.closest('.sidebar-nav a');
        if (!link) return;
        var title = link.getAttribute('data-title');
        if (title) showFixedTooltip(link, title);
      });
      sidebar.addEventListener('mouseout', function(e) {
        var link = e.target.closest('.sidebar-nav a');
        if (link) hideFixedTooltip();
      });
      sidebar.addEventListener('click', function() {
        hideFixedTooltip();
      });
    }

    if (collapseBtn) collapseBtn.addEventListener('click', function() {
      setCollapsed(!sidebar.classList.contains('collapsed'));
    });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', initSidebarCollapse);
  } else {
    initSidebarCollapse();
  }
