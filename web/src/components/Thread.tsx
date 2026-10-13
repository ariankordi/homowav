/** @file One conversation: its messages and the composer. */

import { useEffect, useLayoutEffect, useRef, useState } from "preact/hooks";
import { ServiceApi } from "../api";
import { FollowThresholdPx, ThreadPageSize, ThreadPollMs } from "../constants";
import { usePolling } from "../hooks";
import type { Contact, Message, OutgoingMessage } from "../types";
import { Composer } from "./Composer";
import { MessageBubble } from "./MessageBubble";
import { Button, Notice } from "./ui";

/** The newest page of a thread; older ones are fetched on request. */
const FirstPage = 1;

interface ThreadProps {
	contact: Contact;
	visitorId: string;
	mediaConfigured: boolean;
	/** Returns to the contact list on narrow screens. */
	onBack: () => void;
}

/**
 * Merges newly fetched messages into the known ones.
 * @param known - Messages already shown, oldest first.
 * @param fetched - Messages from the service, in any order.
 * @returns All of them without duplicates, oldest first.
 */
function mergeMessages(known: Message[], fetched: Message[]): Message[] {
	const byId = new Map<string, Message>();
	for (const message of known) {
		byId.set(message.id, message);
	}
	// Fetched copies win: a status changes from pending to approved to read.
	for (const message of fetched) {
		byId.set(message.id, message);
	}
	return [...byId.values()].sort((a, b) => a.created_timestamp - b.created_timestamp || Number(a.id) - Number(b.id));
}

/** A conversation with one contact. */
export function Thread({ contact, visitorId, mediaConfigured, onBack }: ThreadProps) {
	const [messages, setMessages] = useState<Message[]>([]);
	const [loaded, setLoaded] = useState(false);
	const [oldestPage, setOldestPage] = useState(FirstPage);
	const [hasOlder, setHasOlder] = useState(false);
	const [loadingOlder, setLoadingOlder] = useState(false);
	const [error, setError] = useState("");
	const scroller = useRef<HTMLDivElement>(null);
	/** Whether the next render should jump to the newest message. */
	const followNext = useRef(true);

	// A different contact starts from an empty thread.
	useEffect(() => {
		setMessages([]);
		setLoaded(false);
		setOldestPage(FirstPage);
		setHasOlder(false);
		setError("");
		followNext.current = true;
	}, [contact.id]);

	const refresh = async () => {
		try {
			const fetched = await ServiceApi.thread(contact.id, FirstPage);
			const element = scroller.current;
			if (element) {
				followNext.current ||= element.scrollHeight - element.scrollTop - element.clientHeight < FollowThresholdPx;
			}
			setMessages((known) => mergeMessages(known, fetched));
			setHasOlder((previous) => (loaded ? previous : fetched.length === ThreadPageSize));
			setLoaded(true);
			setError("");
		} catch (reason) {
			setError(reason instanceof Error ? reason.message : String(reason));
		}
	};
	usePolling(() => void refresh(), ThreadPollMs, [contact.id]);

	useLayoutEffect(() => {
		const element = scroller.current;
		if (element && followNext.current) {
			element.scrollTop = element.scrollHeight;
			followNext.current = false;
		}
	}, [messages]);

	const loadOlder = async () => {
		setLoadingOlder(true);
		try {
			const page = oldestPage + 1;
			const fetched = await ServiceApi.thread(contact.id, page);
			setMessages((known) => mergeMessages(known, fetched));
			setOldestPage(page);
			setHasOlder(fetched.length === ThreadPageSize);
		} catch (reason) {
			setError(reason instanceof Error ? reason.message : String(reason));
		} finally {
			setLoadingOlder(false);
		}
	};

	const send = async (outgoing: OutgoingMessage) => {
		const sent = await ServiceApi.send(contact, visitorId, outgoing);
		followNext.current = true;
		// The reply to a send has the same shape as a thread entry.
		setMessages((known) => mergeMessages(known, [sent]));
	};

	return (
		<div class="flex h-full min-h-0 flex-col">
			<header class="flex items-center gap-3 border-b border-line bg-panel px-4 py-3">
				<button type="button" onClick={onBack} class="rounded-lg border border-line px-2 py-1 text-sm md:hidden" aria-label="Back to contacts">
					←
				</button>
				<div class="min-w-0">
					<h2 class="truncate font-semibold">{contact.full_name}</h2>
					<p class="truncate text-xs text-muted">
						{contact.prison_name}
						{contact.credit_balance && ` · balance $${contact.credit_balance}`}
					</p>
				</div>
			</header>
			<div ref={scroller} class="min-h-0 flex-1 overflow-y-auto px-3 py-4">
				{hasOlder && (
					<div class="mb-3 text-center">
						<Button onClick={() => void loadOlder()} disabled={loadingOlder}>
							{loadingOlder ? "Loading…" : "Load older messages"}
						</Button>
					</div>
				)}
				{!loaded && !error && <Notice tone="info">Loading messages…</Notice>}
				{loaded && messages.length === 0 && <Notice tone="info">No messages yet.</Notice>}
				<ul class="space-y-2">
					{messages.map((message) => (
						<MessageBubble key={message.id} message={message} />
					))}
				</ul>
				{error && (
					<div class="mt-3">
						<Notice tone="bad">{error}</Notice>
					</div>
				)}
			</div>
			<Composer contact={contact} mediaConfigured={mediaConfigured} onSend={send} />
		</div>
	);
}
