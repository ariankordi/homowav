/** @file Shared hooks. */

import { useEffect, useRef } from "preact/hooks";

/**
 * Runs a task now and then on an interval, while the tab is visible.
 * The task is restarted whenever a dependency changes.
 * @param task - What to run. It should catch its own errors.
 * @param intervalMs - Time between runs.
 * @param dependencies - Values that restart the polling when they change.
 */
export function usePolling(task: () => void, intervalMs: number, dependencies: unknown[]): void {
	const latest = useRef(task);
	latest.current = task;
	useEffect(() => {
		latest.current();
		const timer = setInterval(() => {
			// A hidden tab has nothing to update, and the service is not ours to load.
			if (!document.hidden) {
				latest.current();
			}
		}, intervalMs);
		const onVisible = () => {
			if (!document.hidden) {
				latest.current();
			}
		};
		document.addEventListener("visibilitychange", onVisible);
		return () => {
			clearInterval(timer);
			document.removeEventListener("visibilitychange", onVisible);
		};
	}, [intervalMs, ...dependencies]);
}
