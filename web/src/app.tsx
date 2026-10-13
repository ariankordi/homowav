/** @file The frame: header, navigation, and the screen inside it. */

import { useEffect, useState } from "preact/hooks";
import { ApiError, LocalApi, NotPairedStatus, ServiceApi } from "./api";
import { ContactList } from "./components/ContactList";
import { Settings } from "./components/Settings";
import { Thread } from "./components/Thread";
import { Notice } from "./components/ui";
import { ContactsPollMs, StatusPollMs } from "./constants";
import { usePolling } from "./hooks";
import type { AppStatus, Contact } from "./types";
import { classes } from "./util";

/** The screens of the app, kept in the URL hash so reload and back work. */
type Screen = "chat" | "settings";

/** Hash of the Settings screen; anything else is the chat. */
const SettingsHash = "#/settings";

const screenFromHash = (): Screen => (location.hash === SettingsHash ? "settings" : "chat");

/** Shown on a device that has not opened the pairing link. */
function PairFirst() {
	return (
		<div class="mx-auto max-w-md p-6 text-center">
			<h1 class="text-lg font-semibold">This device is not paired yet</h1>
			<p class="mt-2 text-sm text-muted">
				On the computer running HW Client, open Settings and scan the code under "Use it from the phone" with this device.
			</p>
		</div>
	);
}

interface ChatProps {
	status: AppStatus;
}

/** Contact list and the open conversation. */
function Chat({ status }: ChatProps) {
	const [contacts, setContacts] = useState<Contact[] | null>(null);
	const [selectedId, setSelectedId] = useState<string | null>(null);
	const [error, setError] = useState("");
	const visitorId = status.visitor_id;
	const ready = status.session.have_session && visitorId !== "";

	usePolling(
		() => {
			if (!ready) {
				return;
			}
			ServiceApi.contacts(visitorId)
				.then((fetched) => {
					setContacts(fetched);
					setError("");
				})
				.catch((reason: unknown) => setError(reason instanceof Error ? reason.message : String(reason)));
		},
		ContactsPollMs,
		[ready, visitorId],
	);

	if (!status.session.have_session) {
		return (
			<div class="mx-auto max-w-md p-6 text-center">
				<h1 class="text-lg font-semibold">Not connected yet</h1>
				<p class="mt-2 text-sm text-muted">The phone app has to connect through this computer once.</p>
				<a href={SettingsHash} class="mt-4 inline-block rounded-lg bg-accent px-4 py-2 text-sm font-medium text-accent-ink">
					Set up the phone
				</a>
			</div>
		);
	}
	if (visitorId === "") {
		return (
			<div class="mx-auto max-w-md p-6 text-center">
				<h1 class="text-lg font-semibold">Open your contact list in the app once</h1>
				<p class="mt-2 text-sm text-muted">
					The account id has not passed through the proxy yet. Open the HomeWAV app and look at your contacts, or enter the id under Settings.
				</p>
			</div>
		);
	}

	const selected = contacts?.find((contact) => contact.id === selectedId) ?? null;
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
				{contacts && <ContactList contacts={contacts} selectedId={selectedId} onSelect={(contact) => setSelectedId(contact.id)} />}
			</aside>
			<section class={classes("h-full min-w-0 flex-1 md:block", !selected && "hidden")}>
				{selected ? (
					<Thread contact={selected} visitorId={visitorId} mediaConfigured={status.media_configured} onBack={() => setSelectedId(null)} />
				) : (
					<div class="flex h-full items-center justify-center p-6 text-sm text-muted">Pick a contact to see the conversation.</div>
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
	const [screen, setScreen] = useState<Screen>(screenFromHash());

	useEffect(() => {
		const onHashChange = () => setScreen(screenFromHash());
		addEventListener("hashchange", onHashChange);
		return () => removeEventListener("hashchange", onHashChange);
	}, []);

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

	const connected = status?.session.have_session ?? false;
	return (
		<div class="flex h-full flex-col">
			<header class="flex items-center gap-3 border-b border-line bg-panel px-4 py-2">
				<span class="font-semibold">HW Client</span>
				{status && (
					<span class="flex items-center gap-1.5 text-xs text-muted">
						<span class={classes("h-2 w-2 rounded-full", connected ? "bg-good" : "bg-warn")} />
						{connected ? "Connected" : "Not connected"}
					</span>
				)}
				<nav class="ml-auto flex gap-1 text-sm">
					<a href="#/" aria-current={screen === "chat" ? "page" : undefined} class={classes("rounded-lg px-3 py-1.5", screen === "chat" && "bg-bubble font-medium")}>
						Chat
					</a>
					<a href={SettingsHash} aria-current={screen === "settings" ? "page" : undefined} class={classes("rounded-lg px-3 py-1.5", screen === "settings" && "bg-bubble font-medium")}>
						Settings
					</a>
				</nav>
			</header>
			<main class="min-h-0 flex-1 overflow-y-auto">
				{notPaired && <PairFirst />}
				{!notPaired && !status && (
					<div class="p-6 text-center">{error ? <Notice tone="bad">{error}</Notice> : <Notice tone="info">Loading…</Notice>}</div>
				)}
				{!notPaired && status && screen === "settings" && <Settings status={status} onStatus={setStatus} />}
				{!notPaired && status && screen === "chat" && <Chat status={status} />}
			</main>
		</div>
	);
}
