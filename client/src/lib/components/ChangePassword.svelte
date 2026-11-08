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
	<div class="scrim" role="dialog" aria-modal="true" aria-labelledby="cp-title" tabindex="-1" onkeydown={(e) => e.key === 'Escape' && close()} onclick={close}>
		<!-- The form stops clicks from bubbling to the backdrop close handler. -->
	<!-- svelte-ignore a11y_click_events_have_key_events -->
	<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
	<form class="dialog" onsubmit={submit} onclick={(e) => e.stopPropagation()}>
			<div class="header">
				<h2 id="cp-title" class="title">Change password</h2>
				<button type="button" class="icon-close" aria-label="Close" onclick={close}>
					<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" width="100%" height="100%">
						<path d="M6 6l12 12M18 6L6 18" />
					</svg>
				</button>
			</div>

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

			<div class="field">
				<label for="cp-current">Current password</label>
				<input id="cp-current" bind:this={currentInput} type="password" bind:value={current} autocomplete="current-password" />
			</div>
			<div class="field">
				<label for="cp-new">New password</label>
				<input id="cp-new" type="password" bind:value={newPassword} autocomplete="new-password" />
			</div>
			<div class="field">
				<label for="cp-confirm">Confirm new password</label>
				<input id="cp-confirm" type="password" bind:value={confirmPassword} autocomplete="new-password" />
			</div>

			<p class="warning">
				If your admin password was set using the <code>FILETOASTER_ADMIN_PASSWORD</code> environment variable, this
				change is written to the config file but will not take effect — the environment variable takes precedence.
				Remove the variable (or set <code>admin_password_hash</code> in the config) and restart the server.
			</p>

			<div class="actions">
				<button type="button" class="cancel" onclick={close}>Cancel</button>
				{#if feedback.kind === 'success'}
					<button type="button" class="apply" onclick={close}>Done</button>
				{:else}
					<button type="submit" disabled={busy} class="apply">
						{busy ? 'Changing…' : 'Change password'}
					</button>
				{/if}
			</div>
		</form>
	</div>
{/if}

<style>
	.scrim {
		position: fixed;
		inset: 0;
		z-index: 1000;
		display: grid;
		place-items: center;
		padding: 20px;
		background: rgba(2, 6, 23, 0.6);
		backdrop-filter: blur(4px);
		animation: cp-fade 0.15s ease-out;
	}
	@keyframes cp-fade {
		from {
			opacity: 0;
		}
	}
	.dialog {
		width: 100%;
		max-width: 380px;
		padding: 24px 24px 20px;
		background: rgba(15, 23, 42, 0.55);
		border: 1px solid var(--taskbar-border);
		border-radius: 20px;
		backdrop-filter: blur(16px);
		box-shadow: var(--shadow-popover);
		color: var(--taskbar-text);
		display: flex;
		flex-direction: column;
		gap: 14px;
	}
	.header {
		display: flex;
		align-items: center;
		justify-content: space-between;
	}
	.title {
		margin: 0;
		font-size: 18px;
		font-weight: 700;
		color: #fff;
	}
	.icon-close {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: 32px;
		height: 32px;
		padding: 0;
		background: transparent;
		border: none;
		border-radius: 9px;
		color: var(--taskbar-muted);
		cursor: pointer;
	}
	.icon-close:hover {
		background: var(--taskbar-hover);
		color: var(--taskbar-text);
	}
	.field {
		display: flex;
		flex-direction: column;
		gap: 6px;
		font-size: 13px;
		color: var(--taskbar-muted);
	}
	.field input {
		width: 100%;
		height: 40px;
		padding: 0 12px;
		background: var(--surface-inset);
		border: 1px solid var(--border-strong);
		border-radius: 10px;
		color: var(--text);
		font-size: 15px;
	}
	.field input:focus {
		outline: none;
		border-color: var(--accent);
		box-shadow: 0 0 0 3px rgba(129, 140, 248, 0.25);
	}
	.error-wrap {
		/* Fixed height so an appearing or cleared message never shifts the form. */
		min-height: 30px;
	}
	.error {
		margin: 0;
		font-size: 13px;
		color: var(--alert);
		background: rgba(251, 113, 133, 0.12);
		border: 1px solid rgba(251, 113, 133, 0.3);
		border-radius: 10px;
		padding: 8px 12px;
	}
	.success {
		margin: 0;
		font-size: 13px;
		color: var(--ok-strong);
		background: rgba(52, 211, 153, 0.12);
		border: 1px solid rgba(52, 211, 153, 0.35);
		border-radius: 10px;
		padding: 8px 12px;
	}
	.warning {
		margin: 0;
		font-size: 12px;
		line-height: 1.5;
		color: var(--taskbar-muted);
		background: rgba(251, 191, 36, 0.08);
		border: 1px solid rgba(251, 191, 36, 0.25);
		border-radius: 10px;
		padding: 10px 12px;
	}
	.warning code {
		font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
		font-size: 11px;
		color: var(--warn);
	}
	.actions {
		display: flex;
		gap: 10px;
		justify-content: flex-end;
		margin-top: 2px;
	}
	.cancel {
		height: 40px;
		padding: 0 16px;
		background: var(--taskbar-hover);
		border: 1px solid var(--taskbar-border);
		border-radius: 10px;
		color: var(--taskbar-text);
		font-size: 14px;
		font-weight: 600;
		cursor: pointer;
	}
	.cancel:hover {
		background: rgba(255, 255, 255, 0.16);
	}
	.apply {
		height: 40px;
		padding: 0 18px;
		background: linear-gradient(135deg, var(--accent), var(--accent-strong));
		border: none;
		border-radius: 10px;
		color: #fff;
		font-size: 14px;
		font-weight: 600;
		cursor: pointer;
	}
	.apply:hover:not(:disabled) {
		filter: brightness(1.07);
	}
	.apply:disabled {
		opacity: 0.65;
		cursor: progress;
	}
</style>
