import { browser } from '$app/environment';
import { writable, type Writable } from 'svelte/store';

// Auth state kept in a store so every route can subscribe to it. `error` carries
// only a generic message; it never distinguishes a wrong username from a wrong
// password so login failures cannot be probed.
interface AuthState {
	authenticated: boolean;
	username: string | null;
	error: string;
	submitting: boolean;
}

const initial: AuthState = {
	authenticated: false,
	username: null,
	error: '',
	submitting: false,
};

export const auth: Writable<AuthState> = writable(initial);

function set(patch: Partial<AuthState>) {
	auth.update((state) => ({ ...state, ...patch }));
}

// checkSession hydrates the store from the server. It returns true when a valid
// session cookie is present. It is a no-op outside the browser (prerendering),
// where no cookie or network is available.
export async function checkSession(): Promise<boolean> {
	if (!browser) {
		return false;
	}
	try {
		const response = await fetch('/api/auth/status', { credentials: 'include' });
		if (response.ok) {
			const data = (await response.json().catch(() => ({}))) as { username?: unknown };
			set({
				authenticated: true,
				username: typeof data.username === 'string' && data.username ? data.username : 'admin',
				error: '',
				submitting: false,
			});
			return true;
		}
	} catch {
		// Network error: treat as unauthenticated and let the UI show the login form.
	}
	set({ authenticated: false, username: null, error: '', submitting: false });
	return false;
}

// login attempts to sign in and updates the store with the result. On any
// failure the stored error is a generic message so the UI cannot tell whether the
// username or the password was wrong. It resolves true on success.
export async function login(username: string, password: string): Promise<boolean> {
	set({ submitting: true, error: '' });
	try {
		const response = await fetch('/api/auth/login', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ username, password }),
			credentials: 'include',
		});
		if (response.ok) {
			set({
				authenticated: true,
				username: username || 'admin',
				error: '',
				submitting: false,
			});
			return true;
		}
		const data = (await response.json().catch(() => ({}))) as { error?: unknown };
		const message =
			typeof data.error === 'string' && data.error ? data.error : 'username or password is incorrect';
		set({ authenticated: false, username: null, error: message, submitting: false });
		return false;
	} catch {
		set({ authenticated: false, username: null, error: 'username or password is incorrect', submitting: false });
		return false;
	}
}

// logout clears the server session and resets local state.
export async function logout(): Promise<void> {
	try {
		await fetch('/api/auth/logout', { method: 'POST', credentials: 'include' });
	} catch {
		// Ignore network errors; local state is cleared regardless.
	}
	set(initial);
}

// changePassword sends the current and new password to the server. On success
// the new password is written to the config file and the server accepts it
// immediately; the store is left authenticated so the session is untouched. On
// failure the generic server message is stored in `error`.
//
// Warning handled by the UI: if the admin password was set via the
// FILETOASTER_ADMIN_PASSWORD env var, the server still writes the config hash,
// but the env var wins on login until it is removed, so the change has no effect
// until then.
export async function changePassword(current: string, newPassword: string): Promise<boolean> {
	set({ submitting: true, error: '' });
	try {
		const response = await fetch('/api/auth/change-password', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ current_password: current, new_password: newPassword }),
			credentials: 'include',
		});
		if (response.ok) {
			set({ submitting: false, error: '' });
			return true;
		}
		const data = (await response.json().catch(() => ({}))) as { error?: unknown };
		const message =
			typeof data.error === 'string' && data.error ? data.error : 'could not change the password';
		set({ error: message, submitting: false });
		return false;
	} catch {
		set({ error: 'could not change the password', submitting: false });
		return false;
	}
}
