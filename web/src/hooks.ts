/** @file Shared hooks. */

import { useEffect, useRef, useState } from "preact/hooks";
import { parseRoute, type Route } from "./route";

/**
 * Runs a task now and then on an interval, while the tab is visible.
 * The task is restarted whenever a dependency changes.
 * @param task - What to run. It should catch its own errors.
 * @param intervalMs - Time between runs; 0 runs it only once, when started.
 * @param dependencies - Values that restart the polling when they change.
 */
export function usePolling(task: () => void, intervalMs: number, dependencies: unknown[]): void {
	const latest = useRef(task);
	latest.current = task;
	useEffect(() => {
		latest.current();
		if (intervalMs <= 0) {
			return;
		}
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

/** The current route, following changes of the URL hash. */
export function useRoute(): Route {
	const [route, setRoute] = useState<Route>(() => parseRoute(location.hash));
	useEffect(() => {
		const onHashChange = () => setRoute(parseRoute(location.hash));
		addEventListener("hashchange", onHashChange);
		return () => removeEventListener("hashchange", onHashChange);
	}, []);
	return route;
}

/**
 * A number remembered in this browser. Storage can be unavailable (private
 * windows, blocked site data), in which case the value lasts for the page only.
 * @param key - localStorage key.
 * @param fallback - Value when nothing valid is stored.
 * @param isValid - Accepts only values the caller can use.
 * @returns The value and its setter.
 */
export function useStoredNumber(key: string, fallback: number, isValid: (value: number) => boolean): [number, (value: number) => void] {
	const [value, setValue] = useState(() => {
		try {
			const stored = Number(localStorage.getItem(key));
			return localStorage.getItem(key) !== null && isValid(stored) ? stored : fallback;
		} catch {
			return fallback;
		}
	});
	const store = (next: number) => {
		setValue(next);
		try {
			localStorage.setItem(key, String(next));
		} catch {
			// Remembering is a convenience; the choice still applies to this page.
		}
	};
	return [value, store];
}

/**
 * A yes/no choice remembered in this browser, stored as 1 or 0 by
 * useStoredNumber, with the same fallback when storage is unavailable.
 * @param key - localStorage key.
 * @param fallback - Value when nothing valid is stored.
 * @returns The value and its setter.
 */
export function useStoredFlag(key: string, fallback: boolean): [boolean, (value: boolean) => void] {
	const [stored, store] = useStoredNumber(key, fallback ? 1 : 0, (value) => value === 0 || value === 1);
	return [stored === 1, (next) => store(next ? 1 : 0)];
}
