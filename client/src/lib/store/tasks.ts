import { writable } from 'svelte/store';

// Demo running tasks for the taskbar. No backend is contacted (POC); the server
// will populate this later via SSE. A single demo task keeps the indicator
// visible so the UI can be evaluated now.
export interface DemoTask {
	id: string;
	label: string;
	progress: number;
}

export const runningTasks = writable<DemoTask[]>([
	{ id: 'demo-1', label: 'compress documents', progress: 42 }
]);

export function tickDemoTasks() {
	runningTasks.update((tasks) =>
		tasks
			.map((t) => ({ ...t, progress: Math.min(100, t.progress + 20) }))
			.filter((t) => t.progress < 100)
	);
}
