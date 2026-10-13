/** @file Small presentational pieces reused across screens. */

import type { ComponentChildren } from "preact";
import { useState } from "preact/hooks";
import { CopiedNoticeMs } from "../constants";
import { classes } from "../util";

interface ButtonProps {
	children: ComponentChildren;
	onClick?: () => void;
	disabled?: boolean;
	/** "primary" is the one main action of a form; "plain" is everything else. */
	kind?: "primary" | "plain";
	type?: "button" | "submit";
	title?: string;
}

/** The app's button. */
export function Button({ children, onClick, disabled, kind = "plain", type = "button", title }: ButtonProps) {
	return (
		<button
			type={type}
			title={title}
			disabled={disabled}
			onClick={onClick}
			class={classes(
				"rounded-lg px-3 py-2 text-sm font-medium transition disabled:cursor-not-allowed disabled:opacity-50",
				kind === "primary" ? "bg-accent text-accent-ink hover:opacity-90" : "border border-line bg-panel text-ink hover:bg-bubble",
			)}
		>
			{children}
		</button>
	);
}

interface CardProps {
	title: string;
	/** One line under the title saying what the section is for. */
	hint?: string;
	children: ComponentChildren;
}

/** A titled section of the Settings screen. */
export function Card({ title, hint, children }: CardProps) {
	return (
		<section class="rounded-xl border border-line bg-panel p-4 sm:p-5">
			<h2 class="text-base font-semibold">{title}</h2>
			{hint && <p class="mt-1 text-sm text-muted">{hint}</p>}
			<div class="mt-4 space-y-3">{children}</div>
		</section>
	);
}

interface NoticeProps {
	tone: "bad" | "warn" | "good" | "info";
	children: ComponentChildren;
}

/** Tone to text color. */
const NoticeColors = { bad: "text-bad", warn: "text-warn", good: "text-good", info: "text-muted" } as const;

/** An inline status or error line. */
export function Notice({ tone, children }: NoticeProps) {
	return (
		<p role={tone === "bad" ? "alert" : "status"} class={classes("text-sm", NoticeColors[tone])}>
			{children}
		</p>
	);
}

interface FieldProps {
	label: string;
	value: string;
	onInput: (value: string) => void;
	placeholder?: string;
	type?: "text" | "password" | "url";
	/** Extra explanation under the field. */
	hint?: string;
	inputMode?: "numeric" | "text" | "url";
}

/** A labeled text input. */
export function Field({ label, value, onInput, placeholder, type = "text", hint, inputMode }: FieldProps) {
	return (
		<label class="block text-sm">
			<span class="font-medium">{label}</span>
			<input
				// Preact types each input kind separately and cannot pick one for a
				// union, so the value is narrowed here. All three behave as text inputs.
				type={type as "text"}
				value={value}
				inputMode={inputMode}
				placeholder={placeholder}
				autocomplete="off"
				autocapitalize="off"
				spellcheck={false}
				onInput={(event) => onInput(event.currentTarget.value)}
				class="mt-1 block w-full rounded-lg border border-line bg-page px-3 py-2 text-ink outline-none focus:border-accent"
			/>
			{hint && <span class="mt-1 block text-xs text-muted">{hint}</span>}
		</label>
	);
}

interface CopyRowProps {
	/** The text shown and copied. */
	value: string;
}

/** A value with a Copy button, for URLs the user types into another device. */
export function CopyRow({ value }: CopyRowProps) {
	const [copied, setCopied] = useState(false);
	const copy = async () => {
		try {
			await navigator.clipboard.writeText(value);
			setCopied(true);
			setTimeout(() => setCopied(false), CopiedNoticeMs);
		} catch {
			// Clipboard access needs a secure context; over plain LAN HTTP the
			// user selects the text instead, which is why it is select-all.
		}
	};
	return (
		<div class="flex items-center gap-2">
			<code class="min-w-0 flex-1 select-all break-all rounded-lg bg-page px-3 py-2 text-xs">{value}</code>
			<Button onClick={copy}>{copied ? "Copied" : "Copy"}</Button>
		</div>
	);
}
