/**
 * @file The first-run screen, shown while there is no HomeWAV session. It
 * offers both ways to get one, with signing in by password picked first,
 * and shows the same cards Settings uses for each.
 */

import { useEffect, useState } from "preact/hooks";
import { LocalApi } from "../api";
import type { AppConfig, AppStatus } from "../types";
import { classes } from "../util";
import { AccountCard, PhoneSetupCard } from "./Settings";
import { Notice } from "./ui";

/** The two ways to get a session. */
type SetupWay = "password" | "phone";

/** What each choice is called and why someone would pick it. */
const SetupWays: Record<SetupWay, { title: string; hint: string }> = {
	password: {
		title: "Sign in with your password",
		hint: "Simplest. Signing in here signs the HomeWAV app on your phone out.",
	},
	phone: {
		title: "Share your phone's login",
		hint: "The phone stays signed in and keeps its notifications.",
	},
};

interface WelcomeProps {
	status: AppStatus;
	/** Replaces the frame's status, so a successful sign-in opens the chat at once. */
	onStatus: (status: AppStatus) => void;
}

/** Shown in place of the chat until there is a session. */
export function Welcome({ status, onStatus }: WelcomeProps) {
	const [way, setWay] = useState<SetupWay>("password");
	const [config, setConfig] = useState<AppConfig | null>(null);
	const [error, setError] = useState("");
	useEffect(() => {
		LocalApi.config()
			.then(setConfig)
			.catch((reason: unknown) => setError(reason instanceof Error ? reason.message : String(reason)));
	}, []);

	return (
		<div class="mx-auto max-w-3xl space-y-4 p-4">
			<div>
				<h1 class="text-lg font-semibold">Sign in to get started</h1>
				<p class="mt-1 text-sm text-muted">HomeWAV allows one session per account. Pick how this computer gets it. You can switch later in Settings.</p>
			</div>
			<div class="grid gap-2 sm:grid-cols-2" role="group" aria-label="How to sign in">
				{(Object.keys(SetupWays) as SetupWay[]).map((choice) => (
					<button
						key={choice}
						type="button"
						aria-pressed={way === choice}
						onClick={() => setWay(choice)}
						class={classes(
							"rounded-xl border p-3 text-left transition",
							way === choice ? "border-accent bg-panel ring-1 ring-accent" : "border-line bg-panel opacity-80 hover:opacity-100",
						)}
					>
						<span class="block text-sm font-medium">{SetupWays[choice].title}</span>
						<span class="mt-1 block text-xs text-muted">{SetupWays[choice].hint}</span>
					</button>
				))}
			</div>
			{way === "password" && config && <AccountCard config={config} status={status} onSaved={setConfig} onStatus={onStatus} />}
			{way === "password" && !config && !error && <Notice tone="info">Loading…</Notice>}
			{way === "phone" && <PhoneSetupCard status={status} />}
			{error && <Notice tone="bad">{error}</Notice>}
		</div>
	);
}
