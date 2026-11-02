/** @file The message composer: text, an audio file, or media by URL. */

import { useRef, useState } from "preact/hooks";
import { ApiError, LocalApi } from "../api";
import { FallbackTextLimit, VoiceExtension } from "../constants";
import type { Contact, MessageType, MessagingEnabled, OutgoingMessage, PricedType } from "../types";
import { audioDuration, classes, extensionOf, formatDollars, isHttpUrl, parseNumber } from "../util";
import { Button, Notice } from "./ui";

/** Media kinds that are sent as a public URL. */
type UrlMediaType = Extract<MessageType, "gif" | "image" | "video">;

/** Labels of the URL media kinds, in the order they are offered. */
const UrlMediaLabels: Record<UrlMediaType, string> = { gif: "GIF", image: "Image", video: "Video" };

/** Extensions that suggest a video when a file or URL is chosen. */
const VideoExtensions = [".mp4", ".mov", ".qt", ".m4v", ".webm", ".mkv", ".avi", ".3gp"];

/** Decimal places of the length reported for a recording, as the app sends it. */
const ElapsedDecimals = 3;

/** Used when a contact row carries no messaging_enabled block. */
const AllEnabled: MessagingEnabled = { video: true, library: true, image: true, gif: true, text: true, voice: true };

/** What is attached instead of text. */
type Attachment =
	| { kind: "voice"; file: File; seconds: string }
	| { kind: "media"; url: string; type: UrlMediaType };

interface ComposerProps {
	contact: Contact;
	/** The facility's per-message prices, once loaded. */
	prices: Partial<Record<PricedType, number>> | undefined;
	/** Whether an upload bucket is set up. */
	mediaConfigured: boolean;
	/** Sends the message; rejects with the reason when it fails. */
	onSend: (outgoing: OutgoingMessage) => Promise<void>;
}

/**
 * Guesses the kind to send a file or URL as; the user can change it. A GIF
 * file goes as a GIF, so it stays animated. Any other picture goes as
 * whichever of Image and GIF is cheaper: the recipient sees a still picture
 * sent as a GIF just the same. A tie, or unknown prices, keeps Image.
 * @param name - The file name or URL.
 * @param enabled - What the facility allows.
 * @param prices - The facility's prices, if loaded.
 * @returns The kind to preselect.
 */
const guessMediaType = (
	name: string,
	mimeType: string,
	enabled: MessagingEnabled,
	prices: Partial<Record<PricedType, number>> | undefined,
): UrlMediaType => {
	const extension = extensionOf(name);
	// The browser's type wins over the extension: it covers names like
	// "clip.MOV.download" or containers missing from VideoExtensions.
	const looksLikeVideo = mimeType.startsWith("video/") || VideoExtensions.includes(extension);
	if (looksLikeVideo && enabled.video) {
		return "video";
	}
	if (extension === ".gif" && enabled.gif) {
		return "gif";
	}
	if (enabled.image && enabled.gif) {
		const gifIsCheaper = prices?.gif !== undefined && prices.image !== undefined && prices.gif < prices.image;
		return gifIsCheaper ? "gif" : "image";
	}
	return enabled.image ? "image" : enabled.gif ? "gif" : "video";
};

/** The composer under a thread. */
export function Composer({ contact, prices, mediaConfigured, onSend }: ComposerProps) {
	const [text, setText] = useState("");
	const [attachment, setAttachment] = useState<Attachment | null>(null);
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState("");
	const audioInput = useRef<HTMLInputElement>(null);
	const mediaInput = useRef<HTMLInputElement>(null);

	const enabled = contact.messaging_enabled ?? AllEnabled;
	const textLimit = parseNumber(contact.prison_max_text_message_length, FallbackTextLimit);
	const voiceLimit = parseNumber(contact.prison_max_voice_message_length, 0);
	const urlTypes = (Object.keys(UrlMediaLabels) as UrlMediaType[]).filter((type) => enabled[type]);

	const fail = (reason: unknown) => setError(reason instanceof Error ? reason.message : String(reason));

	const pickAudio = async (file: File | undefined) => {
		setError("");
		if (!file) {
			return;
		}
		if (extensionOf(file.name) !== VoiceExtension) {
			setError(`Only ${VoiceExtension} audio is accepted. Convert the file first.`);
			return;
		}
		try {
			const seconds = await audioDuration(file);
			setAttachment({ kind: "voice", file, seconds: seconds.toFixed(ElapsedDecimals) });
		} catch (reason) {
			fail(reason);
		}
	};

	const pickMediaFile = async (file: File | undefined) => {
		setError("");
		if (!file) {
			return;
		}
		setBusy(true);
		try {
			const url = await LocalApi.upload(file);
			setAttachment({ kind: "media", url, type: guessMediaType(file.name, file.type, enabled, prices) });
		} catch (reason) {
			fail(reason);
		} finally {
			setBusy(false);
		}
	};

	/** Builds the message from the current state, or explains what is missing. */
	const build = (): OutgoingMessage | string => {
		if (attachment?.kind === "voice") {
			const seconds = Number(attachment.seconds);
			if (!Number.isFinite(seconds) || seconds <= 0) {
				return "Enter the length of the recording in seconds.";
			}
			return { type: "voice", body: "", galleryUrl: "", voice: attachment.file, elapsedSeconds: seconds };
		}
		if (attachment?.kind === "media") {
			if (!isHttpUrl(attachment.url)) {
				return "Enter a public http(s) link to the file.";
			}
			return { type: attachment.type, body: "", galleryUrl: attachment.url.trim(), voice: null, elapsedSeconds: 0 };
		}
		if (text.trim() === "") {
			return "Write a message first.";
		}
		return { type: "text", body: text, galleryUrl: "", voice: null, elapsedSeconds: 0 };
	};

	const submit = async (event: Event) => {
		event.preventDefault();
		const outgoing = build();
		if (typeof outgoing === "string") {
			setError(outgoing);
			return;
		}
		setBusy(true);
		setError("");
		try {
			await onSend(outgoing);
			setText("");
			setAttachment(null);
		} catch (reason) {
			fail(reason instanceof ApiError ? reason : new Error("Sending failed. Nothing was sent."));
		} finally {
			setBusy(false);
		}
	};

	const overVoiceLimit = attachment?.kind === "voice" && voiceLimit > 0 && Number(attachment.seconds) > voiceLimit;

	return (
		<form onSubmit={submit} class="border-t border-line bg-panel p-3">
			{attachment?.kind === "voice" && (
				<div class="mb-2 rounded-lg bg-page p-3 text-sm">
					<div class="flex items-center justify-between gap-2">
						<span class="min-w-0 truncate font-medium">{attachment.file.name}</span>
						<Button onClick={() => setAttachment(null)}>Remove</Button>
					</div>
					<label class="mt-2 flex items-center gap-2 text-xs text-muted">
						Length reported, in seconds
						<input
							type="text"
							inputMode="decimal"
							value={attachment.seconds}
							onInput={(event) => setAttachment({ ...attachment, seconds: event.currentTarget.value })}
							class="w-24 rounded border border-line bg-panel px-2 py-1 text-ink"
						/>
					</label>
					{overVoiceLimit && <Notice tone="warn">This is longer than the facility's {voiceLimit} second limit, so it may be refused.</Notice>}
				</div>
			)}
			{attachment?.kind === "media" && (
				<div class="mb-2 space-y-2 rounded-lg bg-page p-3 text-sm">
					<div class="flex items-center gap-2">
						<input
							type="url"
							inputMode="url"
							placeholder="https://… public link to the file"
							value={attachment.url}
							onInput={(event) => setAttachment({ ...attachment, url: event.currentTarget.value })}
							class="min-w-0 flex-1 rounded-lg border border-line bg-panel px-3 py-2 text-ink outline-none focus:border-accent"
						/>
						<Button onClick={() => setAttachment(null)}>Remove</Button>
					</div>
					<div class="flex flex-wrap items-center gap-2">
						<span class="text-xs text-muted">Send as</span>
						{urlTypes.map((type) => (
							<button
								key={type}
								type="button"
								aria-pressed={attachment.type === type}
								onClick={() => setAttachment({ ...attachment, type })}
								class={classes(
									"rounded-full border px-3 py-1 text-xs",
									attachment.type === type ? "border-accent bg-accent text-accent-ink" : "border-line bg-panel",
								)}
							>
								{UrlMediaLabels[type]}
								{prices?.[type] !== undefined && <span class="opacity-75"> · {formatDollars(prices[type])}</span>}
							</button>
						))}
						{mediaConfigured ? (
							<Button onClick={() => mediaInput.current?.click()} disabled={busy}>
								Upload a file…
							</Button>
						) : (
							<span class="text-xs text-muted">Set up media storage in Settings to upload files from here.</span>
						)}
					</div>
					<p class="text-xs text-muted">The facility only shows media from hosts it allows. A picture sent as a GIF can be cheaper.</p>
				</div>
			)}
			{!attachment && (
				<div>
					<textarea
						value={text}
						maxLength={textLimit}
						rows={2}
						placeholder={enabled.text ? `Message ${contact.full_name}` : "Text messages are turned off for this contact"}
						disabled={!enabled.text || busy}
						onInput={(event) => setText(event.currentTarget.value)}
						class="block w-full resize-none rounded-lg border border-line bg-page px-3 py-2 text-sm text-ink outline-none focus:border-accent"
					/>
				</div>
			)}
			<div class="mt-2 flex flex-wrap items-center gap-2">
				<input ref={audioInput} type="file" accept={`${VoiceExtension},audio/mp4,audio/x-m4a`} class="hidden" onChange={(event) => { void pickAudio(event.currentTarget.files?.[0]); event.currentTarget.value = ""; }} />
				<input ref={mediaInput} type="file" accept="image/*,video/*" class="hidden" onChange={(event) => { void pickMediaFile(event.currentTarget.files?.[0]); event.currentTarget.value = ""; }} />
				<Button onClick={() => audioInput.current?.click()} disabled={busy || !enabled.voice} title={`Attach a ${VoiceExtension} audio file`}>
					Audio
				</Button>
				<Button
					onClick={() => setAttachment({ kind: "media", url: "", type: urlTypes[0] })}
					disabled={busy || urlTypes.length === 0}
					title="Send a picture, GIF or video by link"
				>
					Picture / video
				</Button>
				<span class="ml-auto text-xs text-muted">
					{!attachment && `${text.length} / ${textLimit}`}
					{!attachment && contact.prison_price_per_text_message && ` · $${contact.prison_price_per_text_message} per text`}
				</span>
				<Button kind="primary" type="submit" disabled={busy}>
					{busy ? "Working…" : "Send"}
				</Button>
			</div>
			{error && (
				<div class="mt-2">
					<Notice tone="bad">{error}</Notice>
				</div>
			)}
		</form>
	);
}
