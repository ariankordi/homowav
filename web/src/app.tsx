/** @file The frame: header, navigation, and the screen inside it. */

import { useEffect, useRef, useState } from "preact/hooks";
import { ApiError, LocalApi, NotPairedStatus, ServiceApi } from "./api";
import { ContactList } from "./components/ContactList";
import { Logo } from "./components/Logo";
import { Settings } from "./components/Settings";
import { Thread } from "./components/Thread";
import { Welcome } from "./components/Welcome";
import { Button, Notice } from "./components/ui";
import {
	AppName,
	CheckIntervalKey,
	CheckIntervals,
	ContactsPollMs,
	DefaultCheckMs,
	ManualCheck,
	OnlineColor,
	StatusPollMs,
	ThreadReloadEveryChecks,
} from "./constants";
import { usePolling, useRoute, useStoredNumber } from "./hooks";
import { ChatHash, contactHash, SettingsHash } from "./route";
import type { AppStatus, Contact, Heartbeat } from "./types";
import { classes } from "./util";

/** Shown on a device that has not opened the pairing link. */
function PairFirst() {
	return (
		<div class="mx-auto max-w-md p-6 text-center">
			<h1 class="text-lg font-semibold">This device is not paired yet</h1>
			<p class="mt-2 text-sm text-muted">
				On the computer running {AppName}, open Settings and scan the code under "Use it from the phone" with this device.
			</p>
		</div>
	);
}

interface ChatProps {
	status: AppStatus;
	/** The contact open in the URL, if any. */
	contactId: string | null;
	/** Replaces the frame's status after signing in from the first-run screen. */
	onStatus: (status: AppStatus) => void;
}

/** Whether a stored check interval is one of the offered choices. */
const isCheckInterval = (value: number): boolean => CheckIntervals.some((choice) => choice.ms === value);

/** How often to check for new messages, and a button to check now when that is manual. */
function CheckControl({ checkMs, onCheckMs, onCheckNow }: { checkMs: number; onCheckMs: (ms: number) => void; onCheckNow: () => void }) {
	return (
		<div class="flex shrink-0 items-center gap-2">
			{checkMs === ManualCheck && <Button onClick={onCheckNow}>Refresh</Button>}
			<label class="sr-only" for="check-interval">
				Check for new messages
			</label>
			<select
				id="check-interval"
				title="How often to check for new messages"
				value={String(checkMs)}
				onChange={(event) => onCheckMs(Number(event.currentTarget.value))}
				class="rounded-lg border border-line bg-panel px-2 py-1.5 text-sm"
			>
				{CheckIntervals.map((choice) => (
					<option key={choice.ms} value={String(choice.ms)}>
						{choice.label}
					</option>
				))}
			</select>
		</div>
	);
}

/** Contact list and the open conversation. */
function Chat({ status, contactId, onStatus }: ChatProps) {
	const [contacts, setContacts] = useState<Contact[] | null>(null);
	const [heartbeat, setHeartbeat] = useState<Heartbeat | null>(null);
	const [error, setError] = useState("");
	const [checkMs, setCheckMs] = useStoredNumber(CheckIntervalKey, DefaultCheckMs, isCheckInterval);
	/** Bumped whenever the open thread should reload. */
	const [threadReload, setThreadReload] = useState(0);
	const lastUnread = useRef<string | null>(null);
	const checks = useRef(0);
	/** A single contact is opened once, on the first load; going back to the list afterwards stays there. */
	const openedSoleContact = useRef(false);
	const visitorId = status.visitor_id;
	const ready = status.session.have_session && visitorId !== "";

	const loadContacts = () => {
		ServiceApi.contacts(visitorId)
			.then((fetched) => {
				setContacts(fetched);
				setError("");
			})
			.catch((reason: unknown) => setError(reason instanceof Error ? reason.message : String(reason)));
	};

	/**
	 * Polls the heartbeat, which is what the official app does, and reloads the
	 * thread only when the unread count moved or every few checks.
	 * @param reloadThread - Reload the thread whatever the heartbeat says.
	 */
	const check = (reloadThread: boolean) => {
		checks.current += 1;
		const periodic = checks.current % ThreadReloadEveryChecks === 0;
		ServiceApi.heartbeat()
			.then((beat) => {
				setHeartbeat(beat);
				const moved = lastUnread.current !== null && lastUnread.current !== beat.unread_messages_count;
				lastUnread.current = beat.unread_messages_count;
				if (reloadThread || moved || periodic) {
					setThreadReload((count) => count + 1);
				}
			})
			// Without a heartbeat, reloading the thread is the only way to see new messages.
			.catch(() => setThreadReload((count) => count + 1));
	};

	usePolling(
		() => {
			if (ready) {
				loadContacts();
			}
		},
		checkMs === ManualCheck ? ManualCheck : ContactsPollMs,
		[ready, visitorId],
	);
	usePolling(
		() => {
			if (ready) {
				check(false);
			}
		},
		checkMs,
		[ready],
	);

	useEffect(() => {
		if (!contacts || openedSoleContact.current) {
			return;
		}
		openedSoleContact.current = true;
		if (contacts.length === 1 && contactId === null) {
			// replace, so Back leaves the app instead of returning to a list of one.
			location.replace(contactHash(contacts[0].id));
		}
	}, [contacts, contactId]);

	const checkNow = () => {
		loadContacts();
		check(true);
	};

	if (!status.session.have_session) {
		return <Welcome status={status} onStatus={onStatus} />;
	}
	if (visitorId === "") {
		// Only phone mode gets here: a password sign-in brings the account id with it.
		return (
			<div class="mx-auto max-w-md p-6 text-center">
				<h1 class="text-lg font-semibold">Open your contact list on the phone once</h1>
				<p class="mt-2 text-sm text-muted">
					The phone's login came through, but not the account id yet. Open the HomeWAV app on the phone and look at your contacts, or enter the id under
					Settings.
				</p>
			</div>
		);
	}

	const selected = contacts?.find((contact) => contact.id === contactId) ?? null;
	const online = (id: string): boolean | undefined => (heartbeat ? heartbeat.status[id] === OnlineColor : undefined);
	const control = <CheckControl checkMs={checkMs} onCheckMs={setCheckMs} onCheckNow={checkNow} />;
	return (
		<div class="flex h-full min-h-0">
			{/* On a phone the list and the thread take turns; from md up they sit side by side. */}
			<aside class={classes("h-full w-full shrink-0 overflow-y-auto border-r border-line bg-panel md:block md:w-72", selected && "hidden")}>
				{error && (
					<div class="p-4">
						<Notice tone="bad">{error}</Notice>
					</div>
				)}
				{contacts === null && !error && (
					<div class="p-4">
						<Notice tone="info">Loading contacts…</Notice>
					</div>
				)}
				{contacts && <ContactList contacts={contacts} selectedId={contactId} online={online} />}
			</aside>
			<section class={classes("h-full min-w-0 flex-1 md:block", !selected && "hidden")}>
				{selected ? (
					<Thread
						contact={selected}
						visitorId={visitorId}
						mediaConfigured={status.media_configured}
						online={online(selected.id)}
						balance={heartbeat?.credit_balances?.[selected.id] ?? selected.credit_balance}
						reloadSignal={threadReload}
						headerExtra={control}
					/>
				) : (
					<div class="flex h-full flex-col items-center justify-center gap-4 p-6 text-sm text-muted">
						<p>{contactId && contacts ? "That contact is not on this account." : "Pick a contact to see the conversation."}</p>
						{control}
					</div>
				)}
			</section>
		</div>
	);
}

/** The whole app. */
export function App() {
	const [status, setStatus] = useState<AppStatus | null>(null);
	const [notPaired, setNotPaired] = useState(false);
	const [error, setError] = useState("");
	const route = useRoute();

	usePolling(
		() => {
			LocalApi.status()
				.then((next) => {
					setStatus(next);
					setNotPaired(false);
					setError("");
				})
				.catch((reason: unknown) => {
					setNotPaired(reason instanceof ApiError && reason.status === NotPairedStatus);
					setError(reason instanceof Error ? reason.message : String(reason));
				});
		},
		StatusPollMs,
		[],
	);

	const navLink = (href: string, label: string, current: boolean) => (
		<a href={href} aria-current={current ? "page" : undefined} class={classes("rounded-lg px-3 py-1.5", current && "bg-bubble font-medium")}>
			{label}
		</a>
	);
	return (
		// On wide screens the app is a centered column instead of stretching edge to edge.
		<div class="mx-auto flex h-full w-full max-w-6xl flex-col bg-panel xl:border-x xl:border-line">
			<header class="flex items-center gap-3 border-b border-line bg-panel px-4 py-2">
				<Logo />
				<nav class="ml-auto flex gap-1 text-sm">
					{navLink(ChatHash, "Chat", route.screen === "chat")}
					{navLink(SettingsHash, "Settings", route.screen === "settings")}
				</nav>
			</header>
			<main class="min-h-0 flex-1 overflow-y-auto bg-page">
				{notPaired && <PairFirst />}
				{!notPaired && !status && (
					<div class="p-6 text-center">{error ? <Notice tone="bad">{error}</Notice> : <Notice tone="info">Loading…</Notice>}</div>
				)}
				{!notPaired && status && route.screen === "settings" && <Settings status={status} onStatus={setStatus} />}
				{!notPaired && status && route.screen === "chat" && <Chat status={status} contactId={route.contactId} onStatus={setStatus} />}
			</main>
		</div>
	);
}
