/** @file The Settings screen: connection, phone setup, pairing, storage. */

import { useEffect, useState } from "preact/hooks";
import { LocalApi } from "../api";
import { AppName, DefaultPort, MaxPort, MinPort, RestartPollMs, RestartWaitMs } from "../constants";
import type { AppConfig, AppStatus, MediaConfig } from "../types";
import { formatIsoTime } from "../util";
import { QrCode } from "./QrCode";
import { Button, Card, CopyRow, Field, Notice } from "./ui";

/** Characters per group when the CA fingerprint is shown for comparison. */
const FingerprintGroup = 4;

/** A bucket form with nothing filled in. */
const EmptyMedia: MediaConfig = {
	endpoint: "",
	region: "",
	bucket: "",
	access_key_id: "",
	secret_access_key: "",
	public_base_url: "",
	key_prefix: "",
	path_style: false,
	acl: "",
};

interface SettingsProps {
	status: AppStatus;
	/** Replaces the frame's status after an action changed it. */
	onStatus: (status: AppStatus) => void;
}

interface SectionProps {
	status: AppStatus;
}

/** Splits a hex fingerprint into groups, so two displays can be compared by eye. */
const groupFingerprint = (hex: string): string => hex.toUpperCase().match(new RegExp(`.{1,${FingerprintGroup}}`, "g"))?.join(" ") ?? "";

/** The origin a LAN URL belongs to, for building the CA link next to it. */
const originOf = (url: string): string => new URL(url).origin;

/** Connection state and manual renewal. */
function ConnectionCard({ status, onStatus }: SettingsProps) {
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState("");
	const session = status.session;
	const renew = async () => {
		setBusy(true);
		setError("");
		try {
			onStatus(await LocalApi.renew());
		} catch (reason) {
			setError(reason instanceof Error ? reason.message : String(reason));
		} finally {
			setBusy(false);
		}
	};
	return (
		<Card title="Connection" hint={`The session shared with the phone app for ${status.api_host}.`}>
			{session.have_session ? (
				<Notice tone="good">Connected.</Notice>
			) : (
				<Notice tone="warn">No session yet. Sign in below, or follow the phone setup.</Notice>
			)}
			<dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
				<dt class="text-muted">Session changed</dt>
				<dd>{formatIsoTime(session.session_updated)}</dd>
				<dt class="text-muted">Stays renewable until</dt>
				<dd>{session.can_renew ? formatIsoTime(session.remember_expires) : "not renewable yet"}</dd>
				<dt class="text-muted">Account id</dt>
				<dd>{status.visitor_id || "not seen yet"}</dd>
			</dl>
			<div class="flex items-center gap-3">
				<Button onClick={() => void renew()} disabled={busy || !session.can_renew}>
					{busy ? "Renewing…" : "Renew now"}
				</Button>
				<span class="text-xs text-muted">Renewal happens by itself. Use this only to check that it works.</span>
			</div>
			{error && <Notice tone="bad">{error}</Notice>}
		</Card>
	);
}

/** The three steps that connect a phone. */
function PhoneSetupCard({ status }: SectionProps) {
	const pacUrl = status.pac_urls[0];
	if (!pacUrl) {
		return (
			<Card title="Phone setup">
				<Notice tone="warn">This computer has no local network address. Connect it to the same Wi-Fi as the phone.</Notice>
			</Card>
		);
	}
	const caUrl = status.ca_url ? originOf(pacUrl) + status.ca_url : "";
	const connected = status.session.have_session && status.session.can_renew;
	return (
		<Card title="Phone setup" hint="Do this once per phone. The phone must be on the same Wi-Fi as this computer.">
			<ol class="list-decimal space-y-5 pl-5 text-sm">
				<li>
					<p class="font-medium">Trust this computer's certificate</p>
					{caUrl ? (
						<div class="mt-2 space-y-2">
							<p class="text-muted">Open this link in Safari on the phone and allow the profile download.</p>
							<QrCode text={caUrl} label="QR code of the certificate link" />
							<CopyRow value={caUrl} />
							<p class="text-muted">
								Then install it under Settings › General › VPN &amp; Device Management, and switch it on under Settings › General › About › Certificate Trust
								Settings.
							</p>
							<p class="text-xs text-muted">Fingerprint (SHA-256): {groupFingerprint(status.ca_fingerprint)}</p>
						</div>
					) : (
						<p class="mt-2 text-muted">This instance was started with its own certificate. Install the authority that issued it on the phone.</p>
					)}
				</li>
				<li>
					<p class="font-medium">Point the phone's Wi-Fi at the proxy</p>
					<div class="mt-2 space-y-2">
						<p class="text-muted">Settings › Wi-Fi › (your network) › Configure Proxy › Automatic, and enter this URL. Only {status.api_host} is routed here.</p>
						<CopyRow value={pacUrl} />
						{status.pac_urls.length > 1 && <p class="text-xs text-muted">Other addresses of this computer: {status.pac_urls.slice(1).join(", ")}</p>}
					</div>
				</li>
				<li>
					<p class="font-medium">Open the HomeWAV app once</p>
					<div class="mt-2">
						{connected ? <Notice tone="good">Done: the app's session was captured.</Notice> : <Notice tone="info">Waiting for the app to connect through the proxy…</Notice>}
					</div>
				</li>
			</ol>
		</Card>
	);
}

/** The link that admits another device's browser. */
function PairingCard({ status }: SectionProps) {
	const pairingUrl = status.pairing_urls[0];
	if (!pairingUrl) {
		return null;
	}
	return (
		<Card title="Use it from the phone" hint="Open this link in the phone's browser to use this screen from there. Treat it like a password: it gives access to the account.">
			<QrCode text={pairingUrl} label="QR code of the pairing link" />
			<CopyRow value={pairingUrl} />
		</Card>
	);
}

interface StorageCardProps {
	config: AppConfig;
	onSaved: (config: AppConfig) => void;
}

/** The upload bucket and the account id override. */
function StorageCard({ config, onSaved }: StorageCardProps) {
	const [media, setMedia] = useState<MediaConfig>({ ...EmptyMedia, ...config.media });
	const [visitorId, setVisitorId] = useState(config.visitor_id);
	const [busy, setBusy] = useState(false);
	const [saved, setSaved] = useState(false);
	const [error, setError] = useState("");
	const set = (field: keyof MediaConfig) => (value: string) => {
		setSaved(false);
		setMedia((previous) => ({ ...previous, [field]: value }));
	};
	const save = async (event: Event) => {
		event.preventDefault();
		setBusy(true);
		setError("");
		try {
			const next = await LocalApi.saveConfig({ visitor_id: visitorId, media });
			onSaved(next);
			setMedia({ ...EmptyMedia, ...next.media });
			setSaved(true);
		} catch (reason) {
			setError(reason instanceof Error ? reason.message : String(reason));
		} finally {
			setBusy(false);
		}
	};
	return (
		<Card title="Media storage" hint="Pictures and videos are sent as a public link. Fill this in to upload files from the composer to your own S3-compatible bucket.">
			<form onSubmit={save} class="space-y-3">
				<div class="grid gap-3 sm:grid-cols-2">
					<Field label="Bucket" value={media.bucket} onInput={set("bucket")} />
					<Field label="Region" value={media.region} onInput={set("region")} placeholder="us-east-1" />
					<Field label="Access key ID" value={media.access_key_id} onInput={set("access_key_id")} />
					<Field
						label="Secret access key"
						type="password"
						value={media.secret_access_key}
						onInput={set("secret_access_key")}
						placeholder={config.media_secret_set ? "Saved. Leave empty to keep it." : ""}
					/>
					<Field label="Endpoint" type="url" value={media.endpoint} onInput={set("endpoint")} placeholder="Empty for Amazon S3" hint="Only for other providers, such as R2 or MinIO." />
					<Field label="Public base URL" type="url" value={media.public_base_url} onInput={set("public_base_url")} placeholder="Empty to use the bucket's own address" hint="What recipients load files from." />
					<Field label="Key prefix" value={media.key_prefix} onInput={set("key_prefix")} placeholder="hw/" />
					<Field label="ACL" value={media.acl} onInput={set("acl")} placeholder="public-read" hint="Leave empty if the bucket is public by policy." />
				</div>
				<label class="flex items-center gap-2 text-sm">
					<input
						type="checkbox"
						checked={media.path_style}
						onChange={(event) => {
							setSaved(false);
							setMedia({ ...media, path_style: event.currentTarget.checked });
						}}
					/>
					Path-style addressing (needed by MinIO and some providers)
				</label>
				<Field
					label="Account id override"
					inputMode="numeric"
					value={visitorId}
					onInput={(value) => {
						setSaved(false);
						setVisitorId(value);
					}}
					placeholder="Empty to use the id seen from the app"
					hint="Only needed if the contact list stays empty."
				/>
				<div class="flex items-center gap-3">
					<Button kind="primary" type="submit" disabled={busy}>
						{busy ? "Saving…" : "Save"}
					</Button>
					{saved && <Notice tone="good">Saved.</Notice>}
				</div>
				{error && <Notice tone="bad">{error}</Notice>}
			</form>
		</Card>
	);
}

interface AccountCardProps {
	config: AppConfig;
	status: AppStatus;
	onSaved: (config: AppConfig) => void;
	onStatus: (status: AppStatus) => void;
}

/**
 * Signing in with a password. It gets a session without the phone, and a
 * saved password lets the program sign in again by itself whenever the
 * service's remember cookies stop working.
 */
function AccountCard({ config, status, onSaved, onStatus }: AccountCardProps) {
	const [username, setUsername] = useState(config.account_username);
	const [password, setPassword] = useState("");
	const [save, setSave] = useState(true);
	const [busy, setBusy] = useState(false);
	const [notice, setNotice] = useState("");
	const [error, setError] = useState("");
	const run = async (action: () => Promise<void>) => {
		setBusy(true);
		setError("");
		setNotice("");
		try {
			await action();
		} catch (reason) {
			setError(reason instanceof Error ? reason.message : String(reason));
		} finally {
			setBusy(false);
		}
	};
	const signIn = (event: Event) => {
		event.preventDefault();
		void run(async () => {
			onStatus(await LocalApi.signIn(username, password, save));
			onSaved(await LocalApi.config());
			setPassword("");
			setNotice(save ? "Signed in. The password is saved for next time." : "Signed in. Nothing was saved.");
		});
	};
	const forget = () =>
		void run(async () => {
			onSaved(await LocalApi.forgetAccount());
			setNotice("The saved password was removed.");
		});
	const reusesSaved = config.account_password_set && username.trim() === config.account_username;
	return (
		<Card title="Sign in" hint="Optional. Signs in to HomeWAV from this computer, so it works without the phone.">
			<form onSubmit={signIn} class="space-y-3">
				<div class="grid gap-3 sm:grid-cols-2">
					<Field label="Username" value={username} onInput={setUsername} />
					<Field label="Password" type="password" value={password} onInput={setPassword} placeholder={reusesSaved ? "Saved. Leave empty to use it." : ""} />
				</div>
				<label class="flex items-start gap-2 text-sm">
					<input type="checkbox" class="mt-1" checked={save} onChange={(event) => setSave(event.currentTarget.checked)} />
					<span>
						Save the password, so {AppName} can sign in again by itself when HomeWAV logs it out.
						<span class="mt-1 block text-xs text-muted">
							It is saved unencrypted in config.json in the data folder. Anyone who can read that folder, or a backup of it, can read the password.
						</span>
					</span>
				</label>
				<p class="text-xs text-muted">
					HomeWAV allows one session per account. A phone using the proxy shares this one, but a phone that is not (on mobile data, for example) is signed
					out, and signing back in there signs this computer out.
				</p>
				<div class="flex flex-wrap items-center gap-3">
					<Button kind="primary" type="submit" disabled={busy}>
						{busy ? "Signing in…" : "Sign in"}
					</Button>
					{config.account_password_set && (
						<Button onClick={forget} disabled={busy}>
							Forget saved password
						</Button>
					)}
				</div>
				{status.session.login_refused && <Notice tone="bad">The saved password stopped working: {status.session.login_refused}. Sign in again to replace it.</Notice>}
				{notice && <Notice tone="good">{notice}</Notice>}
				{error && <Notice tone="bad">{error}</Notice>}
			</form>
		</Card>
	);
}

interface NetworkCardProps {
	config: AppConfig;
	onSaved: (config: AppConfig) => void;
}

/**
 * The port. Changing it restarts the program, then moves this page to the
 * new address once it answers there.
 */
function NetworkCard({ config, onSaved }: NetworkCardProps) {
	const [port, setPort] = useState(String(config.port || config.listening_port || DefaultPort));
	const [busy, setBusy] = useState(false);
	const [notice, setNotice] = useState("");
	const [error, setError] = useState("");
	const save = async (event: Event) => {
		event.preventDefault();
		const wanted = Number(port);
		if (!Number.isInteger(wanted) || wanted < MinPort || wanted > MaxPort) {
			setError(`Enter a port from ${MinPort} to ${MaxPort}.`);
			return;
		}
		setBusy(true);
		setError("");
		setNotice("");
		try {
			// The default is stored as 0, so a later change of the default applies.
			const saved = await LocalApi.savePort(wanted === DefaultPort ? 0 : wanted);
			onSaved(saved);
			if (saved.port_from_flag) {
				setNotice(`Saved. ${AppName} was started with -listen, which wins until it is started without it.`);
				return;
			}
			if (wanted === saved.listening_port) {
				setNotice("Saved. It is already listening on this port.");
				return;
			}
			if (!saved.can_restart) {
				setNotice(`Saved. Quit ${AppName} and start it again to use the new port.`);
				return;
			}
			setNotice("Restarting…");
			await LocalApi.restart();
			const target = new URL(window.location.href);
			target.port = String(wanted);
			// The old copy has to let go of its port before the new one is checked.
			await LocalApi.waitFor(window.location.origin, false, RestartPollMs, RestartWaitMs);
			if (await LocalApi.waitFor(target.origin, true, RestartPollMs, RestartWaitMs)) {
				window.location.assign(target.href);
				return;
			}
			setNotice("");
			setError(`${AppName} did not come back on port ${wanted}. Something else may be using it; the log in the data folder says why.`);
		} catch (reason) {
			setNotice("");
			setError(reason instanceof Error ? reason.message : String(reason));
		} finally {
			setBusy(false);
		}
	};
	return (
		<Card title="Network" hint="The port for the phone's proxy and for this page. Phones set up with the old port stop connecting until their proxy URL (Phone setup, step 2) is changed to the new one.">
			<form onSubmit={save} class="space-y-3">
				<Field label="Port" inputMode="numeric" value={port} onInput={setPort} placeholder={String(DefaultPort)} hint={`Listening on ${config.listening_port} now.`} />
				<div class="flex items-center gap-3">
					<Button kind="primary" type="submit" disabled={busy}>
						{busy ? "Applying…" : config.can_restart ? "Save and restart" : "Save"}
					</Button>
				</div>
				{notice && <Notice tone="info">{notice}</Notice>}
				{error && <Notice tone="bad">{error}</Notice>}
			</form>
		</Card>
	);
}

/** The Settings screen. */
export function Settings({ status, onStatus }: SettingsProps) {
	const [config, setConfig] = useState<AppConfig | null>(null);
	const [error, setError] = useState("");
	useEffect(() => {
		LocalApi.config()
			.then(setConfig)
			.catch((reason: unknown) => setError(reason instanceof Error ? reason.message : String(reason)));
	}, []);
	return (
		<div class="mx-auto max-w-3xl space-y-4 p-4">
			<ConnectionCard status={status} onStatus={onStatus} />
			{config && <AccountCard config={config} status={status} onSaved={setConfig} onStatus={onStatus} />}
			<PhoneSetupCard status={status} />
			<PairingCard status={status} />
			{config && <StorageCard config={config} onSaved={setConfig} />}
			{config && <NetworkCard config={config} onSaved={setConfig} />}
			{error && <Notice tone="bad">{error}</Notice>}
		</div>
	);
}
