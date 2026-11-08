<script lang="ts">
	import '../app.css';
	import { browser } from '$app/environment';
	import { onMount } from 'svelte';
	import Login from '$lib/components/Login.svelte';
	import Desktop from '$lib/components/Desktop.svelte';
	import Taskbar from '$lib/components/Taskbar.svelte';
	import Window from '$lib/components/Window.svelte';
	import ChangePassword from '$lib/components/ChangePassword.svelte';
	import { windows } from '$lib/store/windows';
	import { auth, login, checkSession } from '$lib/store/auth';

	type LoginDetail = { username: string; password: string };
	async function handleLogin(event: CustomEvent<LoginDetail>) {
		await login(event.detail.username, event.detail.password);
	}

	// Change-password dialog visibility. Held at the page root (rendered inside
	// .shell but outside the taskbar) so the modal escapes the taskbar's stacking
	// context and can paint above open windows.
	let showChangePassword = false;
	function openChangePassword() {
		showChangePassword = true;
	}
	function closeChangePassword() {
		showChangePassword = false;
	}

	onMount(() => {
		if (browser) {
			void checkSession();
		}
	});
</script>

<svelte:window onkeydown={(e) => (e.key === 'Escape' && e.preventDefault())} />

{#if $auth.authenticated}
	<div class="shell">
		<Desktop />
		{#each $windows as win (win.id)}
			<Window window={win} />
		{/each}
		<Taskbar onChangePassword={openChangePassword} />
		<ChangePassword open={showChangePassword} on:close={closeChangePassword} />
	</div>
{:else}
	<div class="login-shell">
		<Login on:submit={handleLogin} />
	</div>
{/if}

<style>
	.shell {
		position: fixed;
		inset: 0;
	}
	.login-shell {
		display: grid;
		place-items: center;
		min-height: 100vh;
		padding: 20px;
	}
</style>
