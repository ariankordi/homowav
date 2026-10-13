/** @file One message in a thread. */

import type { Message } from "../types";
import { classes, formatTimestamp, isDisplayableUrl, looksLikeImage } from "../util";

interface MessageBubbleProps {
	message: Message;
}

/** The URL of a message's media, or "" if it has none we may show. */
const mediaUrl = (message: Message): string => {
	const url = message.image_url || (typeof message.stream_url === "string" ? message.stream_url : "");
	return url && isDisplayableUrl(url) ? url : "";
};

/** The content of a bubble, by message type. */
function MessageContent({ message }: MessageBubbleProps) {
	const url = mediaUrl(message);
	if (message.type === "text") {
		return <p class="whitespace-pre-wrap break-words">{message.body}</p>;
	}
	if (!url) {
		return <p class="italic opacity-80">{message.type} message (no media to show yet)</p>;
	}
	if (message.type === "voice") {
		return <audio controls preload="none" src={url} class="max-w-full" />;
	}
	// The service files media sent by URL under "video" even when it is a picture.
	if (message.type === "video" && !looksLikeImage(url)) {
		return <video controls preload="metadata" src={url} class="max-h-80 max-w-full rounded-lg" />;
	}
	return <img src={url} alt={`${message.type} message`} loading="lazy" class="max-h-80 max-w-full rounded-lg" />;
}

/** A chat bubble: ours on the right, theirs on the left. */
export function MessageBubble({ message }: MessageBubbleProps) {
	const mine = message.sender === "visitor";
	return (
		<li class={classes("flex", mine ? "justify-end" : "justify-start")}>
			<div class={classes("max-w-[85%] rounded-2xl px-3 py-2 text-sm sm:max-w-[70%]", mine ? "bg-accent text-accent-ink" : "bg-bubble text-ink")}>
				<MessageContent message={message} />
				<p class="mt-1 text-[11px] opacity-75">
					{formatTimestamp(message.created_timestamp)}
					{/* The status only says something about messages we sent. */}
					{mine && ` · ${message.status}`}
				</p>
			</div>
		</li>
	);
}
