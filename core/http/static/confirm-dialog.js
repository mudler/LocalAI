/**
 * Custom confirm dialog replacement for browser default confirm().
 * Uses the project's design system CSS variables for consistent styling.
 */
(function() {
  'use strict';

  var overlay = null;
  var dialog = null;

  function ensureDOM() {
    if (overlay) return;

    overlay = document.createElement('div');
    overlay.id = 'confirm-dialog-overlay';
    overlay.style.cssText = 'display:none;position:fixed;inset:0;z-index:9999;background:rgba(0,0,0,0.5);justify-content:center;align-items:center;padding:1rem;';

    dialog = document.createElement('div');
    dialog.setAttribute('role', 'alertdialog');
    dialog.setAttribute('aria-modal', 'true');
    dialog.style.cssText = 'width:100%;max-width:28rem;border-radius:var(--radius-lg,0.75rem);background:var(--color-bg-secondary,#1e1e2e);border:1px solid var(--color-border-subtle,#2a2a3a);box-shadow:var(--shadow-lg,0 10px 25px rgba(0,0,0,0.3));animation:confirmFadeIn 0.15s ease-out;';

    dialog.innerHTML =
      '<div style="padding:1.25rem 1.5rem;border-bottom:1px solid var(--color-border-subtle,#2a2a3a);display:flex;align-items:center;gap:0.75rem;">' +
        '<div id="confirm-dialog-icon" style="flex-shrink:0;width:2rem;height:2rem;border-radius:var(--radius-full,9999px);display:flex;align-items:center;justify-content:center;font-size:0.875rem;"></div>' +
        '<h3 id="confirm-dialog-title" style="margin:0;font-size:1rem;font-weight:600;color:var(--color-text-primary,#e0e0e0);font-family:var(--font-body,sans-serif);"></h3>' +
      '</div>' +
      '<div style="padding:1rem 1.5rem;">' +
        '<p id="confirm-dialog-message" style="margin:0;font-size:0.875rem;color:var(--color-text-secondary,#a0a0b0);font-family:var(--font-body,sans-serif);line-height:1.5;"></p>' +
      '</div>' +
      '<div style="padding:0.75rem 1.5rem 1.25rem;display:flex;justify-content:flex-end;gap:0.75rem;">' +
        '<button id="confirm-dialog-cancel" style="padding:0.5rem 1rem;font-size:0.8125rem;font-weight:500;font-family:var(--font-body,sans-serif);border-radius:var(--radius-md,0.5rem);border:1px solid var(--color-border-default,#3a3a4a);background:transparent;color:var(--color-text-secondary,#a0a0b0);cursor:pointer;transition:all 0.15s ease;"></button>' +
        '<button id="confirm-dialog-confirm" style="padding:0.5rem 1rem;font-size:0.8125rem;font-weight:500;font-family:var(--font-body,sans-serif);border-radius:var(--radius-md,0.5rem);border:none;color:#fff;cursor:pointer;transition:all 0.15s ease;"></button>' +
      '</div>';

    overlay.appendChild(dialog);
    document.body.appendChild(overlay);

    // Add animation keyframes
    var style = document.createElement('style');
    style.textContent =
      '@keyframes confirmFadeIn{from{opacity:0;transform:scale(0.95)}to{opacity:1;transform:scale(1)}}' +
      '#confirm-dialog-cancel:hover{background:var(--color-bg-primary,#161622);color:var(--color-text-primary,#e0e0e0);}' +
      '#confirm-dialog-cancel:focus,#confirm-dialog-confirm:focus{outline:none;box-shadow:0 0 0 2px var(--color-border-focus,rgba(99,102,241,0.5));}';
    document.head.appendChild(style);
  }

  /**
   * Show a confirm dialog.
   *
   * @param {Object|string} options - Options object or message string.
   * @param {string} options.title - Dialog title (default: "Confirm").
   * @param {string} options.message - Dialog message.
   * @param {string} options.confirmText - Confirm button text (default: "Confirm").
   * @param {string} options.cancelText - Cancel button text (default: "Cancel").
   * @param {string} options.variant - "danger" or "default" (default: "default").
   * @param {Function} options.onConfirm - Callback when confirmed.
   * @param {Function} options.onCancel - Callback when cancelled.
   * @returns {Promise<boolean>} Resolves true if confirmed, false if cancelled.
   */
  window.confirmDialog = function(options) {
    if (typeof options === 'string') {
      options = { message: options };
    }

    var title = options.title || 'Confirm';
    var message = options.message || 'Are you sure?';
    var confirmText = options.confirmText || 'Confirm';
    var cancelText = options.cancelText || 'Cancel';
    var variant = options.variant || 'default';
    var onConfirm = options.onConfirm;
    var onCancel = options.onCancel;

    ensureDOM();

    // Set content
    document.getElementById('confirm-dialog-title').textContent = title;
    document.getElementById('confirm-dialog-message').textContent = message;
    document.getElementById('confirm-dialog-cancel').textContent = cancelText;
    document.getElementById('confirm-dialog-confirm').textContent = confirmText;

    // Set variant styling
    var iconEl = document.getElementById('confirm-dialog-icon');
    var confirmBtn = document.getElementById('confirm-dialog-confirm');
    if (variant === 'danger') {
      iconEl.innerHTML = '<i class="fa-solid fa-triangle-exclamation"></i>';
      iconEl.style.background = 'var(--color-error-light, rgba(239,68,68,0.1))';
      iconEl.style.color = 'var(--color-error, #ef4444)';
      confirmBtn.style.background = 'var(--color-error, #ef4444)';
    } else {
      iconEl.innerHTML = '<i class="fa-solid fa-circle-question"></i>';
      iconEl.style.background = 'var(--color-primary-light, rgba(99,102,241,0.1))';
      iconEl.style.color = 'var(--color-primary, #6366f1)';
      confirmBtn.style.background = 'var(--color-primary, #6366f1)';
    }

    // Show
    overlay.style.display = 'flex';
    dialog.style.animation = 'none';
    // Force reflow then re-apply animation
    dialog.offsetHeight;
    dialog.style.animation = 'confirmFadeIn 0.15s ease-out';

    // Focus the cancel button for safety
    document.getElementById('confirm-dialog-cancel').focus();

    return new Promise(function(resolve) {
      function close(result) {
        overlay.style.display = 'none';
        cleanup();
        if (result && onConfirm) onConfirm();
        if (!result && onCancel) onCancel();
        resolve(result);
      }

      function onConfirmClick() { close(true); }
      function onCancelClick() { close(false); }
      function onOverlayClick(e) { if (e.target === overlay) close(false); }
      function onKeydown(e) {
        if (e.key === 'Escape') { close(false); }
        // Trap focus within dialog
        if (e.key === 'Tab') {
          var cancelBtn = document.getElementById('confirm-dialog-cancel');
          var confBtn = document.getElementById('confirm-dialog-confirm');
          if (e.shiftKey && document.activeElement === cancelBtn) {
            e.preventDefault();
            confBtn.focus();
          } else if (!e.shiftKey && document.activeElement === confBtn) {
            e.preventDefault();
            cancelBtn.focus();
          }
        }
      }

      function cleanup() {
        document.getElementById('confirm-dialog-confirm').removeEventListener('click', onConfirmClick);
        document.getElementById('confirm-dialog-cancel').removeEventListener('click', onCancelClick);
        overlay.removeEventListener('click', onOverlayClick);
        document.removeEventListener('keydown', onKeydown);
      }

      document.getElementById('confirm-dialog-confirm').addEventListener('click', onConfirmClick);
      document.getElementById('confirm-dialog-cancel').addEventListener('click', onCancelClick);
      overlay.addEventListener('click', onOverlayClick);
      document.addEventListener('keydown', onKeydown);
    });
  };
})();
