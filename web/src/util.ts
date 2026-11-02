/** @file Small helpers with no state. */

import { MsPerSecond } from "./constants";

/** Extensions shown as a picture even when the service calls the message a video. */
const ImageExtensions = [".png", ".jpg", ".jpeg", ".gif", ".webp"];

/**
 * Returns the element with the given id.
 * @param id - Element id.
 * @returns The element.
 * @throws {Error} When no such element exists.
 */
export function requireElementById(id: string): HTMLElement {
	const element = document.getElementById(id);
	if (!element) {
		throw new Error(`Missing element #${id}.`);
	}
	return element;
}

/** Joins class names, skipping the falsy ones. */
export const classes = (...names: (string | false | null | undefined)[]): string => names.filter(Boolean).join(" ");

/** Formats a unix timestamp (seconds) as a local time of day, e.g. "9:24 PM". */
export const formatTime = (seconds: number): string =>
	new Date(seconds * MsPerSecond).toLocaleTimeString(undefined, { timeStyle: "short" });

/** A key that is equal for timestamps on the same local calendar day. */
export const dayKey = (seconds: number): string => new Date(seconds * MsPerSecond).toDateString();

/** Names a local calendar day: "Today", "Yesterday", or a date. */
export function formatDay(seconds: number): string {
	const day = new Date(seconds * MsPerSecond);
	const today = new Date();
	const yesterday = new Date(today.getFullYear(), today.getMonth(), today.getDate() - 1);
	if (day.toDateString() === today.toDateString()) {
		return "Today";
	}
	if (day.toDateString() === yesterday.toDateString()) {
		return "Yesterday";
	}
	return day.toLocaleDateString(undefined, { weekday: "short", month: "short", day: "numeric", year: "numeric" });
}

/** Formats an ISO time for the status display; "not recorded" when absent. */
export const formatIsoTime = (value: string | undefined): string =>
	value ? new Date(value).toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" }) : "not recorded";

/** Lower-case extension of a file name or URL path, with the dot; "" if none. */
export const extensionOf = (name: string): string => {
	const path = name.split(/[?#]/)[0];
	const dot = path.lastIndexOf(".");
	return dot > path.lastIndexOf("/") ? path.slice(dot).toLowerCase() : "";
};

/** Whether a URL points at a picture, judged by its extension or data type. */
export const looksLikeImage = (url: string): boolean =>
	url.startsWith("data:image/") || ImageExtensions.includes(extensionOf(url));

/**
 * Only http(s) and inline images are ever placed in a media element: the URLs
 * come from the service and from other people.
 */
export const isDisplayableUrl = (url: string): boolean => /^(https?:\/\/|data:image\/)/i.test(url);

/** Whether a string is an absolute http(s) URL, as the service needs for media. */
export const isHttpUrl = (value: string): boolean => /^https?:\/\/\S+$/i.test(value.trim());

/** Formats a price or balance in dollars, as the app shows them. */
export const formatDollars = (amount: number): string => `$${amount.toFixed(2)}`;

/** Parses the service's numbers-as-strings; fallback when missing or invalid. */
export const parseNumber = (value: string | undefined | null, fallback: number): number => {
	const parsed = Number(value);
	return value && Number.isFinite(parsed) ? parsed : fallback;
};

/**
 * Reads the length of an audio file by loading its metadata.
 * @param file - The audio file.
 * @returns Its duration in seconds.
 * @throws {Error} When the browser cannot read the file as audio.
 */
export function audioDuration(file: File): Promise<number> {
	return new Promise((resolve, reject) => {
		const url = URL.createObjectURL(file);
		const audio = new Audio();
		const finish = (action: () => void) => {
			URL.revokeObjectURL(url);
			action();
		};
		audio.preload = "metadata";
		audio.onloadedmetadata = () => finish(() => resolve(audio.duration));
		audio.onerror = () => finish(() => reject(new Error("This file could not be read as audio.")));
		audio.src = url;
	});
}
