<script lang="ts">
  import { createEventDispatcher, onMount } from 'svelte';
  import { auth } from '$lib/store/auth';

  type LoginDetail = { username: string; password: string };
  const dispatch = createEventDispatcher<{ submit: LoginDetail }>();

  let username = $state('');
  let password = $state('');
  let usernameInput = $state<HTMLInputElement | undefined>();

  onMount(() => {
    usernameInput?.focus();
  });

  function submit(event: SubmitEvent) {
    event.preventDefault();
    dispatch('submit', { username, password });
  }
</script>

<form class="login" onsubmit={submit}>
  <div class="crest" aria-hidden="true">
    <img class="mark" src="/favicon.svg" alt="" />
  </div>
  <h1 class="title">FileToaster</h1>
  <p class="subtitle">Sign in to your account</p>

  <div class="error-wrap">
    {#if $auth.error}
      <p class="error" role="alert" aria-live="polite">{$auth.error}</p>
    {/if}
  </div>

  <div class="field">
    <label for="username">Username</label>
    <input id="username" bind:this={usernameInput} type="text" bind:value={username} placeholder="Username" autocomplete="username" />
  </div>
  <div class="field">
    <label for="password">Password</label>
    <input id="password" type="password" bind:value={password} placeholder="Password" autocomplete="current-password" />
  </div>

  <button type="submit" disabled={$auth.submitting} class="submit">
    {$auth.submitting ? 'Signing in…' : 'Sign in'}
  </button>
</form>

<style>
  .login {
    width: 100%;
    max-width: 340px;
    padding: 32px 30px 26px;
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

  .crest {
    display: grid;
    place-items: center;
    width: 96px;
    height: 96px;
    border-radius: 16px;
    padding: 8px;
    background: #fff;
    margin: 2px auto 8px;
  }

  .mark {
    width: 80px;
    height: 80px;
  }

  .title {
    margin: 0;
    font-size: 22px;
    font-weight: 700;
    text-align: center;
    color: #fff;
  }

  .subtitle {
    margin: 0;
    font-size: 14px;
    text-align: center;
    color: var(--taskbar-muted);
  }

  .error-wrap {
    /* Reserve fixed height so the error never shifts the inputs/form layout. */
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

  .submit {
    margin-top: 4px;
    height: 42px;
    background: linear-gradient(135deg, var(--accent), var(--accent-strong));
    border: none;
    border-radius: 10px;
    color: #fff;
    font-size: 15px;
    font-weight: 600;
    cursor: pointer;
  }

  .submit:hover:not(:disabled) {
    filter: brightness(1.07);
  }

  .submit:disabled {
    opacity: 0.65;
    cursor: progress;
  }
</style>
