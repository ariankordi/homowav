/** @file The messages of a thread: bubbles, avatars and day separators. */

import { Fragment } from "preact";
import type { Message } from "../types";
import { classes, dayKey, formatDay, formatTime, isDisplayableUrl, looksLikeImage } from "../util";
import { Avatar } from "./ui";

/** The URL of a message's media, or "" if it has none we may show. */
const mediaUrl = (message: Message): string => {
	const url = message.image_url || (typeof message.stream_url === "string" ? message.stream_url : "");
	return url && isDisplayableUrl(url) ? url : "";
};

interface MessageContentProps {
	message: Message;
}

/** The content of a bubble, by message type. */
function MessageContent({ message }: MessageContentProps) {
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

/** Text, and media that cannot be shown (which then reads as text), sit in a bubble. */
const hasBubble = (message: Message): boolean => message.type === "text" || mediaUrl(message) === "";

interface MessageBubbleProps {
	message: Message;
	/** The contact's name, for the avatar next to their messages. */
	contactName: string;
	contactOnline: boolean | undefined;
}

/**
 * One message. Theirs sit on the left behind the contact's avatar; ours on
 * the right in the accent color with white text, as in iMessage. The time
 * goes under the bubble, as in the official app.
 */
export function MessageBubble({ message, contactName, contactOnline }: MessageBubbleProps) {
	const mine = message.sender === "visitor";
	return (
		<li class={classes("flex items-start gap-2", mine ? "justify-end" : "justify-start")}>
			{!mine && <Avatar name={contactName} online={contactOnline} />}
			<div class={classes("flex max-w-[80%] flex-col sm:max-w-[70%]", mine ? "items-end" : "items-start")}>
				<div
					class={classes(
						"text-[15px] leading-snug",
						// Only text gets a bubble; pictures, videos and the audio player stand on their own.
						hasBubble(message) && "rounded-2xl px-3.5 py-2",
						hasBubble(message) && (mine ? "bg-accent text-accent-ink" : "bg-theirs text-ink"),
					)}
				>
					<MessageContent message={message} />
				</div>
				<p class="mt-1 px-1 text-[11px] text-muted">
					{formatTime(message.created_timestamp)}
					{/* The status only says something about messages we sent. */}
					{mine && ` · ${message.status}`}
				</p>
			</div>
		</li>
	);
}

interface MessageListProps {
	/** Oldest first. */
	messages: Message[];
	contactName: string;
	contactOnline: boolean | undefined;
}

/** The thread's messages, with a separator wherever the day changes. */
export function MessageList({ messages, contactName, contactOnline }: MessageListProps) {
	return (
		<ul class="space-y-3">
			{messages.map((message, index) => {
				const startsDay = index === 0 || dayKey(messages[index - 1].created_timestamp) !== dayKey(message.created_timestamp);
				return (
					<Fragment key={message.id}>
						{startsDay && (
							<li class="py-1 text-center text-xs font-medium text-muted" role="separator">
								{formatDay(message.created_timestamp)}
							</li>
						)}
						<MessageBubble message={message} contactName={contactName} contactOnline={contactOnline} />
					</Fragment>
				);
			})}
		</ul>
	);
}
