/** @file One conversation: its messages and the composer. */

import type { ComponentChildren } from "preact";
import { useEffect, useLayoutEffect, useRef, useState } from "preact/hooks";
import { ServiceApi } from "../api";
import { FollowThresholdPx, ThreadPageSize } from "../constants";
import { ChatHash } from "../route";
import type { Contact, Message, OutgoingMessage } from "../types";
import { Composer } from "./Composer";
import { MessageList } from "./MessageBubble";
import { Button, Notice } from "./ui";

/** The newest page of a thread; older ones are fetched on request. */
const FirstPage = 1;

interface ThreadProps {
	contact: Contact;
	visitorId: string;
	mediaConfigured: boolean;
	/** Whether the contact is online; undefined until known. */
	online: boolean | undefined;
	/** The latest balance, from the heartbeat when there is one. */
	balance: string | undefined;
	/** The thread reloads its newest page whenever this changes. */
	reloadSignal: number;
	/** Shown at the right of the header (the check-interval control). */
	headerExtra: ComponentChildren;
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
export function Thread({ contact, visitorId, mediaConfigured, online, balance, reloadSignal, headerExtra }: ThreadProps) {
	const [messages, setMessages] = useState<Message[]>([]);
	const [loaded, setLoaded] = useState(false);
	const [oldestPage, setOldestPage] = useState(FirstPage);
	const [hasOlder, setHasOlder] = useState(false);
	const [loadingOlder, setLoadingOlder] = useState(false);
	const [error, setError] = useState("");
	const scroller = useRef<HTMLDivElement>(null);
	/** Whether the next render should jump to the newest message. */
	const followNext = useRef(true);
	/**
	 * Distance from the bottom to restore after older messages are put above,
	 * so what was on screen stays where it was.
	 */
	const keepFromBottom = useRef<number | null>(null);
	/** The contact whose replies may still be applied; late replies for another are dropped. */
	const currentContact = useRef(contact.id);
	/** Whether the first page has arrived, read by refresh without waiting for a render. */
	const firstPageLoaded = useRef(false);

	const refresh = async () => {
		const contactId = contact.id;
		try {
			const fetched = await ServiceApi.thread(contactId, FirstPage);
			if (currentContact.current !== contactId) {
				return;
			}
			const element = scroller.current;
			if (element) {
				followNext.current ||= element.scrollHeight - element.scrollTop - element.clientHeight < FollowThresholdPx;
			}
			setMessages((known) => mergeMessages(known, fetched));
			if (!firstPageLoaded.current) {
				firstPageLoaded.current = true;
				setHasOlder(fetched.length === ThreadPageSize);
			}
			setLoaded(true);
			setError("");
		} catch (reason) {
			if (currentContact.current === contactId) {
				setError(reason instanceof Error ? reason.message : String(reason));
			}
		}
	};

	// A different contact starts from an empty thread.
	useEffect(() => {
		currentContact.current = contact.id;
		firstPageLoaded.current = false;
		followNext.current = true;
		keepFromBottom.current = null;
		setMessages([]);
		setLoaded(false);
		setOldestPage(FirstPage);
		setHasOlder(false);
		setError("");
		void refresh();
	}, [contact.id]);

	// The first signal value belongs to the load above; only later changes reload.
	const seenSignal = useRef(reloadSignal);
	useEffect(() => {
		if (seenSignal.current !== reloadSignal) {
			seenSignal.current = reloadSignal;
			void refresh();
		}
	}, [reloadSignal]);

	useLayoutEffect(() => {
		const element = scroller.current;
		if (!element) {
			return;
		}
		if (keepFromBottom.current !== null) {
			element.scrollTop = element.scrollHeight - keepFromBottom.current;
			keepFromBottom.current = null;
		} else if (followNext.current) {
			element.scrollTop = element.scrollHeight;
			followNext.current = false;
		}
	}, [messages]);

	const loadOlder = async () => {
		const contactId = contact.id;
		setLoadingOlder(true);
		try {
			const page = oldestPage + 1;
			const fetched = await ServiceApi.thread(contactId, page);
			if (currentContact.current !== contactId) {
				return;
			}
			const element = scroller.current;
			if (element) {
				keepFromBottom.current = element.scrollHeight - element.scrollTop;
			}
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
		// The reply to a send has the same shape as a thread entry (api.ts adds the timestamp).
		setMessages((known) => mergeMessages(known, [sent]));
	};

	return (
		<div class="flex h-full min-h-0 flex-col">
			<header class="flex items-center gap-3 border-b border-line bg-panel px-4 py-3">
				<a href={ChatHash} class="rounded-lg border border-line px-2 py-1 text-sm md:hidden" aria-label="Back to contacts">
					←
				</a>
				<div class="min-w-0 flex-1">
					<h2 class="truncate font-semibold">{contact.full_name}</h2>
					<p class="truncate text-xs text-muted">
						{online !== undefined && <span class={online ? "text-good" : undefined}>{online ? "Online" : "Offline"} · </span>}
						{contact.prison_name}
						{balance && ` · balance $${balance}`}
					</p>
				</div>
				{headerExtra}
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
				<MessageList messages={messages} contactName={contact.full_name} contactOnline={online} />
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
