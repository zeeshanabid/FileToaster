<script lang="ts">
  import { createEventDispatcher } from 'svelte';
  import { auth, changePassword } from '$lib/store/auth';

  let { open }: { open: boolean } = $props();

  const dispatch = createEventDispatcher<{ close: void }>();

  let current = $state('');
  let newPassword = $state('');
  let confirmPassword = $state('');
  let currentInput = $state<HTMLInputElement | undefined>();
  let busy = $state(false);
  // feedback holds a client-side message (match / minimum-length / success).
  // Server errors come through $auth.error instead. A fixed-height wrap keeps
  // the dialog from shifting when a message appears or clears.
  let feedback = $state<{ kind: 'success' | 'error' | ''; text: string }>({ kind: '', text: '' });

  function setFeedback(kind: 'success' | 'error' | '', text: string) {
    feedback = { kind, text };
  }

  function openDialog() {
    reset();
    // Focus the current-password field so typing can start immediately.
    requestAnimationFrame(() => currentInput?.focus());
  }

  function reset() {
    current = '';
    newPassword = '';
    confirmPassword = '';
    feedback = { kind: '', text: '' };
    busy = false;
  }

  // Keep the dialog in sync with the `open` prop from the parent.
  $effect(() => {
    if (open) openDialog();
  });

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    if (busy) return;
    if (newPassword !== confirmPassword) {
      setFeedback('error', 'New passwords do not match');
      return;
    }
    if (newPassword.length < 5) {
      setFeedback('error', 'Password must be at least 5 characters');
      return;
    }
    setFeedback('', '');
    busy = true;
    const ok = await changePassword(current, newPassword);
    busy = false;
    if (ok) {
      reset();
      setFeedback('success', 'Password changed successfully');
    }
  }

  function close() {
    reset();
    dispatch('close');
  }
</script>

<svelte:window onkeydown={(e) => { if (e.key === 'Escape') close(); }} />

{#if open}
  <div class="overlay" role="dialog" aria-modal="true" aria-labelledby="cp-title" tabindex="-1" onkeydown={(e) => e.key === 'Escape' && close()} onclick={close}>
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
  <form class="dialog" onsubmit={submit} onclick={(e) => e.stopPropagation()}>
      <header class="dlg-header">
        <h2 id="cp-title" class="dlg-title">Change password</h2>
        <button type="button" class="dlg-close" aria-label="Close" onclick={close}>
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" width="100%" height="100%">
            <path d="M7 7l10 10M17 7L7 17" />
          </svg>
        </button>
      </header>

      <div class="error-wrap">
        {#if $auth.error}
          <p class="error" role="alert" aria-live="polite">{$auth.error}</p>
        {/if}
        {#if feedback.kind === 'error'}
          <p class="error" role="alert" aria-live="polite">{feedback.text}</p>
        {/if}
        {#if feedback.kind === 'success'}
          <p class="success" role="status" aria-live="polite">{feedback.text}</p>
        {/if}
      </div>

      <div class="dlg-body">
        <div class="field">
          <label for="cp-current">Current password</label>
          <input id="cp-current" bind:this={currentInput} type="password" bind:value={current} autocomplete="current-password" class="text-input" />
        </div>
        <div class="field">
          <label for="cp-new">New password</label>
          <input id="cp-new" type="password" bind:value={newPassword} autocomplete="new-password" class="text-input" />
        </div>
        <div class="field">
          <label for="cp-confirm">Confirm new password</label>
          <input id="cp-confirm" type="password" bind:value={confirmPassword} autocomplete="new-password" class="text-input" />
        </div>
      </div>

      <p class="warning">
        If your admin password was set using the <code>FILETOASTER_ADMIN_PASSWORD</code> environment variable, this
        change is written to the config file but will not take effect — the environment variable takes precedence.
        Remove the variable (or set <code>admin_password_hash</code> in the config) and restart the server.
      </p>

      <footer class="dlg-footer">
        <button type="button" class="dlg-cancel" onclick={close}>Cancel</button>
        {#if feedback.kind === 'success'}
          <button type="button" class="dlg-apply" onclick={close}>Done</button>
        {:else}
          <button type="submit" disabled={busy} class="dlg-apply">
            {busy ? 'Changing…' : 'Change password'}
          </button>
        {/if}
      </footer>
    </form>
  </div>
{/if}

<style>
  .overlay {
    position: fixed;
    inset: 0;
    display: grid;
    place-items: center;
    background: rgba(11, 17, 32, 0.5);
    backdrop-filter: blur(2px);
    z-index: 1000;
    padding: 24px;
  }
  .dialog {
    display: flex;
    flex-direction: column;
    width: 100%;
    max-width: 504px;
    max-height: 100%;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 14px;
    box-shadow: var(--shadow-popover);
    overflow: hidden;
    outline: none;
  }
  .dlg-header {
    display: flex;
    align-items: center;
    gap: 10px;
    height: 48px;
    padding: 0 12px;
    background: var(--surface-soft);
    border-bottom: 1px solid var(--border);
    flex: none;
  }
  .dlg-title {
    font-size: 14px;
    font-weight: 600;
    color: var(--text);
    margin: 0;
  }
  .dlg-close {
    display: grid;
    place-items: center;
    margin-left: auto;
    width: 30px;
    height: 30px;
    background: transparent;
    border: none;
    border-radius: 8px;
    color: var(--text-muted);
    cursor: pointer;
  }
  .dlg-close:hover {
    background: var(--alert);
    color: #fff;
  }
  .dlg-close svg {
    width: 14px;
    height: 14px;
  }
  .error-wrap {
    flex: none;
    min-height: 30px;
    padding: 2px 16px;
  }
  .error {
    margin: 0;
    font-size: 13px;
    color: var(--alert-strong);
    background: rgba(244, 63, 94, 0.10);
    border: 1px solid var(--alert);
    border-radius: 10px;
    padding: 8px 12px;
  }
  .success {
    margin: 0;
    font-size: 13px;
    color: #047857;
    background: rgba(16, 185, 129, 0.14);
    border: 1px solid var(--ok-strong);
    border-radius: 10px;
    padding: 8px 12px;
  }
  .dlg-body {
    flex: 1;
    min-height: 0;
    overflow: auto;
    padding: 8px 16px 14px;
  }
  .field {
    display: flex;
    flex-direction: column;
    gap: 6px;
    margin: 10px 0;
    font-size: 13px;
    color: var(--text-muted);
  }
  .field:first-child {
    margin-top: 0;
  }
  .field label {
    font-weight: 600;
  }
  .field .text-input {
    width: 100%;
  }
  .text-input {
    height: 30px;
    padding: 0 8px;
    font: inherit;
    font-size: 13px;
    color: var(--text);
    background: var(--surface);
    border: 1px solid var(--accent);
    border-radius: 7px;
    outline: none;
    min-width: 0;
  }
  .text-input:focus {
    border-color: var(--accent-strong);
  }
  .warning {
    flex: none;
    margin: 0 16px;
    padding: 12px 14px;
    font-size: 13px;
    line-height: 1.5;
    color: var(--text);
    background: rgba(251, 191, 36, 0.16);
    border: 1px solid var(--warn);
    border-radius: 10px;
  }
  .warning code {
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
    font-size: 11px;
    color: var(--accent-strong);
    background: rgba(129, 140, 248, 0.16);
    padding: 1px 5px;
    border-radius: 5px;
  }
  .dlg-footer {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    padding: 10px 12px;
    border-top: 1px solid var(--border);
    background: var(--surface-soft);
    flex: none;
  }
  .dlg-cancel {
    height: 32px;
    padding: 0 18px;
    font: inherit;
    font-size: 13px;
    font-weight: 600;
    color: #0f172a;
    background: #f1f5f9;
    border: 1px solid #94a3b8;
    border-radius: 8px;
    cursor: pointer;
    transition: background-color 0.15s ease, border-color 0.15s ease, color 0.15s ease, box-shadow 0.15s ease;
  }
  .dlg-cancel:hover {
    color: #0f172a;
    border-color: #818cf8;
    background: #e2e8f0;
    box-shadow: 0 1px 4px rgba(99, 102, 241, 0.15);
  }
  .dlg-cancel:active {
    transform: scale(0.98);
    background: #cbd5e1;
  }
  .dlg-apply {
    height: 32px;
    padding: 0 18px;
    font: inherit;
    font-size: 13px;
    font-weight: 600;
    color: #fff;
    background: var(--accent);
    border: 1px solid var(--accent);
    border-radius: 8px;
    cursor: pointer;
    transition: background-color 0.15s ease, border-color 0.15s ease, box-shadow 0.15s ease;
  }
  .dlg-apply:hover:not(:disabled) {
    background: var(--accent-strong);
    border-color: var(--accent-strong);
    box-shadow: 0 2px 8px rgba(var(--accent-rgb), 0.3);
  }
  .dlg-apply:active:not(:disabled) {
    transform: scale(0.98);
  }
  .dlg-apply:disabled {
    opacity: 0.5;
    cursor: not-allowed;
    transform: none;
  }
</style>
