import { writable } from 'svelte/store';

export const taskNotification = writable<{ taskId: string; label: string } | null>(null);

export function showTaskToast(label: string, taskId: string, duration = 3000): void {
  taskNotification.set({ taskId, label });
  setTimeout(() => {
    taskNotification.update((n) => (n?.taskId === taskId ? null : n));
  }, duration);
}